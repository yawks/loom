package whatsapp

import (
	"Loom/pkg/models"
	"fmt"
	"sort"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func (w *WhatsAppProvider) lookupDisplayName(jid types.JID, fallback string) string {
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

	// For LIDs, try to resolve to phone number using whatsmeow's LID map
	if jid.Server == "lid" && w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil {
		// Try to get the phone number JID from whatsmeow's LID map
		phoneJID, err := w.client.Store.LIDs.GetPNForLID(w.ctx, jid)
		if err == nil && !phoneJID.IsEmpty() {
			fmt.Printf("WhatsApp: [LID-MAP] Resolved LID %s to phone %s\n", jid.User, phoneJID.User)
			// Now try to get contact info for the phone number
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
			// If no name, at least format the phone number nicely
			fmt.Printf("WhatsApp: [LID-MAP] No name found, formatting phone %s for LID %s\n", phoneJID.User, jid.User)
			return formatPhoneNumber(phoneJID.User)
		} else if err != nil {
			fmt.Printf("WhatsApp: [LID-MAP] GetContactByLID failed for %s: %v\n", jid.User, err)
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

func (w *WhatsAppProvider) lookupSenderName(jid types.JID) string {
	return w.lookupDisplayName(jid, "")
}

func (w *WhatsAppProvider) GetContactName(contactID string) (string, error) {
	fmt.Printf("WhatsApp: GetContactName called with ID: '%s'\n", contactID)
	// Parse the contact ID as a JID
	jid, err := types.ParseJID(contactID)
	if err != nil {
		fmt.Printf("WhatsApp: GetContactName(%s) - failed to parse JID: %v\n", contactID, err)
		return "", fmt.Errorf("invalid contact ID: %w", err)
	}

	if jid.IsEmpty() {
		fmt.Printf("WhatsApp: GetContactName(%s) - empty JID\n", contactID)
		return "", fmt.Errorf("empty JID")
	}

	// Try to get contact from store first
	if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil && !jid.IsEmpty() {
		if contact, err := w.client.Store.Contacts.GetContact(w.ctx, jid); err == nil && contact.Found {
			// Only return actual names, not phone numbers
			if contact.FullName != "" && !isPhoneNumber(contact.FullName) {
				fmt.Printf("WhatsApp: GetContactName(%s) = '%s' (FullName)\n", contactID, contact.FullName)
				return contact.FullName, nil
			}
			if contact.FirstName != "" && !isPhoneNumber(contact.FirstName) {
				fmt.Printf("WhatsApp: GetContactName(%s) = '%s' (FirstName)\n", contactID, contact.FirstName)
				return contact.FirstName, nil
			}
			if contact.PushName != "" && !isPhoneNumber(contact.PushName) {
				fmt.Printf("WhatsApp: GetContactName(%s) = '%s' (PushName)\n", contactID, contact.PushName)
				return contact.PushName, nil
			}
			if contact.BusinessName != "" && !isPhoneNumber(contact.BusinessName) {
				fmt.Printf("WhatsApp: GetContactName(%s) = '%s' (BusinessName)\n", contactID, contact.BusinessName)
				return contact.BusinessName, nil
			}
			fmt.Printf("WhatsApp: GetContactName(%s) - contact found but all names are phone numbers (FullName='%s', FirstName='%s', PushName='%s', BusinessName='%s')\n",
				contactID, contact.FullName, contact.FirstName, contact.PushName, contact.BusinessName)
		} else {
			found := false
			if err == nil {
				found = contact.Found
			}
			fmt.Printf("WhatsApp: GetContactName(%s) - contact not found in store (found=%v, err=%v)\n", contactID, found, err)
		}
	} else {
		fmt.Printf("WhatsApp: GetContactName(%s) - store not available (client=%v, store=%v)\n", contactID, w.client != nil, w.client != nil && w.client.Store != nil)
	}

	// Check known groups for group names
	if jid.Server == types.GroupServer {
		w.mu.RLock()
		if name, ok := w.knownGroups[jid.String()]; ok && name != "" && !isPhoneNumber(name) {
			w.mu.RUnlock()
			fmt.Printf("WhatsApp: GetContactName(%s) = '%s' (GroupName)\n", contactID, name)
			return name, nil
		}
		w.mu.RUnlock()
	}

	// No actual contact name found - return error instead of a formatted phone number
	fmt.Printf("WhatsApp: GetContactName(%s) - no actual name found, returning error\n", contactID)
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
	fmt.Printf("WhatsApp: GetContacts called\n")
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Check if client is connected (Store.ID is set after successful login)
	if w.client.Store == nil || w.client.Store.ID == nil {
		fmt.Printf("WhatsApp: GetContacts - client not connected, returning empty\n")
		return []models.LinkedAccount{}, nil
	}
	fmt.Printf("WhatsApp: GetContacts - starting to load conversations\n")

	// Log instance ID for debugging
	w.mu.RLock()
	instanceID := ""
	if w.config != nil {
		if id, ok := w.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	w.mu.RUnlock()
	fmt.Printf("WhatsApp: GetContacts - instance ID: %s\n", instanceID)

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
	fmt.Printf("WhatsApp: GetContacts - calling getContactsFallback\n")
	fallbackAccounts, err := w.getContactsFallback()
	if err != nil {
		fmt.Printf("WhatsApp: GetContacts - getContactsFallback error: %v\n", err)
		return nil, err
	}
	fmt.Printf("WhatsApp: GetContacts - getContactsFallback returned %d accounts\n", len(fallbackAccounts))

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

	fmt.Printf("WhatsApp: GetContacts - Before filtering: %d contacts, hasCachedHistory: %v, cachedHistoryCount: %d\n", len(linkedAccounts), hasCachedHistory, cachedHistoryCount)

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
	fmt.Printf("WhatsApp: GetContacts - Verifying contacts have messages in database\n")
	// TEMPORARILY DISABLED: Too aggressive after DB reset
	// linkedAccounts = w.filterAccountsWithMessagesInDB(linkedAccounts)
	fmt.Printf("WhatsApp: GetContacts - After database verification: %d contacts\n", len(linkedAccounts))

	// Final pass: ensure all avatars from cache are included
	// This ensures that avatars loaded asynchronously are visible
	for i := range linkedAccounts {
		if linkedAccounts[i].AvatarURL == "" {
			if avatarURL, hasAvatar := avatarCache[linkedAccounts[i].UserID]; hasAvatar {
				linkedAccounts[i].AvatarURL = avatarURL
			}
		}
	}

	fmt.Printf("WhatsApp: GetContacts returning %d conversations (%d cached + %d fallback)\n", len(linkedAccounts), cachedCount, len(fallbackAccounts))

	// Load avatars asynchronously in background (non-blocking)
	// Only load if we have accounts that need avatars
	// Use a separate goroutine to avoid blocking
	go func() {
		// Check if any avatars are already being loaded
		w.avatarLoadingMu.Lock()
		hasLoading := len(w.avatarLoading) > 0
		w.avatarLoadingMu.Unlock()

		// Only start loading if not already loading
		if !hasLoading {
			w.loadAvatarsAsync(linkedAccounts)
		}
	}()

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
		if w.hasConversationHistory(acc.UserID) {
			filtered = append(filtered, acc)
		}
	}
	return filtered
}

func (w *WhatsAppProvider) getContactsFallback() ([]models.LinkedAccount, error) {
	fmt.Printf("WhatsApp: getContactsFallback called\n")
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
		fmt.Printf("WhatsApp: GetAllContacts returned %d contacts\n", len(contacts))
	}

	linkedAccounts := make([]models.LinkedAccount, 0, len(contacts))
	now := time.Now()

	// Add individual contacts
	for jid, contact := range contacts {
		// Skip groups (they have @g.us server)
		if jid.Server == types.GroupServer || jid.Server == types.BroadcastServer {
			continue
		}

		displayName := contact.FullName
		if displayName == "" {
			displayName = contact.FirstName
		}
		if displayName == "" {
			displayName = contact.PushName
		}
		displayName = w.lookupDisplayName(jid, displayName)

		// Don't fetch profile pictures synchronously - it blocks and causes rate limiting
		// Avatars will be loaded lazily when needed or in background
		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    jid.String(),
			Username:  displayName,
			AvatarURL: "", // Will be loaded asynchronously if needed
			Status:    "offline",
			CreatedAt: now,
			UpdatedAt: now,
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
			Protocol:  "whatsapp",
			UserID:    groupJID,
			Username:  displayName,
			AvatarURL: "",
			Status:    "offline",
			CreatedAt: now,
			UpdatedAt: now,
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
