package matrix

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
)

// Authenticated Matrix media cannot be displayed by handing a download URL to
// the webview: it does not have the provider's bearer token.
func (p *Provider) downloadAvatar(ctx context.Context, reference string) (string, error) {
	if reference == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, mimeType, err := p.GetAttachmentData(ctx, reference)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(mimeType, "image/") || len(data) > 4<<20 {
		return "", fmt.Errorf("matrix: invalid avatar image")
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// updateRoomAvatars coalesces state and timeline before writing one sync batch.
func (p *Provider) updateRoomAvatars(ctx context.Context, rooms map[string][][]matrixEvent) {
	instance := p.getInstanceID()
	if instance == "" {
		log.Print("matrix: cannot update avatar without provider instance")
		return
	}
	avatars := make(map[string]string)
	for roomID, batches := range rooms {
		reference, found := latestRoomAvatar(batches...)
		if !found {
			continue
		}
		avatar, err := p.downloadAvatar(ctx, reference)
		if err != nil {
			log.Printf("matrix: download room avatar: %v", err)
			continue
		}
		avatars[p.namespacedRoom(roomID)] = avatar
	}
	if err := db.UpdateConversationAvatars(instance, avatars); err != nil {
		log.Printf("matrix: update room avatars: %v", err)
		return
	}
	for conversationID := range avatars {
		p.emit(core.GroupChangeEvent{InstanceID: instance, ConversationID: conversationID, ChangeType: core.GroupChangeUpdated, Timestamp: time.Now().Unix()})
	}
}

func latestRoomAvatar(batches ...[]matrixEvent) (reference string, found bool) {
	// State precedes timeline. Empty content explicitly removes the photo.
	for _, events := range batches {
		for _, event := range events {
			if event.Type != "m.room.avatar" || event.StateKey == nil || *event.StateKey != "" {
				continue
			}
			var content struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(event.Content, &content) == nil {
				reference, found = content.URL, true
			}
		}
	}
	return
}
