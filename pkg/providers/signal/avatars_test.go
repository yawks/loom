package signal

import (
	"context"
	"fmt"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
	"strings"
	"testing"
)

type avatarClientStub struct {
	group     *signalmeow.Group
	data      []byte
	err       error
	downloads int
	revision  uint32
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
