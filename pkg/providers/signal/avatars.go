package signal

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

type groupAvatarClient interface {
	RetrieveGroupByID(context.Context, types.GroupIdentifier, uint32) (*signalmeow.Group, *signalmeow.SendEndorsementCache, error)
	DownloadGroupAvatar(context.Context, string, types.SerializedGroupMasterKey) ([]byte, error)
}

func groupAvatar(client groupAvatarClient, chatID string, revision uint32) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	group, _, err := client.RetrieveGroupByID(ctx, types.GroupIdentifier(chatID), revision)
	if err != nil {
		return "", err
	}
	if group == nil {
		return "", fmt.Errorf("signal: missing group")
	}
	if group.AvatarPath == "" {
		return "", nil
	}
	data, err := client.DownloadGroupAvatar(ctx, group.AvatarPath, group.GroupMasterKey)
	if err != nil {
		return "", err
	}
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") || len(data) > 4<<20 {
		return "", fmt.Errorf("signal: invalid group avatar")
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (p *Provider) refreshGroupAvatar(chatID string, revision uint32) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	instance := p.instanceID()
	if client == nil || instance == "" {
		return
	}
	avatar, err := groupAvatar(client, chatID, revision)
	if err != nil {
		log.Printf("signal: download group avatar: %v", err)
		return
	}
	if err := db.UpdateConversationAvatar(instance, core.BuildConvID(instance, chatID), avatar); err != nil {
		log.Printf("signal: update group avatar: %v", err)
		return
	}
	p.emit(core.GroupChangeEvent{InstanceID: instance, ConversationID: core.BuildConvID(instance, chatID), ChangeType: core.GroupChangeUpdated, Timestamp: time.Now().Unix()})
}

func (p *Provider) enrichGroupAvatars(client groupAvatarClient, accounts []models.LinkedAccount) {
	instance := p.instanceID()
	if instance == "" {
		return
	}
	jobs := make(chan int)
	succeeded := make([]bool, len(accounts))
	var workers sync.WaitGroup
	for worker := 0; worker < min(4, len(accounts)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				avatar, err := groupAvatar(client, accounts[index].UserID, 0)
				if err != nil {
					log.Printf("signal: download group avatar: %v", err)
					if cached, ok := db.ContactStore.FindByProviderUser(instance, accounts[index].UserID); ok {
						avatar = cached.AvatarURL
					}
				}
				succeeded[index] = err == nil
				accounts[index].AvatarURL = avatar
			}
		}()
	}
	for index := range accounts {
		if accounts[index].IsGroup {
			jobs <- index
		}
	}
	close(jobs)
	workers.Wait()
	avatars := make(map[string]string)
	for index, account := range accounts {
		if succeeded[index] {
			avatars[core.BuildConvID(instance, account.UserID)] = account.AvatarURL
		}
	}
	if err := db.UpdateConversationAvatars(instance, avatars); err != nil {
		log.Printf("signal: update group avatars: %v", err)
	}
}
