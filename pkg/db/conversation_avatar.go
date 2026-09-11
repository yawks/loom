package db

import (
	"Loom/pkg/models"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateConversationAvatar applies an authoritative photo, including deletion.
func UpdateConversationAvatar(instanceID, conversationID, avatar string) error {
	return UpdateConversationAvatars(instanceID, map[string]string{conversationID: avatar})
}

// UpdateConversationAvatars updates a synchronization batch in one retryable
// transaction. Unknown conversations are left for normal provider discovery.
func UpdateConversationAvatars(instanceID string, avatars map[string]string) error {
	scope := ForProvider(DB, instanceID)
	if instanceID == "" {
		return fmt.Errorf("missing avatar provider instance")
	}
	ids := make([]string, 0, len(avatars))
	for id := range avatars {
		if !scope.OwnsConversation(id) {
			return fmt.Errorf("invalid conversation avatar ownership")
		}
		ids = append(ids, id)
	}
	if DB == nil || len(ids) == 0 {
		return nil
	}
	var accounts []models.LinkedAccount
	var metas []models.MetaContact
	err := Transaction(DB, func(tx *gorm.DB) error {
		accounts = nil
		metas = nil
		var conversations []models.Conversation
		if err := ForProvider(tx, instanceID).Conversations().Where("protocol_conv_id IN ?", ids).Find(&conversations).Error; err != nil {
			return err
		}
		accountIDs := make([]uint, 0, len(conversations))
		byAccount := make(map[uint]string, len(conversations))
		for _, c := range conversations {
			accountIDs = append(accountIDs, c.LinkedAccountID)
			byAccount[c.LinkedAccountID] = avatars[c.ProtocolConvID]
		}
		if len(accountIDs) == 0 {
			return nil
		}
		if err := ForProvider(tx, instanceID).LinkedAccounts().Where("id IN ?", accountIDs).Find(&accounts).Error; err != nil {
			return err
		}
		metaIDs := make([]uint, 0, len(accounts))
		byMeta := make(map[uint]string, len(accounts))
		for i := range accounts {
			accounts[i].AvatarURL = byAccount[accounts[i].ID]
			if accounts[i].MetaContactID != 0 {
				metaIDs = append(metaIDs, accounts[i].MetaContactID)
				byMeta[accounts[i].MetaContactID] = accounts[i].AvatarURL
			}
		}
		if len(metaIDs) > 0 {
			if err := tx.Where("id IN ?", metaIDs).Find(&metas).Error; err != nil {
				return err
			}
			for i := range metas {
				metas[i].AvatarURL = byMeta[metas[i].ID]
			}
		}
		conflict := clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"avatar_url"})}
		if len(accounts) > 0 {
			if err := tx.Omit(clause.Associations).Clauses(conflict).CreateInBatches(&accounts, 100).Error; err != nil {
				return err
			}
		}
		if len(metas) > 0 {
			return tx.Omit(clause.Associations).Clauses(conflict).CreateInBatches(&metas, 100).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, account := range accounts {
		ContactStore.UpsertLinkedAccount(account)
	}
	for _, meta := range metas {
		ContactStore.UpsertMetaContact(meta)
	}
	return nil
}
