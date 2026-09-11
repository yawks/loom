package teams

import (
	"Loom/pkg/db"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func (p *Provider) conversationAvatar(client *msteams.Client, chat msteams.Chat) string {
	if chat.Type != msteams.ChatType1on1 {
		if chat.Picture == "" {
			return ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		data, _, err := client.FetchChatAvatar(ctx, chat.ID, chat.Picture)
		if err == nil && len(data) <= 4<<20 && strings.HasPrefix(http.DetectContentType(data), "image/") {
			return "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
		// Temporary media failures do not mean the remote photo was removed.
		if account, ok := db.ContactStore.FindByProviderUser(p.instance, chat.ID); ok {
			return account.AvatarURL
		}
		return ""
	}
	members := chat.Members
	if len(members) == 0 {
		if detailed, err := client.GetChat(context.Background(), chat.ID); err == nil {
			members = detailed.Members
		}
	}
	for _, member := range members {
		if member.MRI == "" || strings.EqualFold(member.MRI, client.UserMRI()) {
			continue
		}
		return p.cachedAvatar(client, member.MRI)
	}
	return ""
}

func (p *Provider) cachedAvatar(client *msteams.Client, mri string) string {
	if mri == "" || strings.EqualFold(mri, client.UserMRI()) {
		return ""
	}
	key := mriLookupKey(mri)
	p.fileMu.RLock()
	_, failed := p.avatarFailures[key]
	p.fileMu.RUnlock()
	if failed {
		return ""
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	cacheDir := filepath.Join(configDir, "Loom", p.instance, "avatars")
	name := fmt.Sprintf("%x.img", sha256.Sum256([]byte(strings.ToLower(mri))))
	path := filepath.Join(cacheDir, name)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path
	}
	data, _, err := client.FetchAvatar(context.Background(), mri)
	if err != nil || len(data) == 0 {
		p.fileMu.Lock()
		p.avatarFailures[key] = struct{}{}
		p.fileMu.Unlock()
		return ""
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return ""
	}
	return path
}
