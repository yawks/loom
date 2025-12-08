package whatsapp

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow/types"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// WhatsAppExtraData stores additional data for WhatsApp contacts in LinkedAccount.Extra
type WhatsAppExtraData struct {
	LID         string            `json:"lid,omitempty"`         // Linked Device ID (e.g., "176188215558395@lid")
	PhoneNumber string            `json:"phoneNumber,omitempty"` // Phone number JID (e.g., "33677815440@s.whatsapp.net")
	Aliases     map[string]string `json:"aliases,omitempty"`     // Map of alternative IDs to canonical ID
}

// resolveContactID resolves a contact ID (LID or phone number) to the canonical phone number JID.
// This is the unified function to handle LID/phone number conversions.
func (w *WhatsAppProvider) resolveContactID(contactID string) (string, error) {
	if contactID == "" {
		return "", fmt.Errorf("contact ID is empty")
	}

	// Parse the contact ID
	jid, err := types.ParseJID(contactID)
	if err != nil {
		return "", fmt.Errorf("invalid contact ID: %w", err)
	}

	// If it's already a phone number JID, return it as-is
	if jid.Server == types.DefaultUserServer {
		return contactID, nil
	}

	// If it's a LID, try to resolve it
	if jid.Server == "lid" {
		// Strategy 1: Check memory cache (fastest)
		w.lidToJIDMu.RLock()
		if resolved, found := w.lidToJIDMap[contactID]; found && resolved != "" {
			w.lidToJIDMu.RUnlock()
			return resolved, nil
		}
		w.lidToJIDMu.RUnlock()

		// Strategy 2: Check database LID mappings
		if db.DB != nil {
			var mapping models.LIDMapping
			// Use a silent session to prevent GORM from logging "record not found"
			silentDB := db.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			err := silentDB.Where("lid = ? AND protocol = ?", contactID, "whatsapp").First(&mapping).Error
			if err == nil {
				// Update cache
				w.lidToJIDMu.Lock()
				w.lidToJIDMap[contactID] = mapping.JID
				w.lidToJIDMu.Unlock()
				return mapping.JID, nil
			}
			// Don't log "record not found" - it's normal for LIDs that haven't been mapped yet
			// Use errors.Is to check for gorm.ErrRecordNotFound without triggering GORM's automatic logging
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				// Only log if it's a real error, not just "not found"
				fmt.Printf("WhatsApp: Error checking LID mapping for %s: %v\n", contactID, err)
			}
		}

		// Strategy 3: Check LinkedAccount.Extra for stored mappings
		if db.DB != nil {
			var accounts []models.LinkedAccount
			err := db.DB.Where("protocol = ? AND extra LIKE ?", "whatsapp", "%"+contactID+"%").Find(&accounts).Error
			if err == nil {
				for _, acc := range accounts {
					if acc.Extra != "" {
						var extraData WhatsAppExtraData
						if err := json.Unmarshal([]byte(acc.Extra), &extraData); err == nil {
							if extraData.LID == contactID && extraData.PhoneNumber != "" {
								return extraData.PhoneNumber, nil
							}
							if alias, ok := extraData.Aliases[contactID]; ok {
								return alias, nil
							}
						}
					}
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				// Only log if it's a real error, not just "not found"
				fmt.Printf("WhatsApp: Error checking LinkedAccount.Extra for %s: %v\n", contactID, err)
			}
		}

		// Could not resolve LID
		// Could not resolve LID - log warning and return original ID (fallback)
		fmt.Printf("WhatsApp: WARNING - Could not resolve LID to phone number: %s, using LID as fallback\n", contactID)
		return contactID, nil
	}

	// For other server types (groups, etc.), return as-is
	return contactID, nil
}

// resolveContactIDForGroup resolves a contact ID in a group context.
// In groups, we also check the groupParticipants cache.
func (w *WhatsAppProvider) resolveContactIDForGroup(contactID string, groupJID types.JID) (string, error) {
	// First try standard resolution
	resolved, err := w.resolveContactID(contactID)
	if err == nil {
		return resolved, nil
	}

	// If standard resolution failed and we're in a group, try group participants cache
	if groupJID.Server == types.GroupServer {
		jid, parseErr := types.ParseJID(contactID)
		if parseErr == nil && jid.Server == "lid" {
			w.mu.RLock()
			groupParticipants, hasGroup := w.groupParticipants[groupJID.String()]
			w.mu.RUnlock()

			if hasGroup {
				if phoneNumber, ok := groupParticipants[jid]; ok {
					return phoneNumber, nil
				}
			}
		}
	}

	// Return original error
	return "", err
}

// updateLinkedAccountExtra updates the Extra field of a LinkedAccount with WhatsApp-specific data.
func (w *WhatsAppProvider) updateLinkedAccountExtra(userID string, extraData WhatsAppExtraData) error {
	if db.DB == nil {
		return nil
	}

	var account models.LinkedAccount
	err := db.DB.Where("protocol = ? AND user_id = ?", "whatsapp", userID).First(&account).Error
	if err != nil {
		// Account doesn't exist yet, that's okay
		return nil
	}

	// Parse existing extra data if present
	existingData := WhatsAppExtraData{}
	if account.Extra != "" {
		if err := json.Unmarshal([]byte(account.Extra), &existingData); err != nil {
			// If parsing fails, start fresh
			existingData = WhatsAppExtraData{}
		}
	}

	// Merge new data
	if extraData.LID != "" {
		existingData.LID = extraData.LID
	}
	if extraData.PhoneNumber != "" {
		existingData.PhoneNumber = extraData.PhoneNumber
	}
	if existingData.Aliases == nil {
		existingData.Aliases = make(map[string]string)
	}
	for k, v := range extraData.Aliases {
		existingData.Aliases[k] = v
	}

	// Marshal back to JSON
	extraJSON, err := json.Marshal(existingData)
	if err != nil {
		return fmt.Errorf("failed to marshal extra data: %w", err)
	}

	// Update account
	account.Extra = string(extraJSON)
	if err := db.DB.Save(&account).Error; err != nil {
		return fmt.Errorf("failed to update LinkedAccount extra: %w", err)
	}

	return nil
}

// storeContactMapping stores a mapping between LID and phone number in LinkedAccount.
// This ensures we can resolve contacts even when the mapping cache is cleared.
func (w *WhatsAppProvider) storeContactMapping(lid, phoneNumber string) error {
	if lid == "" || phoneNumber == "" {
		return nil
	}

	if db.DB == nil {
		return nil
	}

	// Determine which is the canonical ID (phone number)
	canonicalID := phoneNumber
	extraData := WhatsAppExtraData{
		PhoneNumber: phoneNumber,
		LID:         lid,
		Aliases: map[string]string{
			lid: phoneNumber,
		},
	}

	// Try to find existing account by phone number (canonical ID)
	var account models.LinkedAccount
	err := db.DB.Where("protocol = ? AND user_id = ?", "whatsapp", phoneNumber).First(&account).Error
	if err != nil {
		// Account doesn't exist by phone number, try by LID in Extra field
		var accounts []models.LinkedAccount
		if err := db.DB.Where("protocol = ? AND extra != ''", "whatsapp").Find(&accounts).Error; err == nil {
			for _, acc := range accounts {
				if acc.Extra != "" {
					var existingExtra WhatsAppExtraData
					if err := json.Unmarshal([]byte(acc.Extra), &existingExtra); err == nil {
						if existingExtra.LID == lid || existingExtra.PhoneNumber == phoneNumber {
							account = acc
							// Update to use phone number as canonical ID
							if account.UserID != phoneNumber {
								account.UserID = phoneNumber
							}
							canonicalID = phoneNumber
							break
						}
					}
				}
			}
		}
		// If still no account found, that's okay - it will be created when needed
		if account.ID == 0 {
			return nil
		}
	}

	// Update the account
	return w.updateLinkedAccountExtra(canonicalID, extraData)
}
