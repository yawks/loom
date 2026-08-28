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
		cleanLID := jid.ToNonAD().String()

		// Strategy 1: Check memory cache (fastest)
		w.lidToJIDMu.RLock()
		if resolved, found := w.lidToJIDMap[contactID]; found && resolved != "" {
			w.lidToJIDMu.RUnlock()
			return resolved, nil
		}
		if resolved, found := w.lidToJIDMap[cleanLID]; found && resolved != "" {
			w.lidToJIDMu.RUnlock()
			return resolved, nil
		}
		w.lidToJIDMu.RUnlock()

		// Strategy 2: Check whatsmeow LID store
		if w.client != nil && w.client.Store != nil && w.client.Store.LIDs != nil {
			phoneJID, err := w.client.Store.LIDs.GetPNForLID(w.ctx, jid.ToNonAD())
			if err == nil && !phoneJID.IsEmpty() {
				resolved := phoneJID.ToNonAD().String()
				w.lidToJIDMu.Lock()
				w.lidToJIDMap[contactID] = resolved
				w.lidToJIDMap[cleanLID] = resolved
				w.lidToJIDMu.Unlock()
				return resolved, nil
			}
		}

		// Strategy 3: Check database LID mappings
		if db.DB != nil {
			var mapping models.LIDMapping
			// Use a silent session to prevent GORM from logging "record not found"
			silentDB := db.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
			err := silentDB.Where("(lid = ? OR lid = ?) AND protocol = ?", contactID, cleanLID, "whatsapp").First(&mapping).Error
			if err == nil {
				// Update cache
				w.lidToJIDMu.Lock()
				w.lidToJIDMap[contactID] = mapping.JID
				w.lidToJIDMap[cleanLID] = mapping.JID
				w.lidToJIDMu.Unlock()
				return mapping.JID, nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				fmt.Printf("WhatsApp: Error checking LID mapping for %s: %v\n", contactID, err)
			}
		}

		// Strategy 4: Check LinkedAccount.Extra for stored mappings
		if db.DB != nil {
			var accounts []models.LinkedAccount
			err := db.DB.Where(
				"protocol = ? AND provider_instance_id = ? AND (extra LIKE ? OR extra LIKE ?)",
				"whatsapp", w.getInstanceId(), "%"+contactID+"%", "%"+cleanLID+"%",
			).Find(&accounts).Error
			if err == nil {
				for _, acc := range accounts {
					if acc.Extra != "" {
						var extraData WhatsAppExtraData
						if err := json.Unmarshal([]byte(acc.Extra), &extraData); err == nil {
							if (extraData.LID == contactID || extraData.LID == cleanLID) && extraData.PhoneNumber != "" {
								return extraData.PhoneNumber, nil
							}
							if alias, ok := extraData.Aliases[contactID]; ok {
								return alias, nil
							}
							if alias, ok := extraData.Aliases[cleanLID]; ok {
								return alias, nil
							}
						}
					}
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				fmt.Printf("WhatsApp: Error checking LinkedAccount.Extra for %s: %v\n", contactID, err)
			}
		}

		// Could not resolve LID - log warning and return original ID (fallback)
		fmt.Printf("WhatsApp: WARNING - Could not resolve LID to phone number: %s, using LID as fallback\n", contactID)
		return contactID, nil
	}

	// For other server types (groups, etc.), return as-is
	return contactID, nil
}

// canonicalReactionUserID keeps reaction authors consistent with message
// senders. WhatsApp commonly uses a LID for group reactions even when the
// participant is already known by their phone-number JID.
func (w *WhatsAppProvider) canonicalReactionUserID(userID string) string {
	return w.NormalizeParticipantID(userID)
}

// NormalizeParticipantID removes device qualification and resolves LID aliases
// before an identity crosses the backend/frontend boundary.
func (w *WhatsAppProvider) NormalizeParticipantID(userID string) string {
	jid, err := types.ParseJID(userID)
	if err != nil {
		return userID
	}
	nonAD := jid.ToNonAD().String()
	resolved, resolveErr := w.resolveContactID(nonAD)
	if resolveErr == nil && resolved != "" {
		if resolvedJID, parseErr := types.ParseJID(resolved); parseErr == nil {
			return resolvedJID.ToNonAD().String()
		}
		return resolved
	}
	return nonAD
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
	err := db.DB.Where(
		"protocol = ? AND provider_instance_id = ? AND user_id = ?",
		"whatsapp", w.getInstanceId(), userID,
	).First(&account).Error
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
	instanceID := w.getInstanceId()
	err := db.DB.Where(
		"protocol = ? AND provider_instance_id = ? AND user_id = ?",
		"whatsapp", instanceID, phoneNumber,
	).First(&account).Error
	if err != nil {
		// Account doesn't exist by phone number, try by LID in Extra field
		var accounts []models.LinkedAccount
		if err := db.DB.Where(
			"protocol = ? AND provider_instance_id = ? AND extra != ''",
			"whatsapp", instanceID,
		).Find(&accounts).Error; err == nil {
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
		// If still no account found, try by LID as UserID
		if account.ID == 0 {
			if err := db.DB.Where(
				"protocol = ? AND provider_instance_id = ? AND user_id = ?",
				"whatsapp", instanceID, lid,
			).First(&account).Error; err == nil {
				// Found account by LID! Update to use phone number as canonical ID
				fmt.Printf("WhatsApp: Found account by LID %s, updating to canonical ID %s\n", lid, phoneNumber)

				// We need to update the UserID in the database directly
				// because saving the struct with a new UserID might create a new record
				// or fail if ID is primary key (it's not here, ID is int PK).
				// But we want to keep the same DB record.

				// Update user_id to phone number
				account.UserID = phoneNumber
				if err := db.DB.Save(&account).Error; err != nil {
					fmt.Printf("WhatsApp: Failed to update canonical ID for %s: %v\n", lid, err)
				}
				canonicalID = phoneNumber
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
