// Package slack provides the Slack provider implementation.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// getUserDisplayName extracts the display name from a Slack user
func getUserDisplayName(user *slack.User) string {
	if user.RealName != "" {
		return user.RealName
	}
	if user.Profile.DisplayName != "" {
		return user.Profile.DisplayName
	}
	return user.Name
}

// fetchUserDisplayName tries to fetch and return a user's display name by ID
func (p *SlackProvider) fetchUserDisplayName(userID string, userMap map[string]*slack.User) string {
	// Try to find in cache first
	if user, ok := userMap[userID]; ok {
		return getUserDisplayName(user)
	}

	// Fetch from API
	userInfo, err := p.client.GetUserInfo(userID)
	if err == nil && userInfo != nil {
		return getUserDisplayName(userInfo)
	}

	// Fallback to user ID
	return userID
}

// getParticipantNames retrieves the display names of participants in a conversation (excluding the current user)
func (p *SlackProvider) getParticipantNames(conversationID string, users []slack.User) ([]string, error) {
	// Get the current user ID
	authTest, err := p.client.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	currentUserID := authTest.UserID

	// Get the list of participants in the conversation with retry for rate limits
	var userIDs []string
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		userIDs, _, err = p.client.GetUsersInConversation(&slack.GetUsersInConversationParameters{
			ChannelID: conversationID,
		})
		if err == nil {
			break
		}

		// Check if it's a rate limit error
		errStr := err.Error()
		if strings.Contains(errStr, "rate limit") {
			// Extract retry-after time from error message if available
			waitTime := (i + 1) * 2 // Exponential backoff: 2s, 4s, 6s
			// TODO: Try to extract the number from "retry after Xs" if available
			_ = waitTime // Use waitTime when implementing retry-after parsing
			if i < maxRetries-1 {
				p.log("SlackProvider.getParticipantNames: Rate limit hit for %s, waiting %ds before retry %d/%d\n", conversationID, waitTime, i+1, maxRetries)
				time.Sleep(time.Duration(waitTime) * time.Second)
				continue
			}
		}
		return nil, fmt.Errorf("failed to get users in conversation: %w", err)
	}

	// Create a map for quick user lookup
	userMap := make(map[string]*slack.User)
	for i := range users {
		userMap[users[i].ID] = &users[i]
	}

	// Build the list of participant names (excluding current user)
	var names []string
	for _, userID := range userIDs {
		if userID != currentUserID {
			names = append(names, p.fetchUserDisplayName(userID, userMap))
		}
	}

	return names, nil
}

// ResolveUserNames resolves user names for a list of user IDs by fetching from Slack API if needed.
// Returns a map of userID -> displayName for successfully resolved users.
func (p *SlackProvider) ResolveUserNames(userIDs []string) map[string]string {
	result := make(map[string]string)
	if len(userIDs) == 0 {
		return result
	}

	p.mu.RLock()
	client := p.client
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	if client == nil || instanceID == "" {
		return result
	}

	// Fetch user info from Slack API for missing IDs
	for _, userID := range userIDs {
		// Skip if empty or already resolved
		if userID == "" {
			continue
		}

		// Try to get from cache first
		cachedName, _ := p.getUserNameFromCache(userID)
		if cachedName != "" && cachedName != userID {
			result[userID] = cachedName
			continue
		}

		// Fetch from Slack API
		user, err := client.GetUserInfo(userID)
		if err == nil && user != nil {
			// Determine display name
			displayName := user.RealName
			if displayName == "" && user.Profile.DisplayName != "" {
				displayName = user.Profile.DisplayName
			}
			if displayName == "" {
				displayName = user.Name
			}

			// Only use if it's a valid name (not empty and not the userID)
			if displayName != "" && displayName != userID {
				result[userID] = displayName

				// Persist to database
				avatarURL := ""
				if user.Profile.Image512 != "" {
					avatarURL = user.Profile.Image512
				} else if user.Profile.Image48 != "" {
					avatarURL = user.Profile.Image48
				}
				p.saveUserNameToCache(userID, displayName, avatarURL)
			}
		}
	}

	return result
}

// GetContactName retrieves the display name for a contact ID.
// This implements the Provider interface method.
func (p *SlackProvider) GetContactName(contactID string) (string, error) {
	if contactID == "" {
		return "", fmt.Errorf("contact ID is empty")
	}

	// Use ResolveUserNames which handles caching and API calls
	names := p.ResolveUserNames([]string{contactID})
	if name, ok := names[contactID]; ok && name != "" && name != contactID {
		return name, nil
	}

	// Fallback: try resolveUserName which has more fallback logic
	name := p.resolveUserName(contactID)
	if name != "" && name != contactID {
		return name, nil
	}

	return "", fmt.Errorf("no contact name found for %s", contactID)
}

// GetContacts returns the list of contacts for this protocol.
// This includes both individual users and group conversations (channels).
func (p *SlackProvider) GetContacts() ([]models.LinkedAccount, error) {
	fmt.Printf("SlackProvider.GetContacts: START\n")
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	fmt.Printf("SlackProvider.GetContacts: Getting user info\n")
	// Get current user info to filter out self
	authTest, err := client.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	currentUserID := authTest.UserID
	fmt.Printf("SlackProvider.GetContacts: Current user ID: %s\n", currentUserID)

	var contacts []models.LinkedAccount

	// Fetch all channels (public, private, MPIM, IM)
	params := &slack.GetConversationsParameters{
		Types:           []string{"public_channel", "private_channel", "mpim", "im"},
		Limit:           1000,
		ExcludeArchived: true,
	}

	fmt.Printf("SlackProvider.GetContacts: Fetching conversations...\n")
	channels, nextCursor, err := client.GetConversations(params)
	if err != nil {
		fmt.Printf("SlackProvider.GetContacts: ERROR fetching conversations: %v\n", err)
		return nil, err
	}
	fmt.Printf("SlackProvider.GetContacts: Fetched %d conversations (nextCursor=%s)\n", len(channels), nextCursor)

	// Handle pagination
	allChannels := channels
	if err == nil {
		for nextCursor != "" {
			moreChannels, cursor, err := p.client.GetConversations(&slack.GetConversationsParameters{
				Types:           []string{"public_channel", "private_channel", "mpim", "im"},
				Limit:           1000,
				Cursor:          nextCursor,
				ExcludeArchived: true,
			})
			if err != nil {
				p.log("SlackProvider.GetContacts: WARNING - failed to paginate channels: %v\n", err)
				break
			}
			allChannels = append(allChannels, moreChannels...)
			nextCursor = cursor
		}
	} else {
		p.log("SlackProvider.GetContacts: WARNING - failed to get channels: %v\n", err)
	}

	// Create a map of DM UserID -> LastRead for efficient lookup
	userLastRead := make(map[string]string)
	for _, ch := range allChannels {
		if ch.IsIM && ch.User != "" {
			userLastRead[ch.User] = ch.LastRead
		}
	}

	// 2. Optimization: Instead of client.GetUsers() which can return thousands of users,
	// we only care about users with active IM channels or users who were recently seen.
	// But to maintain the "Recent" and "Alphabetical" list correctly, we still need users.
	// NEW STRATEGY: Only fetch details for users who have active IM channels.

	activeUserIDs := make(map[string]bool)
	for _, ch := range allChannels {
		if ch.IsIM && ch.User != "" {
			activeUserIDs[ch.User] = true
		}
	}

	p.log("SlackProvider.GetContacts: Found %d active IM users to fetch details for\n", len(activeUserIDs))

	var users []slack.User

	// Optimization: If it's a huge workspace, client.GetUsers() is a CPU/memory killer.
	// To optimize CPU, we will skip detailed processing for users who aren't "Active" (no IM).

	fullUserList, err := p.client.GetUsers()
	if err != nil {
		p.log("SlackProvider.GetContacts: WARNING - failed to get users: %v\n", err)
	} else {
		users = fullUserList
		p.log("SlackProvider.GetContacts: Total workspace users: %d\n", len(fullUserList))
		for _, user := range fullUserList {
			if user.Deleted || user.IsBot {
				continue
			}

			// If not in active IMs and we have many users, skip processing status/metadata to save CPU
			if len(fullUserList) > 500 && !activeUserIDs[user.ID] {
				// Still include the user for alphabetical search, but with minimal metadata
				contacts = append(contacts, models.LinkedAccount{
					UserID:    user.ID,
					Username:  getUserDisplayName(&user),
					IsGroup:   false,
					AvatarURL: user.Profile.Image48,
					Status:    "offline",
					Protocol:  "slack",
				})
				continue
			}

			// Determine status based on presence and custom status
			status := "offline"
			extraData := make(map[string]interface{})

			statusText := user.Profile.StatusText
			statusEmoji := user.Profile.StatusEmoji

			if user.Presence == "active" {
				// User is active, but check for custom status (like meeting)
				statusLower := ""
				if statusText != "" {
					statusLower = strings.ToLower(statusText)
				}

				// Check for calendar emoji (meeting status)
				// Common calendar emojis: :calendar:, :spiral_calendar:, etc.
				isMeeting := strings.Contains(statusEmoji, "calendar") ||
					strings.Contains(statusLower, "meeting") ||
					strings.Contains(statusLower, "réunion") ||
					strings.Contains(statusLower, "en réunion")

				if isMeeting {
					status = "meeting"
				} else {
					status = "online"
				}
			} else if user.Presence == "away" {
				// Check if there's a custom status that might indicate a specific away type
				statusLower := ""
				if statusText != "" {
					statusLower = strings.ToLower(statusText)
				}

				// Map common status texts to specific status types
				if strings.Contains(statusLower, "holiday") || strings.Contains(statusLower, "vacation") || strings.Contains(statusLower, "vacances") {
					status = "holiday"
				} else if strings.Contains(statusLower, "busy") || strings.Contains(statusLower, "dnd") || strings.Contains(statusLower, "do not disturb") {
					status = "busy"
				} else if strings.Contains(statusLower, "meeting") || strings.Contains(statusLower, "réunion") || strings.Contains(statusEmoji, "calendar") {
					status = "meeting"
				} else {
					// Default away status
					status = "away"
				}
			}

			// Store status emoji and text in Extra field for potential future use
			if statusEmoji != "" {
				extraData["statusEmoji"] = statusEmoji
			}
			if statusText != "" {
				extraData["statusText"] = statusText
			}

			// Add LastRead if available from IM channel check
			if lastRead, ok := userLastRead[user.ID]; ok && lastRead != "" {
				extraData["last_read"] = lastRead
			}

			// Use RealName if available, fallback to DisplayName, then Name
			displayName := user.RealName
			if displayName == "" && user.Profile.DisplayName != "" {
				displayName = user.Profile.DisplayName
			}
			if displayName == "" {
				displayName = user.Name
			}

			// Get avatar URL with fallback to different sizes
			avatarURL := ""
			if user.Profile.Image512 != "" {
				avatarURL = user.Profile.Image512
			} else if user.Profile.Image192 != "" {
				avatarURL = user.Profile.Image192
			} else if user.Profile.Image72 != "" {
				avatarURL = user.Profile.Image72
			} else if user.Profile.Image48 != "" {
				avatarURL = user.Profile.Image48
			} else if user.Profile.Image32 != "" {
				avatarURL = user.Profile.Image32
			}

			// Serialize extra data if present
			extraJSON := ""
			if len(extraData) > 0 {
				if extraBytes, err := json.Marshal(extraData); err == nil {
					extraJSON = string(extraBytes)
				}
			}

			contacts = append(contacts, models.LinkedAccount{
				UserID:    user.ID,
				Username:  displayName, // Use display name instead of username
				IsGroup:   false, // Individual user
				AvatarURL: avatarURL,
				Status:    status,
				Protocol:  "slack",
				Extra:     extraJSON,
			})
		}
		p.log("SlackProvider.GetContacts: Retrieved %d users\n", len(contacts))
	}

	// 3. Process channels (exclude IMs here as they are covered by users)
	filteredCount := 0
	var mpimChannels []slack.Channel // Store MPIMs for async processing
	for _, channel := range allChannels {
		// Skip IMs (handled via users) and channels we left
		if channel.IsIM || !channel.IsMember {
			filteredCount++
			continue
		}

		// Determine the display name
		displayName := channel.Name

		// For mpim, use temporary name
		if channel.IsMpIM {
			mpimChannels = append(mpimChannels, channel)
			displayName = "Group Chat"
		}

		// Prepare extra data with LastRead
		extraData := make(map[string]interface{})
		if channel.LastRead != "" {
			extraData["last_read"] = channel.LastRead
		}
		extraJSON := ""
		if len(extraData) > 0 {
			if extraBytes, err := json.Marshal(extraData); err == nil {
				extraJSON = string(extraBytes)
			}
		}

		// Channels are group conversations in Slack
		contacts = append(contacts, models.LinkedAccount{
			UserID:   channel.ID,
			Username: displayName,
			IsGroup:  true,
			Status:   "offline", // Channels don't have online status
			Protocol: "slack",
			Extra:    extraJSON,
		})
	}

	// Process MPIMs asynchronously to update their names progressively
	if len(mpimChannels) > 0 {
		// Set MPIM count before starting async processing
		p.mpimCountMu.Lock()
		p.mpimCount = len(mpimChannels)
		p.mpimCountMu.Unlock()
		go p.updateMPIMNamesAsync(mpimChannels, users)
	}
	p.log("SlackProvider.GetContacts: Retrieved %d channels (%d filtered out)\n",
		len(allChannels)-filteredCount, filteredCount)

	p.log("SlackProvider.GetContacts: Total contacts (users + channels): %d\n", len(contacts))
	return contacts, nil
}

// updateMPIMNamesAsync processes MPIM channels asynchronously and updates their names in the database
// This allows other conversations to be displayed immediately while MPIM names are fetched progressively
func (p *SlackProvider) updateMPIMNamesAsync(mpimChannels []slack.Channel, users []slack.User) {
	p.log("SlackProvider.updateMPIMNamesAsync: Processing %d MPIMs asynchronously\n", len(mpimChannels))

	// Get instance ID for database updates
	p.mu.RLock()
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	if instanceID == "" {
		p.log("SlackProvider.updateMPIMNamesAsync: No instance ID, skipping MPIM name updates\n")
		return
	}

	// Get current user ID to exclude self from participant names
	currentUserID := ""
	if authTest, err := p.client.AuthTest(); err == nil && authTest != nil {
		currentUserID = authTest.UserID
	}

	// Slug Cache: optimize by parsing MPIM names
	// Map "slug" (username handle) -> "Real Name", and userID -> "Real Name"
	slugCache := make(map[string]string)

	// Populate slug cache from initial users list
	for _, u := range users {
		name := u.RealName
		if name == "" {
			name = u.Name
		}
		if u.Name != "" {
			slugCache[u.Name] = name
		}
		slugCache[u.ID] = name
	}

	// Process each MPIM one by one with rate limiting
	processedCount := 0
	for _, channel := range mpimChannels {
		// Check if we should stop
		select {
		case <-p.stopChan:
			p.log("SlackProvider.updateMPIMNamesAsync: Stop signal received, aborting\n")
			return
		default:
		}

		// Optimization: Try to resolve names from MPIM title (slugs) first
		// Typical format: "mpdm-user1--user2--user3-1" (includes all participants incl. self)
		var participantNames []string
		resolvedFromSlugs := false

		if strings.HasPrefix(channel.Name, "mpdm-") {
			content := strings.TrimPrefix(channel.Name, "mpdm-")
			// Remove trailing number suffix (e.g. "-1")
			if lastDash := strings.LastIndex(content, "-"); lastDash != -1 {
				if _, err := strconv.Atoi(content[lastDash+1:]); err == nil {
					content = content[:lastDash]
				}
			}

			slugs := strings.Split(content, "--")
			if len(slugs) > 0 {
				slugNames := make([]string, 0, len(slugs))
				allResolved := true

				for _, slug := range slugs {
					if name, ok := slugCache[slug]; ok {
						// Skip if this slug resolves to the current user
						if currentUserID != "" && slugCache[currentUserID] == name {
							continue
						}
						slugNames = append(slugNames, name)
					} else {
						allResolved = false
						break
					}
				}

				if allResolved && len(slugNames) > 0 {
					participantNames = slugNames
					resolvedFromSlugs = true
				}
			}
		}

		if !resolvedFromSlugs {
			// GetConversations does not return Members; fetch them explicitly
			memberIDs, _, err := p.client.GetUsersInConversation(&slack.GetUsersInConversationParameters{
				ChannelID: channel.ID,
			})
			if err != nil {
				p.log("SlackProvider.updateMPIMNamesAsync: Failed to get members for %s: %v\n", channel.ID, err)
				memberIDs = nil
			}

			missingUserIDs := make([]string, 0)
			for _, memberID := range memberIDs {
				if memberID == "USLACKBOT" || memberID == currentUserID {
					continue
				}
				if name, ok := slugCache[memberID]; ok {
					participantNames = append(participantNames, name)
				} else {
					missingUserIDs = append(missingUserIDs, memberID)
				}
			}

			// Fetch names for any members not in cache
			for _, userID := range missingUserIDs {
				user, err := p.client.GetUserInfo(userID)
				if err != nil {
					p.log("SlackProvider.updateMPIMNamesAsync: Failed to get info for user %s: %v\n", userID, err)
					continue
				}
				name := user.RealName
				if name == "" {
					name = user.Name
				}
				slugCache[userID] = name
				if user.Name != "" {
					slugCache[user.Name] = name
				}
				participantNames = append(participantNames, name)
				time.Sleep(200 * time.Millisecond)
			}
		}

		// Sort names for consistency
		displayName := "Group Chat"
		if len(participantNames) > 0 {
			displayName = strings.Join(participantNames, ", ")
		} else if channel.Name != "" {
			displayName = channel.Name
		}

		// DEBUG: Log the MPIM name analysis as requested
		p.log("SlackProvider.updateMPIMNamesAsync: [MPIM-ANALYSIS] ID=%s, Name='%s', Members=%d, ResolvedNames=[%s], FinalDisplayName='%s', FromSlugs=%v\n",
			channel.ID, channel.Name, len(channel.Members), strings.Join(participantNames, ", "), displayName, resolvedFromSlugs)

		// Update the LinkedAccount in the database
		if err := p.updateLinkedAccountName(instanceID, channel.ID, displayName); err != nil {
			p.log("SlackProvider.updateMPIMNamesAsync: Failed to update MPIM %s name: %v\n", channel.ID, err)
		} else {
			p.log("SlackProvider.updateMPIMNamesAsync: Updated MPIM %s name to: %s\n", channel.ID, displayName)
		}

		processedCount++

		// Update MPIM count
		p.mpimCountMu.Lock()
		remaining := p.mpimCount - processedCount
		if remaining < 0 {
			remaining = 0
		}
		p.mpimCount = remaining
		p.mpimCountMu.Unlock()

		// Emit contacts-refresh event to update frontend (but only every 5 MPIMs to reduce load)
		// Or emit sync status update if we're near the end
		if remaining == 0 || processedCount%5 == 0 {
			select {
			case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(), UserID: "refresh", Status: "mpim_updated"}:
				// Event sent successfully
			default:
				// Channel full, skip
			}
		}

		// Add delay between MPIM requests to avoid rate limits
		// Only delay if we actually hit the API (i.e. NOT resolved from slugs)
		if !resolvedFromSlugs {
			time.Sleep(1200 * time.Millisecond)
		} else {
			// Minimal delay even for cache hits to yield CPU
			time.Sleep(10 * time.Millisecond)
		}
	}

	p.log("SlackProvider.updateMPIMNamesAsync: Completed processing %d MPIMs\n", len(mpimChannels))

	// Signal that MPIM processing is complete
	select {
	case p.mpimProcessingChan <- struct{}{}:
		// Signal sent
	default:
		// Channel full, skip
	}

	// Clear MPIM count
	p.mpimCountMu.Lock()
	p.mpimCount = 0
	p.mpimCountMu.Unlock()
}

// updateLinkedAccountName updates the username of a LinkedAccount in the database
func (p *SlackProvider) updateLinkedAccountName(instanceID, userID, username string) error {
	if db.DB == nil || userID == "" || username == "" {
		return fmt.Errorf("invalid parameters for updateLinkedAccountName")
	}

	// First, try to find existing LinkedAccount
	var account models.LinkedAccount
	err := db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, userID).First(&account).Error

	if err == nil {
		// Record exists, update LinkedAccount username (this is the source of truth)
		account.Username = username
		account.UpdatedAt = time.Now()
		if err := db.DB.Save(&account).Error; err != nil {
			return fmt.Errorf("failed to update LinkedAccount name: %w", err)
		}
	} else {
		// Record does not exist, create it (and corresponding MetaContact)

		// 1. Create MetaContact first
		metaContact := models.MetaContact{
			DisplayName: username,
			AvatarURL:   "", // No avatar for MPIM usually
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := db.DB.Create(&metaContact).Error; err != nil {
			return fmt.Errorf("failed to create MetaContact: %w", err)
		}

		// 2. Create LinkedAccount linked to MetaContact
		account := models.LinkedAccount{
			MetaContactID:      metaContact.ID,
			ProviderInstanceID: instanceID,
			UserID:             userID,
			Username:           username,
			IsGroup:            true, // MPIM is a group
			Status:             "offline", // Default status
			Protocol:           "slack",
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}
		if err := db.DB.Create(&account).Error; err != nil {
			return fmt.Errorf("failed to create LinkedAccount: %w", err)
		}

		p.log("SlackProvider: Created new MetaContact (ID=%d) and LinkedAccount for %s (%s)\n", metaContact.ID, username, userID)
	}

	return nil
}

// normalizeDMConversationID resolves DM channel IDs to User IDs for consistency
// This prevents duplicate conversations when Slack returns both channel and user IDs
func (p *SlackProvider) normalizeDMConversationID(conversationID string) string {
	// Only normalize DM channels (start with 'D' but not MPIMs which have different format)
	if len(conversationID) == 0 || conversationID[0] != 'D' {
		return conversationID
	}

	// Check cache first to avoid repeated API calls
	p.dmChannelCacheMu.RLock()
	if cached, ok := p.dmChannelCache[conversationID]; ok {
		p.dmChannelCacheMu.RUnlock()
		return cached
	}
	p.dmChannelCacheMu.RUnlock()

	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return conversationID
	}

	// Try to get the UserID from the channel info
	convInfo, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: conversationID,
	})
	if err == nil && convInfo != nil && convInfo.IsIM && convInfo.User != "" {
		// This is a DM, cache and return the UserID instead of channel ID
		p.log("SlackProvider.normalizeDMConversationID: Resolved DM channel %s to user %s\n", conversationID, convInfo.User)
		p.dmChannelCacheMu.Lock()
		p.dmChannelCache[conversationID] = convInfo.User
		p.dmChannelCacheMu.Unlock()
		return convInfo.User
	}

	// Cache the identity mapping so we don't retry failed lookups repeatedly
	p.dmChannelCacheMu.Lock()
	p.dmChannelCache[conversationID] = conversationID
	p.dmChannelCacheMu.Unlock()

	// If we can't resolve, return original (might be a MPIM or already a User ID)
	return conversationID
}

// ensureConversation creates or retrieves a Conversation record for a ProtocolConvID
// It links the Conversation to the LinkedAccount and returns the ConversationID
// If LinkedAccount doesn't exist, it creates it first
func (p *SlackProvider) ensureConversation(protocolConvID string) (uint, error) {
	if db.DB == nil || protocolConvID == "" {
		return 0, fmt.Errorf("invalid parameters for ensureConversation")
	}

	// Get instance ID from config
	p.mu.RLock()
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	if instanceID == "" {
		return 0, fmt.Errorf("instance ID missing, cannot ensure conversation")
	}

	// IMPORTANT: For DM channels, we need to resolve to UserID first
	// Slack DM channels have IDs like "DEHFWSQF6", but LinkedAccount should use UserID like "UDY7N2D2L"
	actualUserID := protocolConvID
	if len(protocolConvID) > 0 && protocolConvID[0] == 'D' {
		// This might be a DM channel. Try to get the UserID from the channel info
		p.mu.RLock()
		client := p.client
		p.mu.RUnlock()

		if client != nil {
			convInfo, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
				ChannelID: protocolConvID,
			})
			if err == nil && convInfo != nil && convInfo.IsIM && convInfo.User != "" {
				// This is a DM, use the UserID instead of channel ID
				actualUserID = convInfo.User
				p.log("SlackProvider.ensureConversation: Resolved DM channel %s to user %s\n", protocolConvID, actualUserID)
			}
		}
	}

	// Find the LinkedAccount for this user ID, or create it if it doesn't exist
	var linkedAccount models.LinkedAccount
	err := db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, actualUserID).First(&linkedAccount).Error
	if err != nil {
		// LinkedAccount doesn't exist, create it
		// Try to get a display name for the conversation
		displayName := p.resolveConversationName(protocolConvID, "")
		if displayName == "" {
			displayName = protocolConvID // Fallback to ID if we can't resolve name
		}

		// Create LinkedAccount via ensureConversationContact (which will also resolve DM → UserID)
		if err = p.ensureConversationContact(protocolConvID, displayName); err != nil {
			return 0, fmt.Errorf("failed to create linked account for protocolConvID %s: %w", protocolConvID, err)
		}

		// Try again to find the LinkedAccount (using actualUserID)
		if err = db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, actualUserID).First(&linkedAccount).Error; err != nil {
			return 0, fmt.Errorf("linked account still not found after creation for protocolConvID %s (resolved to %s): %w", protocolConvID, actualUserID, err)
		}
	}

	// Check if Conversation already exists
	var conversation models.Conversation
	err = db.DB.Where("protocol_conv_id = ?", protocolConvID).First(&conversation).Error
	if err == nil {
		// Conversation exists, update LinkedAccountID if needed
		if conversation.LinkedAccountID != linkedAccount.ID {
			conversation.LinkedAccountID = linkedAccount.ID
			if err := db.DB.Save(&conversation).Error; err != nil {
				return 0, fmt.Errorf("failed to update conversation: %w", err)
			}
		}
		return conversation.ID, nil
	}

	// Conversation doesn't exist, create it
	// Determine if it's a group (channels start with C, groups with G, MPIMs with D, DMs with D)
	isGroup := strings.HasPrefix(protocolConvID, "C") || strings.HasPrefix(protocolConvID, "G")
	groupName := ""
	if isGroup {
		// Try to get group name from LinkedAccount
		groupName = linkedAccount.Username
	}

	conversation = models.Conversation{
		LinkedAccountID: linkedAccount.ID,
		ProtocolConvID:  protocolConvID,
		IsGroup:         isGroup,
		GroupName:       groupName,
		IsPinned:        false,
		IsMuted:         false,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := db.DB.Create(&conversation).Error; err != nil {
		return 0, fmt.Errorf("failed to create conversation: %w", err)
	}

	p.log("SlackProvider.ensureConversation: Created conversation %d for ProtocolConvID %s\n", conversation.ID, protocolConvID)
	return conversation.ID, nil
}
