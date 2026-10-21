package signal

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

type avatarClientStub struct {
	group     *signalmeow.Group
	data      []byte
	err       error
	downloads int
	revision  uint32
}

type contactAvatarStub struct {
	avatarClientStub
	profile   *types.Profile
	requested []uuid.UUID
}

func (c *contactAvatarStub) RetrieveProfileByID(ctx context.Context, id uuid.UUID, _ time.Duration) (*types.Profile, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, fmt.Errorf("missing profile timeout")
	}
	c.requested = append(c.requested, id)
	return c.profile, nil
}

func (c *contactAvatarStub) DownloadUserAvatar(ctx context.Context, path string, key libsignalgo.ProfileKey) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok || path != c.profile.AvatarPath || key != c.profile.Key {
		return nil, fmt.Errorf("wrong avatar request")
	}
	c.downloads++
	return c.data, c.err
}

func TestContactAvatarDownloadsAndValidatesDecryptedImage(t *testing.T) {
	c := &contactAvatarStub{profile: &types.Profile{AvatarPath: "photo", Key: libsignalgo.ProfileKey{1}}}
	c.data = []byte("\x89PNG\r\n\x1a\nimage")
	id := uuid.NewString()
	photo, err := contactAvatar(c, id)
	if err != nil || !strings.HasPrefix(photo, "data:image/png;base64,") {
		t.Fatalf("photo=%q err=%v", photo, err)
	}
	c.err = fmt.Errorf("network failure")
	if _, err := contactAvatar(c, id); err == nil {
		t.Fatal("download failure treated as deletion")
	}
	c.err = nil
	c.data = []byte("not an image")
	if _, err := contactAvatar(c, id); err == nil {
		t.Fatal("accepted invalid image")
	}
	c.profile.AvatarPath = "clear"
	photo, err = contactAvatar(c, id)
	if err != nil || photo != "" || c.downloads != 3 {
		t.Fatal("explicit removal attempted a download")
	}
	c.profile.AvatarPath = ""
	if _, err := contactAvatar(c, id); err == nil {
		t.Fatal("unknown avatar treated as deletion")
	}
}

func TestContactAvatarEnrichmentIsolatesProvider(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := database.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := db.DB
	db.DB = database
	t.Cleanup(func() {
		db.DB = previous
		if previous != nil {
			_ = db.ContactStore.Load()
		}
	})
	if err := database.AutoMigrate(&models.LinkedAccount{}, &models.Conversation{}, &models.MetaContact{}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	for _, instance := range []string{"signal-avatar-test", "other-avatar-test"} {
		account := models.LinkedAccount{ProviderInstanceID: instance, UserID: id, AvatarURL: "old"}
		if err := database.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: core.BuildConvID(instance, id)}
		if err := database.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ContactStore.Load(); err != nil {
		t.Fatal(err)
	}
	p := NewProvider().(*Provider)
	p.config = core.ProviderConfig{"_instance_id": "signal-avatar-test"}
	c := &contactAvatarStub{profile: &types.Profile{AvatarPath: "photo"}}
	c.data = []byte("\x89PNG\r\n\x1a\nimage")
	accounts := []models.LinkedAccount{{ProviderInstanceID: "signal-avatar-test", UserID: id}}
	p.enrichAvatars(c, accounts)
	if len(c.requested) != 1 || len(p.events) != 0 {
		t.Fatal("unexpected avatar work or events")
	}
	var rows []models.LinkedAccount
	if err := database.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].AvatarURL != accounts[0].AvatarURL || !strings.HasPrefix(rows[0].AvatarURL, "data:image/png;") || rows[1].AvatarURL != "old" {
		t.Fatalf("unexpected provider avatars: %+v", rows)
	}
}

func (c *avatarClientStub) RetrieveGroupByID(ctx context.Context, id types.GroupIdentifier, revision uint32) (*signalmeow.Group, *signalmeow.SendEndorsementCache, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, nil, fmt.Errorf("missing timeout")
	}
	c.revision = revision
	return c.group, nil, nil
}
func (c *avatarClientStub) DownloadGroupAvatar(ctx context.Context, path string, key types.SerializedGroupMasterKey) ([]byte, error) {
	c.downloads++
	if path != c.group.AvatarPath || key != c.group.GroupMasterKey {
		return nil, fmt.Errorf("wrong decryption key or path")
	}
	return c.data, c.err
}
func TestGroupAvatarUsesRevisionAndDecryptionAndSupportsRemoval(t *testing.T) {
	c := &avatarClientStub{group: &signalmeow.Group{AvatarPath: "photo", GroupMasterKey: "key"}, data: []byte("\x89PNG\r\n\x1a\nimage")}
	photo, err := groupAvatar(c, "group", 42)
	if err != nil || !strings.HasPrefix(photo, "data:image/png;base64,") || c.revision != 42 {
		t.Fatalf("photo=%s err=%v revision=%d", photo, err, c.revision)
	}
	c.err = fmt.Errorf("network failure")
	if _, err := groupAvatar(c, "group", 42); err == nil {
		t.Fatal("download failure treated as deletion")
	}
	c.group.AvatarPath = ""
	photo, err = groupAvatar(c, "group", 43)
	if err != nil || photo != "" || c.downloads != 2 {
		t.Fatal("removal attempted a download")
	}
}
