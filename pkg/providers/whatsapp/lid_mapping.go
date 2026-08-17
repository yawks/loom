package whatsapp

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"time"
)

func (w *WhatsAppProvider) saveLIDMapping(lid, jid string) error {
	if lid == "" || jid == "" {
		return fmt.Errorf("lid and jid cannot be empty")
	}

	// Update cache first (fast)
	w.lidToJIDMu.Lock()
	w.lidToJIDMap[lid] = jid
	w.lidToJIDMu.Unlock()

	// Then persist to database
	if db.DB != nil {
		mapping := models.LIDMapping{
			LID:      lid,
			JID:      jid,
			Protocol: "whatsapp",
			LastSeen: time.Now(),
		}

		// Upsert: update if exists, create if not
		err := db.DB.Where("lid = ?", lid).Assign(models.LIDMapping{
			JID:      jid,
			Protocol: "whatsapp",
			LastSeen: time.Now(),
		}).FirstOrCreate(&mapping).Error

		if err != nil {
			return fmt.Errorf("failed to save LID mapping to database: %w", err)
		}

		// Reactions may have arrived before WhatsApp exposed this mapping. Rewrite
		// only reactions belonging to this provider instance so their author can be
		// resolved immediately to a contact name or, at minimum, a phone number.
		if err := w.canonicalizePersistedReactionAuthor(lid, jid); err != nil {
			return fmt.Errorf("failed to canonicalize reaction authors for %s: %w", lid, err)
		}
	}

	return nil
}

func (w *WhatsAppProvider) loadLIDMappingsFromDB() error {
	if db.DB == nil {
		return fmt.Errorf("database not available")
	}

	var mappings []models.LIDMapping
	if err := db.DB.Where("protocol = ?", "whatsapp").Find(&mappings).Error; err != nil {
		return fmt.Errorf("failed to load LID mappings from database: %w", err)
	}

	w.lidToJIDMu.Lock()

	for _, mapping := range mappings {
		w.lidToJIDMap[mapping.LID] = mapping.JID
	}
	w.lidToJIDMu.Unlock()

	// Also repair reactions stored before their LID mapping became available.
	for _, mapping := range mappings {
		if err := w.canonicalizePersistedReactionAuthor(mapping.LID, mapping.JID); err != nil {
			return err
		}
	}

	fmt.Printf("WhatsApp: Loaded %d LID->JID mappings from database into cache\n", len(mappings))
	return nil
}

func (w *WhatsAppProvider) canonicalizePersistedReactionAuthor(lid, jid string) error {
	if db.DB == nil || lid == "" || jid == "" || lid == jid {
		return nil
	}
	instancePrefix := w.getInstanceId() + "::%"
	return db.DB.Model(&models.Reaction{}).
		Where("user_id = ? AND message_id IN (SELECT id FROM messages WHERE protocol_conv_id LIKE ?)", lid, instancePrefix).
		Update("user_id", jid).Error
}

func (w *WhatsAppProvider) buildLIDMappingsFromConversations() {
	fmt.Println("WhatsApp: Building LID->JID mappings from existing messages in database...")

	// Load existing mappings from database first (fast, indexed query)
	if err := w.loadLIDMappingsFromDB(); err != nil {
		fmt.Printf("WhatsApp: Warning - Failed to load LID mappings from database: %v\n", err)
	}

	// Also load mappings from LinkedAccount.Extra
	w.loadLIDMappingsFromLinkedAccounts()

	// Log current state
	w.lidToJIDMu.RLock()
	existingCount := len(w.lidToJIDMap)
	w.lidToJIDMu.RUnlock()

	fmt.Printf("WhatsApp: Loaded %d existing LID mappings from database and LinkedAccounts\n", existingCount)
	fmt.Println("WhatsApp: LID mappings will be created automatically as new messages arrive")
	fmt.Println("WhatsApp: Typing indicators are ready!")

	// Note: We no longer scan ALL historical messages as this can be very slow
	// Instead, mappings will be created automatically when:
	// 1. New messages arrive (Chat.Server == "lid")
	// 2. Typing events arrive (fallback resolution + save)
	// This approach is much faster and doesn't block the sync
}

// loadLIDMappingsFromLinkedAccounts loads LID mappings from LinkedAccount.Extra field.
func (w *WhatsAppProvider) loadLIDMappingsFromLinkedAccounts() {
	if db.DB == nil {
		return
	}

	var accounts []models.LinkedAccount
	if err := db.DB.Where(
		"protocol = ? AND provider_instance_id = ? AND extra != ''",
		"whatsapp", w.getInstanceId(),
	).Find(&accounts).Error; err != nil {
		fmt.Printf("WhatsApp: Failed to load LinkedAccounts with extra data: %v\n", err)
		return
	}

	w.lidToJIDMu.Lock()
	defer w.lidToJIDMu.Unlock()

	loaded := 0
	for _, acc := range accounts {
		if acc.Extra == "" {
			continue
		}

		var extraData WhatsAppExtraData
		if err := json.Unmarshal([]byte(acc.Extra), &extraData); err != nil {
			continue
		}

		// Load LID -> PhoneNumber mapping
		if extraData.LID != "" && extraData.PhoneNumber != "" {
			w.lidToJIDMap[extraData.LID] = extraData.PhoneNumber
			loaded++
		}

		// Load aliases
		for alias, canonicalID := range extraData.Aliases {
			if alias != "" && canonicalID != "" {
				w.lidToJIDMap[alias] = canonicalID
				loaded++
			}
		}
	}

	if loaded > 0 {
		fmt.Printf("WhatsApp: Loaded %d LID mappings from LinkedAccount.Extra\n", loaded)
	}
}
