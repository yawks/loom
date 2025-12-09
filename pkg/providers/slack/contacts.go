// Package slack provides the Slack provider implementation.
package slack

import (
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// GetContacts returns the list of contacts for this protocol.
// This includes both individual users and group conversations (channels).
func (p *SlackProvider) GetContacts() ([]models.LinkedAccount, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	var contacts []models.LinkedAccount

	// Get individual users
	users, err := p.client.GetUsers()
	if err != nil {
		p.log("SlackProvider.GetContacts: WARNING - failed to get users: %v\n", err)
	} else {
		for _, user := range users {
			if user.Deleted || user.IsBot {
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
				AvatarURL: avatarURL,
				Status:    status,
				Protocol:  "slack",
				Extra:     extraJSON,
			})
		}
		p.log("SlackProvider.GetContacts: Retrieved %d users\n", len(contacts))
	}

	// Get group conversations (channels)
	channels, nextCursor, err := p.client.GetConversations(&slack.GetConversationsParameters{
		Types:           []string{"public_channel", "private_channel"},
		Limit:           1000, // Get up to 1000 channels
		ExcludeArchived: true,
	})
	if err != nil {
		p.log("SlackProvider.GetContacts: WARNING - failed to get channels: %v\n", err)
	} else {
		// Handle pagination if needed
		allChannels := channels
		for nextCursor != "" {
			moreChannels, cursor, err := p.client.GetConversations(&slack.GetConversationsParameters{
				Types:           []string{"public_channel", "private_channel"},
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

		for _, channel := range allChannels {
			// Channels are group conversations in Slack
			// We already have the channel name from GetConversations, so we don't need to fetch detailed info
			// Channels don't typically have avatars in Slack API
			contacts = append(contacts, models.LinkedAccount{
				UserID:   channel.ID,
				Username: channel.Name,
				Status:   "offline", // Channels don't have online status
				Protocol: "slack",
			})
		}
		p.log("SlackProvider.GetContacts: Retrieved %d channels\n", len(allChannels))
	}

	p.log("SlackProvider.GetContacts: Total contacts (users + channels): %d\n", len(contacts))
	return contacts, nil
}
