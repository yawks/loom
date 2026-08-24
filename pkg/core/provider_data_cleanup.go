package core

import (
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
)

// deleteProviderData hard-deletes the complete graph owned by one provider
// instance and returns its local media paths for post-commit cleanup.
func deleteProviderData(database *gorm.DB, instanceID string) ([]string, error) {
	var mediaPaths []string
	err := database.Transaction(func(tx *gorm.DB) error {
		var accounts []models.LinkedAccount
		if err := tx.Unscoped().Where("provider_instance_id = ?", instanceID).Find(&accounts).Error; err != nil {
			return err
		}
		accountIDs := make([]uint, 0, len(accounts))
		metaIDs := make([]uint, 0, len(accounts))
		usersByProtocol := make(map[string][]string)
		for _, account := range accounts {
			accountIDs = append(accountIDs, account.ID)
			metaIDs = append(metaIDs, account.MetaContactID)
			usersByProtocol[account.Protocol] = append(usersByProtocol[account.Protocol], account.UserID)
		}

		var conversationIDs []uint
		if len(accountIDs) > 0 {
			if err := tx.Model(&models.Conversation{}).Where("linked_account_id IN ?", accountIDs).Pluck("id", &conversationIDs).Error; err != nil {
				return err
			}
		}

		// Include legacy messages saved with conversation_id=0 but a namespaced
		// protocol_conv_id belonging to this instance.
		pattern := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(instanceID) + "::%"
		messageQuery := tx.Unscoped().Model(&models.Message{})
		if len(conversationIDs) > 0 {
			messageQuery = messageQuery.Where("conversation_id IN ? OR protocol_conv_id LIKE ? ESCAPE '\\'", conversationIDs, pattern)
		} else {
			messageQuery = messageQuery.Where("protocol_conv_id LIKE ? ESCAPE '\\'", pattern)
		}
		var messages []models.Message
		if err := messageQuery.Find(&messages).Error; err != nil {
			return err
		}
		messageIDs := make([]uint, 0, len(messages))
		for _, message := range messages {
			messageIDs = append(messageIDs, message.ID)
			mediaPaths = append(mediaPaths, attachmentLocalPaths(message.Attachments)...)
		}

		if len(messageIDs) > 0 {
			if err := tx.Unscoped().Where("message_id IN ?", messageIDs).Delete(&models.Reaction{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("message_id IN ?", messageIDs).Delete(&models.MessageReceipt{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("id IN ?", messageIDs).Delete(&models.Message{}).Error; err != nil {
				return err
			}
		}
		if len(conversationIDs) > 0 {
			if err := tx.Unscoped().Where("conversation_id IN ?", conversationIDs).Delete(&models.GroupParticipant{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("id IN ?", conversationIDs).Delete(&models.Conversation{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Where("provider_instance_id = ?", instanceID).Delete(&models.MessagePin{}).Error; err != nil {
			return err
		}
		if len(accountIDs) > 0 {
			if err := tx.Unscoped().Where("id IN ?", accountIDs).Delete(&models.LinkedAccount{}).Error; err != nil {
				return err
			}
		}

		for protocol, userIDs := range usersByProtocol {
			for _, userID := range userIDs {
				var stillUsed int64
				if err := tx.Model(&models.LinkedAccount{}).Where("protocol = ? AND user_id = ?", protocol, userID).Count(&stillUsed).Error; err != nil {
					return err
				}
				if stillUsed == 0 {
					if err := tx.Unscoped().Where("protocol = ? AND jid = ?", protocol, userID).Delete(&models.LIDMapping{}).Error; err != nil {
						return err
					}
					if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&models.ContactAlias{}).Error; err != nil {
						return err
					}
				}
			}
		}
		for _, metaID := range metaIDs {
			var remaining int64
			if err := tx.Model(&models.LinkedAccount{}).Where("meta_contact_id = ?", metaID).Count(&remaining).Error; err != nil {
				return err
			}
			if remaining == 0 {
				if err := tx.Unscoped().Where("id = ?", metaID).Delete(&models.MetaContact{}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Unscoped().Where("instance_id = ?", instanceID).Delete(&models.ProviderConfiguration{}).Error
	})
	return mediaPaths, err
}

func attachmentLocalPaths(raw string) []string {
	var attachments []models.Attachment
	if raw == "" || json.Unmarshal([]byte(raw), &attachments) != nil {
		return nil
	}
	var paths []string
	for _, attachment := range attachments {
		for _, candidate := range []string{attachment.URL, attachment.Thumbnail} {
			parsed, err := url.Parse(candidate)
			if candidate != "" && err == nil && parsed.Scheme == "" && filepath.IsAbs(candidate) {
				paths = append(paths, filepath.Clean(candidate))
			}
		}
	}
	return paths
}

func removeUnreferencedProviderMedia(database *gorm.DB, candidates []string) {
	if len(candidates) == 0 {
		return
	}
	var remaining []string
	if err := database.Unscoped().Model(&models.Message{}).Where("attachments <> ''").Pluck("attachments", &remaining).Error; err != nil {
		return
	}
	referenced := make(map[string]bool)
	for _, raw := range remaining {
		for _, path := range attachmentLocalPaths(raw) {
			referenced[path] = true
		}
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	loomRoot, err := filepath.Abs(filepath.Join(configDir, "Loom"))
	if err != nil {
		return
	}
	for _, path := range candidates {
		absolute, err := filepath.Abs(path)
		if err != nil || referenced[absolute] {
			continue
		}
		relative, err := filepath.Rel(loomRoot, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if err := os.Remove(absolute); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Provider data cleanup: WARNING - failed to remove media %s: %v\n", absolute, err)
		}
	}
}
