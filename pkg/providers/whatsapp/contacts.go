package whatsapp

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"gorm.io/gorm"
)

func (w *WhatsAppProvider) lookupDisplayName(jid types.JID, fallback string) string {
	// For own user, use the device's push name (WhatsApp profile display name)
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil && !jid.IsEmpty() {
		if jid.User == w.client.Store.ID.User {
			if w.client.Store.PushName != "" && !isPhoneNumber(w.client.Store.PushName) {
				return w.client.Store.PushName
			}
		}
	}

	// First, try to get contact info from whatsmeow's Contact store
	// This is the recommended approach - let whatsmeow handle LID/PN mapping
	if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil && !jid.IsEmpty() {
		contact, err := w.client.Store.Contacts.GetContact(w.ctx, jid)
		if err == nil && contact.Found {
			// Use contact names in order of preference
			// Only return names that are actual names, not phone numbers
			if contact.FullName != "" && !isPhoneNumber(contact.FullName) {
				return contact.FullName
			}
			if contact.PushName != "" && !isPhoneNumber(contact.PushName) {
				return contact.PushName
			}
			if contact.FirstName != "" && !isPhoneNumber(contact.FirstName) {
				return contact.FirstName
			}
			if contact.BusinessName != "" && !isPhoneNumber(contact.BusinessName) {
				return contact.BusinessName
			}
		}
	}

	// For LIDs, try to resolve to phone number using whatsmeow's LID map or Loom's LID resolver
	if jid.Server == "lid" {
		var phoneJID types.JID
		if w.client != nil && w.client.Store != nil && w.client.Store.LIDs != nil {
			if pj, err := w.client.Store.LIDs.GetPNForLID(w.ctx, jid.ToNonAD()); err == nil && !pj.IsEmpty() {
				phoneJID = pj.ToNonAD()
			}
		}
		if phoneJID.IsEmpty() {
			if resolvedStr, err := w.resolveContactID(jid.String()); err == nil && resolvedStr != "" && resolvedStr != jid.String() {
				if parsedPJ, err := types.ParseJID(resolvedStr); err == nil {
					phoneJID = parsedPJ.ToNonAD()
				}
			}
		}
		if !phoneJID.IsEmpty() {
			fmt.Printf("WhatsApp: [LID-MAP] Resolved LID %s to phone %s\n", jid.User, phoneJID.User)
			// Now try to get contact info for the phone number
			if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil {
				if contact, err := w.client.Store.Contacts.GetContact(w.ctx, phoneJID); err == nil && contact.Found {
					if contact.FullName != "" && !isPhoneNumber(contact.FullName) {
						fmt.Printf("WhatsApp: [LID-MAP] Using FullName '%s' for LID %s\n", contact.FullName, jid.User)
						return contact.FullName
					}
					if contact.PushName != "" && !isPhoneNumber(contact.PushName) {
						fmt.Printf("WhatsApp: [LID-MAP] Using PushName '%s' for LID %s\n", contact.PushName, jid.User)
						return contact.PushName
					}
					if contact.FirstName != "" && !isPhoneNumber(contact.FirstName) {
						fmt.Printf("WhatsApp: [LID-MAP] Using FirstName '%s' for LID %s\n", contact.FirstName, jid.User)
						return contact.FirstName
					}
				}
			}
			// Check database LinkedAccount for phone JID
			if db.DB != nil {
				var account models.LinkedAccount
				if err := db.DB.Where("provider_instance_id = ? AND user_id = ?", w.getInstanceId(), phoneJID.String()).First(&account).Error; err == nil {
					if account.Username != "" && !isPhoneNumber(account.Username) && account.Username != account.UserID {
						fmt.Printf("WhatsApp: [LID-MAP] Using LinkedAccount '%s' for LID %s\n", account.Username, jid.User)
						return account.Username
					}
				}
			}
			// If no name, at least format the phone number nicely
			fmt.Printf("WhatsApp: [LID-MAP] No name found, formatting phone %s for LID %s\n", phoneJID.User, jid.User)
			return formatPhoneNumber(phoneJID.User)
		}
	}

	// Check group name for group chats
	if jid.Server == types.GroupServer {
		w.mu.RLock()
		if name, ok := w.knownGroups[jid.String()]; ok && name != "" && !isPhoneNumber(name) {
			w.mu.RUnlock()
			return name
		}
		w.mu.RUnlock()
	}

	// Use fallback if provided and it's not just a phone number
	if fallback != "" && fallback != jid.String() && !isPhoneNumber(fallback) {
		return fallback
	}

	// Last resort: return the JID user part
	// For phone numbers, format them nicely
	if !jid.IsEmpty() {
		if jid.Server == types.DefaultUserServer {
			return formatPhoneNumber(jid.User)
		}
		// For LIDs with no mapping, return raw (no formatting to avoid "14 90 44...")
		return jid.User
	}

	return ""
}

// normalizeChatJID resolves a LID JID to its canonical phone number JID when possible.
// WhatsApp history sync sometimes reports conversations using LID-based JIDs
// (e.g. "267859930403006@lid") instead of phone number JIDs ("33617590388@s.whatsapp.net").
// This causes the same DM to appear twice if both forms are present.
func (w *WhatsAppProvider) normalizeChatJID(jid types.JID) types.JID {
	if jid.Server != "lid" {
		return jid
	}
	if w.client == nil || w.client.Store == nil || w.client.Store.LIDs == nil {
		return jid
	}
	phoneJID, err := w.client.Store.LIDs.GetPNForLID(w.ctx, jid)
	if err == nil && !phoneJID.IsEmpty() {
		return phoneJID
	}
	return jid
}

func (w *WhatsAppProvider) lookupSenderName(jid types.JID) string {
	return w.lookupDisplayName(jid, "")
}

// LookupDisplayName is a public method to lookup display name for a JID
// This is used by app.go to resolve participant names (including LIDs)
func (w *WhatsAppProvider) LookupDisplayName(jid types.JID, fallback string) string {
	return w.lookupDisplayName(jid, fallback)
}

// LookupDisplayNameFromString parses a userID string and returns the display name
// This is used by app.go to resolve participant names (including LIDs)
func (w *WhatsAppProvider) LookupDisplayNameFromString(userID string) string {
	jid, err := types.ParseJID(userID)
	if err != nil || jid.IsEmpty() {
		return ""
	}
	return w.lookupDisplayName(jid, "")
}

// lookupContactInfo returns both display name and avatar URL for a contact
// This is a centralized function that handles LID resolution, phone formatting, and avatar retrieval
func (w *WhatsAppProvider) lookupContactInfo(jid types.JID, fallbackName string) (displayName string, avatarURL string) {
	// Get display name using centralized function
	displayName = w.lookupDisplayName(jid, fallbackName)

	// Get avatar URL using centralized function (may return empty if not available or failed previously)
	// Note: This may be slow for many contacts, so avatars are typically loaded asynchronously
	// But we can try to get from cache first
	w.mu.RLock()
	if cached, exists := w.conversations[jid.String()]; exists && cached.AvatarURL != "" {
		avatarURL = cached.AvatarURL
		w.mu.RUnlock()
		return displayName, avatarURL
	}
	w.mu.RUnlock()

	// If not in cache, try to get avatar (but this is slow, so usually done asynchronously)
	// For now, return empty - avatars will be loaded via loadAvatarsAsync
	return displayName, ""
}

// GetContactName retrieves the display name for a contact ID.
// This implements the Provider interface method.
// Uses the centralized lookupDisplayName function which handles LID resolution, phone formatting, etc.
func (w *WhatsAppProvider) GetContactName(contactID string) (string, error) {
	if contactID == "" {
		return "", fmt.Errorf("contact ID is empty")
	}

	// Parse the contact ID as a JID
	jid, err := types.ParseJID(contactID)
	if err != nil {
		return "", fmt.Errorf("invalid contact ID: %w", err)
	}

	if jid.IsEmpty() {
		return "", fmt.Errorf("empty JID")
	}

	// Use the centralized lookupDisplayName function
	// This handles LID resolution, phone formatting, group names, etc.
	name := w.lookupDisplayName(jid, "")
	if name != "" && name != contactID {
		return name, nil
	}

	// If lookupDisplayName returned empty or the same as contactID, return error
	return "", fmt.Errorf("no contact name found for %s", contactID)
}

func (w *WhatsAppProvider) lookupSenderNameInGroup(senderJID types.JID, groupJID types.JID) string {
	// First, try to resolve LID to phone number if needed
	// This uses the generic resolution mechanism that handles LID -> phone number conversion
	resolvedID, err := w.resolveContactIDForGroup(senderJID.String(), groupJID)
	if err != nil {
		// If resolution failed, use the original JID with generic lookupDisplayName
		// This will still try to get the name from various sources
		return w.lookupDisplayName(senderJID, "")
	}

	// Parse resolved ID and use generic lookupDisplayName
	// This ensures we use the same mechanism as individual chats
	if resolvedJID, err := types.ParseJID(resolvedID); err == nil {
		return w.lookupDisplayName(resolvedJID, "")
	}

	// Fallback to generic lookup with original JID
	return w.lookupDisplayName(senderJID, "")
}

func (w *WhatsAppProvider) GetContacts() ([]models.LinkedAccount, error) {
	verboseLogf("WhatsApp: GetContacts called\n")
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Check if client is connected (Store.ID is set after successful login)
	if w.client.Store == nil || w.client.Store.ID == nil {
		fmt.Printf("WhatsApp: GetContacts - client not connected, returning empty\n")
		return []models.LinkedAccount{}, nil
	}
	verboseLogf("WhatsApp: GetContacts - starting to load conversations\n")

	// Log instance ID for debugging
	w.mu.RLock()
	instanceID := ""
	if w.config != nil {
		if id, ok := w.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	w.mu.RUnlock()
	verboseLogf("WhatsApp: GetContacts - instance ID: %s\n", instanceID)

	// Start with cached conversations discovered via history sync
	// Make a copy to avoid holding the lock while processing fallback
	w.mu.RLock()
	cachedCount := len(w.conversations)
	cachedConversations := make([]models.LinkedAccount, 0, cachedCount)
	for _, conv := range w.conversations {
		// Make a copy to ensure we have the latest avatar URL
		cachedConversations = append(cachedConversations, conv)
	}
	w.mu.RUnlock()

	linkedAccounts := make([]models.LinkedAccount, 0, cachedCount+32)
	seen := make(map[string]struct{}, cachedCount)
	for _, conv := range cachedConversations {
		linkedAccounts = append(linkedAccounts, conv)
		seen[conv.UserID] = struct{}{}
	}

	// Fall back to the contact store and joined groups
	// Note: getContactsFallback may need to write to w.knownGroups, so we don't hold the lock
	verboseLogf("WhatsApp: GetContacts - calling getContactsFallback\n")
	fallbackAccounts, err := w.getContactsFallback()
	if err != nil {
		fmt.Printf("WhatsApp: GetContacts - getContactsFallback error: %v\n", err)
		return nil, err
	}
	verboseLogf("WhatsApp: GetContacts - getContactsFallback returned %d accounts\n", len(fallbackAccounts))

	// Get all avatars from cache in one pass to minimize lock contention
	w.mu.RLock()
	avatarCache := make(map[string]string, len(w.conversations))
	for userID, conv := range w.conversations {
		if conv.AvatarURL != "" {
			avatarCache[userID] = conv.AvatarURL
		}
	}
	w.mu.RUnlock()

	// Merge fallback accounts with cached conversations
	for _, acc := range fallbackAccounts {
		if _, exists := seen[acc.UserID]; exists {
			// If already in cache, check if cache has avatar and fallback doesn't
			// Update the entry in linkedAccounts with avatar from cache if available
			if avatarURL, hasAvatar := avatarCache[acc.UserID]; hasAvatar {
				for i := range linkedAccounts {
					if linkedAccounts[i].UserID == acc.UserID && linkedAccounts[i].AvatarURL == "" {
						linkedAccounts[i].AvatarURL = avatarURL
						break
					}
				}
			}
			continue
		}
		// Check if cache has avatar for this account
		if avatarURL, hasAvatar := avatarCache[acc.UserID]; hasAvatar {
			acc.AvatarURL = avatarURL
		}
		linkedAccounts = append(linkedAccounts, acc)
		seen[acc.UserID] = struct{}{}
	}

	// Always filter out contacts without messages in the database
	// This ensures we only show conversations that have actual messages
	// We check the database directly to be accurate, even if we have cached history
	w.mu.RLock()
	hasCachedHistory := len(w.conversationMessages) > 0
	cachedHistoryCount := len(w.conversationMessages)
	w.mu.RUnlock()

	verboseLogf("WhatsApp: GetContacts - Before filtering: %d contacts, hasCachedHistory: %v, cachedHistoryCount: %d\n", len(linkedAccounts), hasCachedHistory, cachedHistoryCount)

	// First try to filter using cached history (faster)
	if hasCachedHistory {
		fmt.Printf("WhatsApp: GetContacts - Filtering contacts with cached history first\n")
		filtered := w.filterAccountsWithHistory(linkedAccounts)
		if len(filtered) > 0 {
			fmt.Printf("WhatsApp: GetContacts - filterAccountsWithHistory returned %d contacts\n", len(filtered))
			linkedAccounts = filtered
		}
	}

	// Always double-check with database to ensure accuracy
	// This catches cases where cache might be incomplete or contacts were added without messages
	verboseLogf("WhatsApp: GetContacts - Verifying contacts have messages in database\n")
	// TEMPORARILY DISABLED: Too aggressive after DB reset
	// linkedAccounts = w.filterAccountsWithMessagesInDB(linkedAccounts)
	verboseLogf("WhatsApp: GetContacts - After database verification: %d contacts\n", len(linkedAccounts))

	// Final pass: ensure all avatars from cache are included
	// This ensures that avatars loaded asynchronously are visible
	for i := range linkedAccounts {
		if linkedAccounts[i].AvatarURL == "" {
			if avatarURL, hasAvatar := avatarCache[linkedAccounts[i].UserID]; hasAvatar {
				linkedAccounts[i].AvatarURL = avatarURL
			}
		}
	}

	// Deduplicate LID vs phone-number conversations.
	// If both "267859930403006@lid" and "33617590388@s.whatsapp.net" are present for the
	// same contact (can happen with data already persisted before the normalization above),
	// keep only the phone-number version and drop the LID entry.
	if w.client != nil && w.client.Store != nil && w.client.Store.LIDs != nil {
		phoneIdx := make(map[string]int, len(linkedAccounts))
		for i, acc := range linkedAccounts {
			jid, err := types.ParseJID(acc.UserID)
			if err == nil && jid.Server == types.DefaultUserServer {
				phoneIdx[acc.UserID] = i
			}
		}
		toRemove := make(map[int]bool)
		for i, acc := range linkedAccounts {
			jid, err := types.ParseJID(acc.UserID)
			if err != nil || jid.Server != "lid" {
				continue
			}
			phoneJID, err := w.client.Store.LIDs.GetPNForLID(w.ctx, jid)
			if err != nil || phoneJID.IsEmpty() {
				continue
			}
			phoneStr := phoneJID.String()
			if _, exists := phoneIdx[phoneStr]; exists {
				// Phone-number version already present — drop the LID duplicate.
				toRemove[i] = true
				fmt.Printf("WhatsApp: Dedup — removing LID conversation %s (canonical: %s)\n", acc.UserID, phoneStr)
			} else {
				// Promote the LID entry to use the phone-number ID.
				linkedAccounts[i].UserID = phoneStr
				phoneIdx[phoneStr] = i
				fmt.Printf("WhatsApp: Dedup — promoted LID conversation %s → %s\n", acc.UserID, phoneStr)
			}
		}
		if len(toRemove) > 0 {
			filtered := linkedAccounts[:0]
			for i, acc := range linkedAccounts {
				if !toRemove[i] {
					filtered = append(filtered, acc)
				}
			}
			linkedAccounts = filtered
		}
	}

	fmt.Printf("WhatsApp: GetContacts returning %d conversations (%d cached + %d fallback)\n", len(linkedAccounts), cachedCount, len(fallbackAccounts))

	// Save contacts to database and keep the in-memory store in sync.
	if db.DB != nil && instanceID != "" {
		fmt.Printf("WhatsApp: Saving %d contacts to database\n", len(linkedAccounts))

		// Load existing accounts and their MetaContacts from the in-memory store — no DB queries.
		existingByUserID := make(map[string]models.LinkedAccount)
		for _, la := range db.ContactStore.FindByProvider(instanceID) {
			existingByUserID[la.UserID] = la
		}

		now := time.Now()
		var laToUpdate []models.LinkedAccount
		var metaToUpdate []models.MetaContact
		var newContacts []models.LinkedAccount

		for i := range linkedAccounts {
			linkedAccounts[i].ProviderInstanceID = instanceID
			existing, found := existingByUserID[linkedAccounts[i].UserID]
			if !found {
				linkedAccounts[i].CreatedAt = now
				linkedAccounts[i].UpdatedAt = now
				newContacts = append(newContacts, linkedAccounts[i])
				continue
			}

			changed := false
			if existing.Username != linkedAccounts[i].Username {
				existing.Username = linkedAccounts[i].Username
				changed = true
			}
			if linkedAccounts[i].AvatarURL != "" && existing.AvatarURL != linkedAccounts[i].AvatarURL {
				existing.AvatarURL = linkedAccounts[i].AvatarURL
				changed = true
			}
			if existing.Status != linkedAccounts[i].Status {
				existing.Status = linkedAccounts[i].Status
				changed = true
			}
			if existing.IsGroup != linkedAccounts[i].IsGroup {
				existing.IsGroup = linkedAccounts[i].IsGroup
				changed = true
			}

			// Check MetaContact DisplayName using the in-memory store.
			if existing.MetaContactID > 0 {
				if meta, ok := db.ContactStore.FindMetaContact(existing.MetaContactID); ok {
					if meta.DisplayName != linkedAccounts[i].Username && linkedAccounts[i].Username != "" {
						meta.DisplayName = linkedAccounts[i].Username
						meta.UpdatedAt = now
						metaToUpdate = append(metaToUpdate, meta)
						changed = true
					}
				}
			}

			if changed {
				existing.UpdatedAt = now
				laToUpdate = append(laToUpdate, existing)
			}
		}

		// Batch all updates in a single transaction, then sync the store.
		updateCount := len(laToUpdate) + len(metaToUpdate)
		if updateCount > 0 {
			if err := db.DB.Transaction(func(tx *gorm.DB) error {
				for i := range metaToUpdate {
					if err := tx.Save(&metaToUpdate[i]).Error; err != nil {
						return err
					}
				}
				for i := range laToUpdate {
					if err := tx.Save(&laToUpdate[i]).Error; err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				fmt.Printf("WhatsApp: Failed to batch-update contacts: %v\n", err)
			} else {
				for _, mc := range metaToUpdate {
					db.ContactStore.UpsertMetaContact(mc)
				}
				for _, la := range laToUpdate {
					db.ContactStore.UpsertLinkedAccount(la)
				}
			}
		}

		// Create new contacts individually (rare after first sync).
		for i := range newContacts {
			mc := models.MetaContact{
				DisplayName: newContacts[i].Username,
				AvatarURL:   newContacts[i].AvatarURL,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := db.DB.Create(&mc).Error; err != nil {
				fmt.Printf("WhatsApp: Failed to create MetaContact for %s: %v\n", newContacts[i].Username, err)
				continue
			}
			db.ContactStore.UpsertMetaContact(mc)
			newContacts[i].MetaContactID = mc.ID
			newContacts[i].ID = 0
			if err := db.DB.Create(&newContacts[i]).Error; err != nil {
				fmt.Printf("WhatsApp: Failed to create LinkedAccount for %s: %v\n", newContacts[i].UserID, err)
				continue
			}
			db.ContactStore.UpsertLinkedAccount(newContacts[i])
		}

		fmt.Printf("WhatsApp: Contacts saved (%d updated, %d created)\n", updateCount, len(newContacts))
	} else {
		fmt.Printf("WhatsApp: WARNING - Skipping contact save (DB=%v, InstanceID=%s)\n", db.DB != nil, instanceID)
	}

	// Load avatars asynchronously in background (non-blocking)
	// Trigger initial bulk avatar load if no avatars are cached
	// We count how many non-empty avatars we have in the current contact list
	hasAvatars := false
	for _, acc := range linkedAccounts {
		if acc.AvatarURL != "" {
			hasAvatars = true
			break
		}
	}

	if !hasAvatars || w.lastSyncTimestamp == nil {
		fmt.Printf("WhatsApp: Triggering bulk avatar sync (hasAvatars=%v, firstSync=%v)\n", hasAvatars, w.lastSyncTimestamp == nil)
		go func() {
			// Only load top 50 avatars during initial sync
			w.loadAvatarsAsync(linkedAccounts, 50)
		}()
	}

	return linkedAccounts, nil
}

func (w *WhatsAppProvider) filterAccountsWithHistory(accounts []models.LinkedAccount) []models.LinkedAccount {
	w.mu.RLock()
	historyCount := len(w.conversationMessages)
	w.mu.RUnlock()

	if historyCount == 0 {
		return nil
	}

	filtered := make([]models.LinkedAccount, 0, len(accounts))
	for _, acc := range accounts {
		// Joined groups are valid conversations even before WhatsApp transfers a
		// first message for them. Filtering them by local history would make a new
		// invitation disappear again immediately after JoinedGroup cached it.
		if acc.IsGroup || w.hasConversationHistory(acc.UserID) {
			filtered = append(filtered, acc)
		}
	}
	return filtered
}

func (w *WhatsAppProvider) getContactsFallback() ([]models.LinkedAccount, error) {
	verboseLogf("WhatsApp: getContactsFallback called\n")
	// Check if store is available
	if w.client == nil || w.client.Store == nil || w.client.Store.Contacts == nil {
		fmt.Printf("WhatsApp: Store not available yet (client=%v, store=%v, contacts=%v)\n",
			w.client != nil,
			w.client != nil && w.client.Store != nil,
			w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil)
		return []models.LinkedAccount{}, nil
	}

	// Get contacts - these represent people you can chat with
	// Note: GetAllContacts only returns contacts in the address book, not all conversations
	contacts, err := w.client.Store.Contacts.GetAllContacts(w.ctx)
	if err != nil {
		fmt.Printf("WhatsApp: GetAllContacts failed: %v\n", err)
		contacts = make(map[types.JID]types.ContactInfo)
	} else {
		verboseLogf("WhatsApp: GetAllContacts returned %d contacts\n", len(contacts))
	}

	// Get instance ID for this provider (once, before the loop)
	w.mu.RLock()
	instanceID := ""
	if w.config != nil {
		if id, ok := w.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	w.mu.RUnlock()

	linkedAccounts := make([]models.LinkedAccount, 0, len(contacts))
	now := time.Now()

	// Add individual contacts
	for jid, contact := range contacts {
		// Skip groups (they have @g.us server)
		if jid.Server == types.GroupServer || jid.Server == types.BroadcastServer {
			continue
		}

		// Use centralized function to get display name (handles LID resolution, phone formatting, etc.)
		// Use contact names as fallback
		fallbackName := contact.FullName
		if fallbackName == "" {
			fallbackName = contact.FirstName
		}
		if fallbackName == "" {
			fallbackName = contact.PushName
		}
		displayName := w.lookupDisplayName(jid, fallbackName)

		// Don't fetch profile pictures synchronously - it blocks and causes rate limiting
		// Avatars will be loaded lazily when needed or in background
		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:           "whatsapp",
			ProviderInstanceID: instanceID,
			UserID:             jid.String(),
			Username:           displayName,
			IsGroup:            jid.Server == types.GroupServer,
			AvatarURL:          "", // Will be loaded asynchronously if needed
			Status:             "offline",
			CreatedAt:          now,
			UpdatedAt:          now,
		})
	}

	// Add known groups (tracked from messages)
	// Copy the map first to avoid holding the lock during lookupDisplayName
	w.mu.RLock()
	knownGroupsCopy := make(map[string]string, len(w.knownGroups))
	for k, v := range w.knownGroups {
		knownGroupsCopy[k] = v
	}
	w.mu.RUnlock()

	// Now iterate over the copy without holding the lock
	for groupJID, groupName := range knownGroupsCopy {
		displayName := groupName
		if jid, err := types.ParseJID(groupJID); err == nil {
			displayName = w.lookupDisplayName(jid, groupName)
		}
		// Groups don't have profile pictures, so avatarURL is empty
		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:           "whatsapp",
			ProviderInstanceID: instanceID,
			UserID:             groupJID,
			Username:           displayName,
			IsGroup:            true,
			AvatarURL:          "",
			Status:             "offline",
			CreatedAt:          now,
			UpdatedAt:          now,
		})
	}

	// Try to get groups using GetJoinedGroups if available
	// Only fetch if cache is empty or older than 1 hour to avoid rate limiting
	shouldFetchGroups := false
	w.mu.RLock()
	if w.groupsCacheTimestamp == nil {
		shouldFetchGroups = true
	} else {
		// Refresh cache if older than 1 hour
		if time.Since(*w.groupsCacheTimestamp) > 1*time.Hour {
			shouldFetchGroups = true
		}
	}
	w.mu.RUnlock()

	if shouldFetchGroups && w.client != nil && w.client.Store.ID != nil {
		fmt.Printf("WhatsApp: Attempting to fetch groups via GetJoinedGroups...\n")
		groups, err := w.client.GetJoinedGroups(w.ctx)
		if err == nil && len(groups) > 0 {
			fmt.Printf("WhatsApp: Found %d groups via GetJoinedGroups\n", len(groups))
			groupsAdded := 0
			for _, group := range groups {
				if group == nil {
					continue
				}
				groupJID := group.JID.String()
				alreadyAdded := false
				for _, acc := range linkedAccounts {
					if acc.UserID == groupJID {
						alreadyAdded = true
						fmt.Printf("WhatsApp: Group %s already in linkedAccounts, skipping\n", groupJID)
						break
					}
				}
				if alreadyAdded {
					continue
				}
				displayName := group.Name
				if displayName == "" {
					displayName = groupJID
				}
				displayName = w.lookupDisplayName(group.JID, displayName)
				// Groups don't have profile pictures, so avatarURL is empty
				linkedAccounts = append(linkedAccounts, models.LinkedAccount{
					Protocol:  "whatsapp",
					UserID:    groupJID,
					Username:  displayName,
					IsGroup:   true,
					AvatarURL: "",
					Status:    "offline",
					CreatedAt: now,
					UpdatedAt: now,
				})
				// Store in known groups
				w.mu.Lock()
				w.knownGroups[groupJID] = displayName
				w.mu.Unlock()
				fmt.Printf("WhatsApp: Added group %s (%s)\n", displayName, groupJID)
				groupsAdded++
			}
			fmt.Printf("WhatsApp: Added %d new groups from GetJoinedGroups (total linkedAccounts: %d)\n", groupsAdded, len(linkedAccounts))

			// Cache the groups and timestamp
			// Build cache outside the lock first
			groupsToCache := make([]models.LinkedAccount, 0, groupsAdded)
			cacheNow := time.Now()
			for _, acc := range linkedAccounts {
				if jid, err := types.ParseJID(acc.UserID); err == nil && jid.Server == types.GroupServer {
					groupsToCache = append(groupsToCache, acc)
				}
			}
			// Now update the cache with the lock
			w.mu.Lock()
			w.groupsCache = groupsToCache
			w.groupsCacheTimestamp = &cacheNow
			w.mu.Unlock()
		} else if err != nil {
			fmt.Printf("WhatsApp: Could not get groups via GetJoinedGroups: %v\n", err)
		} else {
			fmt.Printf("WhatsApp: No groups found via GetJoinedGroups\n")
		}
	} else if !shouldFetchGroups {
		// Use cached groups - copy first to avoid holding lock
		w.mu.RLock()
		cachedGroupsCopy := make([]models.LinkedAccount, len(w.groupsCache))
		copy(cachedGroupsCopy, w.groupsCache)
		cacheAge := time.Since(*w.groupsCacheTimestamp)
		w.mu.RUnlock()

		if len(cachedGroupsCopy) > 0 {
			fmt.Printf("WhatsApp: Using cached groups (cache age: %v)\n", cacheAge)
			for _, cachedGroup := range cachedGroupsCopy {
				alreadyAdded := false
				for _, acc := range linkedAccounts {
					if acc.UserID == cachedGroup.UserID {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					linkedAccounts = append(linkedAccounts, cachedGroup)
				}
			}
		}
	} else {
		fmt.Printf("WhatsApp: Cannot fetch groups - client=%v, Store.ID=%v\n", w.client != nil, w.client != nil && w.client.Store != nil && w.client.Store.ID != nil)
	}

	fmt.Printf("WhatsApp: Retrieved %d contacts/conversations (including groups)\n", len(linkedAccounts))
	// TODO: Add sorting by message count once messages table is populated
	// For now, just sort by last activity
	sort.Slice(linkedAccounts, func(i, j int) bool {
		return linkedAccounts[i].UpdatedAt.After(linkedAccounts[j].UpdatedAt)
	})

	return linkedAccounts, nil
}

// cacheJoinedGroup updates every directory cache used by GetContacts. JoinedGroup
// can arrive while the GetJoinedGroups cache is still considered fresh, so merely
// emitting a frontend refresh is not enough to make the new group visible.
func (w *WhatsAppProvider) cacheJoinedGroup(jid types.JID, name string) {
	if jid.IsEmpty() || jid.Server != types.GroupServer {
		return
	}

	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = jid.String()
	}
	instanceID := w.getInstanceId()
	now := time.Now()
	account := models.LinkedAccount{
		Protocol:           "whatsapp",
		ProviderInstanceID: instanceID,
		UserID:             jid.String(),
		Username:           displayName,
		IsGroup:            true,
		Status:             "offline",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	w.mu.Lock()
	w.knownGroups[account.UserID] = displayName
	w.conversations[account.UserID] = account
	found := false
	for index := range w.groupsCache {
		if w.groupsCache[index].UserID == account.UserID {
			w.groupsCache[index] = account
			found = true
			break
		}
	}
	if !found {
		w.groupsCache = append(w.groupsCache, account)
	}
	w.groupsCacheTimestamp = &now
	w.mu.Unlock()
}

// subscribeToContactPresence subscribes to presence updates for all DM contacts
// This allows us to receive online/offline status updates for all contacts
// Note: WhatsApp requires explicit subscription to receive presence events
func (w *WhatsAppProvider) subscribeToContactPresence() {
	if w.client == nil || w.client.Store == nil || w.client.Store.ID == nil {
		fmt.Printf("WhatsApp: Cannot subscribe to presence - client not connected\n")
		return
	}

	// Wait a bit for the connection to stabilize
	time.Sleep(2 * time.Second)

	verboseLogf("WhatsApp: Starting presence subscription for DM contacts...\n")

	// Get all contacts from the store
	if w.client.Store.Contacts == nil {
		fmt.Printf("WhatsApp: Contacts store not available, cannot subscribe to presence\n")
		return
	}

	contacts, err := w.client.Store.Contacts.GetAllContacts(w.ctx)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to get contacts for presence subscription: %v\n", err)
		return
	}

	// Also get conversations from our cache (they might not be in GetAllContacts)
	w.mu.RLock()
	cachedConversations := make([]string, 0, len(w.conversations))
	for userID := range w.conversations {
		cachedConversations = append(cachedConversations, userID)
	}
	w.mu.RUnlock()

	// Combine contacts from store and cached conversations
	seen := make(map[string]bool)
	dmContacts := make([]types.JID, 0)

	// Add contacts from store (only DM, not groups)
	for jid := range contacts {
		if jid.Server == types.DefaultUserServer {
			jidStr := jid.String()
			if !seen[jidStr] {
				dmContacts = append(dmContacts, jid)
				seen[jidStr] = true
			}
		}
	}

	// Add cached conversations (only DM, not groups)
	for _, userID := range cachedConversations {
		if !seen[userID] {
			jid, err := types.ParseJID(userID)
			if err == nil && jid.Server == types.DefaultUserServer {
				dmContacts = append(dmContacts, jid)
				seen[userID] = true
			}
		}
	}

	// Limit presence subscription to top 100 most recent contacts to avoid long startup delay and API spam
	// We'll subscribe to others on-demand when their conversation is opened.
	if len(dmContacts) > 100 {
		dmContacts = dmContacts[:100]
	}

	verboseLogf("WhatsApp: Found %d DM contacts to subscribe to presence\n", len(dmContacts))

	// Subscribe to presence for each contact with a small delay to avoid rate limiting
	successCount := 0
	for i, jid := range dmContacts {
		// Add delay between subscriptions to avoid overwhelming WhatsApp API
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		err := w.client.SubscribePresence(w.ctx, jid)
		if err != nil {
			fmt.Printf("WhatsApp: Failed to subscribe to presence for %s: %v\n", jid.String(), err)
		} else {
			successCount++
			if (i+1)%10 == 0 {
				verboseLogf("WhatsApp: Subscribed to presence for %d/%d contacts...\n", i+1, len(dmContacts))
			}
		}
	}

	fmt.Printf("WhatsApp: Successfully subscribed to presence for %d/%d DM contacts\n", successCount, len(dmContacts))
}
