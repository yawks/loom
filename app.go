// Package main is the entry point for the Loom chat application.
package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gorm.io/gorm"
)

// App struct
type App struct {
	ctx             context.Context
	provider        core.Provider // Use the interface
	providerManager *core.ProviderManager
	eventChan       <-chan core.ProviderEvent
	eventCancel     context.CancelFunc
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize the database
	if err := db.InitDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Initialize provider manager
	a.providerManager = core.NewProviderManager()
	fmt.Printf("App.startup: ProviderManager initialized\n")

	// Register available providers
	a.providerManager.RegisterProvider("mock", core.ProviderInfo{
		ID:          "mock",
		Name:        "Mock",
		Description: "Mock provider for development and testing",
		ConfigSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}, func() core.Provider {
		return providers.NewMockProvider()
	})

	a.providerManager.RegisterProvider("whatsapp", core.ProviderInfo{
		ID:          "whatsapp",
		Name:        "WhatsApp",
		Description: "WhatsApp messaging provider",
		ConfigSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}, func() core.Provider {
		return providers.NewWhatsAppProvider()
	})

	// Load and restore providers from database
	configs, err := a.providerManager.LoadProviderConfigs()
	if err != nil {
		fmt.Printf("App.startup: Warning: Failed to load provider configs: %v\n", err)
		configs = []models.ProviderConfiguration{}
	}
	fmt.Printf("App.startup: Loaded %d provider configs from database\n", len(configs))

	// Restore providers from database
	var activeProvider core.Provider
	for _, config := range configs {
		// Capture config for goroutine
		providerConfig := config
		fmt.Printf("App.startup: Attempting to restore provider %s (IsActive: %v)\n", providerConfig.ProviderID, providerConfig.IsActive)

		fmt.Printf("App.startup: About to call RestoreProvider for %s\n", providerConfig.ProviderID)
		provider, err := a.providerManager.RestoreProvider(providerConfig)
		if err != nil {
			fmt.Printf("App.startup: ERROR - Failed to restore provider %s: %v\n", providerConfig.ProviderID, err)
			continue
		}
		fmt.Printf("App.startup: Successfully restored provider %s from database, provider is nil: %v\n", providerConfig.ProviderID, provider == nil)

		// Only connect the provider if it's already authenticated
		// Providers that need authentication (like WhatsApp) should only be connected
		// when the user explicitly requests it via the UI
		isAuth := provider.IsAuthenticated()
		fmt.Printf("App.startup: Provider %s IsAuthenticated: %v\n", providerConfig.ProviderID, isAuth)
		if isAuth {
			if err := provider.Connect(); err != nil {
				log.Printf("Warning: Failed to connect provider %s: %v", providerConfig.ProviderID, err)
				continue
			}
			log.Printf("Provider %s connected successfully", providerConfig.ProviderID)
		} else {
			log.Printf("Provider %s is not authenticated yet, skipping auto-connect. User must configure it first.", providerConfig.ProviderID)
			// Don't set as active if not authenticated
			if providerConfig.IsActive {
				log.Printf("Warning: Provider %s is marked as active but not authenticated, clearing active status", providerConfig.ProviderID)
				// Clear active status in database
				if db.DB != nil {
					db.DB.Model(&models.ProviderConfiguration{}).Where("provider_id = ?", providerConfig.ProviderID).Update("is_active", false)
				}
				providerConfig.IsActive = false
			}
			continue
		}

		// Sync missed messages if last sync was more than 1 minute ago
		if providerConfig.LastSyncAt != nil {
			timeSinceLastSync := time.Since(*providerConfig.LastSyncAt)
			if timeSinceLastSync > time.Minute {
				go func(p core.Provider, providerID string, lastSync time.Time) {
					if err := p.SyncHistory(lastSync); err != nil {
						log.Printf("Warning: Failed to sync history for provider %s: %v", providerID, err)
					} else {
						// Update last sync time
						now := time.Now()
						if db.DB != nil {
							db.DB.Model(&models.ProviderConfiguration{}).Where("provider_id = ?", providerID).Update("last_sync_at", now)
						}
					}
				}(provider, providerConfig.ProviderID, *providerConfig.LastSyncAt)
			}
		} else {
			// First time sync - sync last 1 year to get all conversations
			// WhatsApp will automatically sync via HistorySync events, but we trigger a manual sync
			// with a long period to ensure we get all available conversations
			go func(p core.Provider, providerID string) {
				since := time.Now().Add(-365 * 24 * time.Hour) // 1 year ago
				fmt.Printf("App.startup: First time sync for provider %s, syncing since %s\n", providerID, since.Format("2006-01-02 15:04:05"))
				if err := p.SyncHistory(since); err != nil {
					log.Printf("Warning: Failed to sync history for provider %s: %v", providerID, err)
				} else {
					// Update last sync time
					now := time.Now()
					if db.DB != nil {
						db.DB.Model(&models.ProviderConfiguration{}).Where("provider_id = ?", providerID).Update("last_sync_at", now)
					}
				}
			}(provider, providerConfig.ProviderID)
		}

		// Set as active provider if marked as active
		if providerConfig.IsActive {
			activeProvider = provider
			a.provider = provider
			fmt.Printf("App.startup: Set provider %s as active provider\n", providerConfig.ProviderID)
		}
	}

	fmt.Printf("App.startup: Finished restoring providers. Active provider: %v\n", activeProvider != nil)
	fmt.Printf("App.startup: a.provider is nil: %v\n", a.provider == nil)

	// If no active provider was restored, check if MockProvider exists in database
	// Only create it if it doesn't exist (wasn't explicitly deleted by user)
	if activeProvider == nil {
		var mockConfig models.ProviderConfiguration
		mockExists := false
		if db.DB != nil {
			result := db.DB.Where("provider_id = ?", "mock").First(&mockConfig)
			mockExists = result.Error == nil
		}

		if mockExists {
			// MockProvider exists in database, restore it
			log.Println("No active provider found, restoring MockProvider from database")
			mockProvider := providers.NewMockProvider()
			if err := mockProvider.Init(nil); err != nil {
				log.Fatalf("Failed to initialize provider: %v", err)
			}
			if err := mockProvider.Connect(); err != nil {
				log.Fatalf("Failed to connect provider: %v", err)
			}
			// Add the mock provider to the manager
			a.providerManager.AddProvider("mock", mockProvider)
			// Set as active provider
			if err := a.providerManager.SetActiveProvider("mock"); err != nil {
				log.Printf("Warning: Failed to set mock provider as active: %v", err)
			}
			a.provider = mockProvider
			// Update active status in database
			if db.DB != nil {
				mockConfig.IsActive = true
				if err := db.DB.Save(&mockConfig).Error; err != nil {
					log.Printf("Warning: Failed to update mock provider config: %v", err)
				}
			}
		} else {
			// MockProvider was deleted by user, don't recreate it automatically
			log.Println("No active provider found and MockProvider was deleted. User must configure a provider.")
		}
	}

	// Start event listener for the active provider
	if a.provider != nil {
		log.Printf("Starting event listener for active provider: %T", a.provider)
	} else {
		log.Printf("Warning: No active provider found, event listener will not start")
	}
	a.startEventListener(ctx)

	// Test event emission after a short delay to ensure frontend is ready
	go func() {
		time.Sleep(2 * time.Second)
		if a.ctx != nil {
			log.Printf("App: Sending test event to verify event system works")
			runtime.EventsEmit(a.ctx, "test-event", `{"message": "Event system test"}`)
		}
	}()
}

// startEventListener starts listening to provider events and sends them to the frontend.
func (a *App) startEventListener(ctx context.Context) {
	// Cancel previous listener if any
	if a.eventCancel != nil {
		a.eventCancel()
	}

	// Check if provider is initialized
	if a.provider == nil {
		fmt.Printf("App.startEventListener: Warning: No provider available, skipping event stream setup\n")
		fmt.Printf("App.startEventListener: a.provider is nil, a.providerManager is nil: %v\n", a.providerManager == nil)
		if a.providerManager != nil {
			active, err := a.providerManager.GetActiveProvider()
			fmt.Printf("App.startEventListener: GetActiveProvider() returned: provider=%v, error=%v\n", active != nil, err)
		}
		return
	}
	fmt.Printf("App.startEventListener: Starting event listener for active provider\n")

	// Create context for this listener
	eventCtx, cancel := context.WithCancel(ctx)
	a.eventCancel = cancel

	// Start listening to provider events and send them to the frontend
	eventChan, err := a.provider.StreamEvents()
	if err != nil {
		log.Printf("Failed to get event stream: %v", err)
		cancel()
		return
	}

	a.eventChan = eventChan

	go func() {
		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					log.Printf("Event channel closed")
					return
				}
				switch e := event.(type) {
				case core.MessageEvent:
					log.Printf("App: Received MessageEvent for conversation %s, message ID: %s", e.Message.ProtocolConvID, e.Message.ProtocolMsgID)
					// Convert avatar path to base64 data URL if present
					if e.Message.SenderAvatarURL != "" {
						avatarURL := a.GetAvatar(e.Message.SenderAvatarURL)
						if avatarURL != "" {
							e.Message.SenderAvatarURL = avatarURL
						}
					}
					// Serialize the message to JSON
					msgJSON, err := json.Marshal(e.Message)
					if err != nil {
						log.Printf("Failed to marshal message: %v", err)
						continue
					}
					// Emit the event to the frontend using the app context
					log.Printf("App: Emitting new-message event to frontend for message %s", e.Message.ProtocolMsgID)
					previewLen := 100
					if len(msgJSON) < previewLen {
						previewLen = len(msgJSON)
					}
					log.Printf("App: Message JSON length: %d bytes, first %d chars: %s", len(msgJSON), previewLen, string(msgJSON[:previewLen]))
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "new-message", string(msgJSON))
						log.Printf("App: Event emitted (no error returned)")
					} else {
						log.Printf("App: ERROR - a.ctx is nil, cannot emit event")
					}

				case core.ReactionEvent:
					// Serialize the reaction to JSON
					reactionJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal reaction: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "reaction", string(reactionJSON))
					}

				case core.TypingEvent:
					// Serialize the typing indicator to JSON
					typingJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal typing indicator: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "typing", string(typingJSON))
					}

				case core.ContactStatusEvent:
					// Serialize the contact status to JSON
					statusJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal contact status: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "contact-status", string(statusJSON))
						// If this is a refresh event, also invalidate the contacts query
						if e.UserID == "refresh" && (e.Status == "sync_complete" || e.Status == "message_received") {
							runtime.EventsEmit(a.ctx, "contacts-refresh", "{}")
						}
					}

				case core.GroupChangeEvent:
					// Serialize the group change to JSON
					groupChangeJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal group change: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "group-change", string(groupChangeJSON))
					}

				case core.ReceiptEvent:
					// Serialize the receipt to JSON
					receiptJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal receipt: %v", err)
						continue
					}
					// Emit the event to the frontend
					log.Printf("App: Received ReceiptEvent for conversation %s, message %s, type: %s", e.ConversationID, e.MessageID, e.ReceiptType)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "receipt", string(receiptJSON))
						log.Printf("App: Emitted receipt event to frontend")
					}

				case core.RetryReceiptEvent:
					// Serialize the retry receipt to JSON
					retryReceiptJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal retry receipt: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "retry-receipt", string(retryReceiptJSON))
					}
				case core.SyncStatusEvent:
					// Serialize the sync status to JSON
					syncStatusJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal sync status: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "sync-status", string(syncStatusJSON))
					}
				}
			case <-eventCtx.Done():
				log.Printf("Event listener stopped")
				return
			}
		}
	}()
}

// domReady is called when the frontend is ready.
func (a *App) domReady(ctx context.Context) {
	// Start listening to provider events
	a.startEventListener(ctx)
}

// shutdown is called at application closure.
func (a *App) shutdown(_ context.Context) {
	if a.provider != nil {
		a.provider.Disconnect()
	}
}

// --- Methods Exposed to the Frontend ---

// GetMetaContacts returns a list of unified contacts.
func (a *App) GetMetaContacts() ([]models.MetaContact, error) {
	// TODO: Implement logic to merge contacts from different providers from the database.
	// For this mock, we create meta contacts from the mock provider's data.

	// Get the active provider from the manager instead of using a.provider directly
	// This ensures we always use the correct active provider
	activeProvider, err := a.providerManager.GetActiveProvider()
	if err != nil {
		log.Printf("GetMetaContacts: No active provider found, trying a.provider: %v", err)
		if a.provider == nil {
			log.Printf("Warning: No provider available, returning empty contacts")
			return []models.MetaContact{}, nil
		}
		activeProvider = a.provider
	}

	log.Printf("GetMetaContacts: Calling GetContacts on provider %T (active: %v)", activeProvider, activeProvider != nil)
	linkedAccounts, err := activeProvider.GetContacts()
	if err != nil {
		log.Printf("GetMetaContacts: Error getting contacts: %v", err)
		return nil, err
	}
	log.Printf("GetMetaContacts: Retrieved %d linked accounts", len(linkedAccounts))

	// This is a simulation of contact grouping.
	// A real implementation would involve more complex logic from the database.
	metaContactsMap := make(map[string]*models.MetaContact)

	for _, acc := range linkedAccounts {
		// Use Username, but if empty, try to format UserID nicely
		displayName := acc.Username
		if displayName == "" {
			// Try to format UserID as a display name
			// For WhatsApp IDs like "33631207926@s.whatsapp.net", extract phone number
			if whatsappMatch := regexp.MustCompile(`^(\d+)@s\.whatsapp\.net$`).FindStringSubmatch(acc.UserID); whatsappMatch != nil {
				phoneNumber := whatsappMatch[1]
				if phoneNumber != "" {
					displayName = phoneNumber
				}
			}
			if displayName == "" {
				// Fallback to UserID
				displayName = acc.UserID
			}
		}

		// Use UserID as the key to avoid collisions when multiple accounts have same display name
		key := acc.UserID
		if _, exists := metaContactsMap[key]; !exists {
			// Use avatar from LinkedAccount if available, otherwise fallback to dicebear
			avatarURL := acc.AvatarURL
			if avatarURL == "" {
				avatarURL = fmt.Sprintf("https://api.dicebear.com/7.x/initials/svg?seed=%s", displayName)
			} else {
				// Check if file exists before trying to convert
				if _, err := os.Stat(avatarURL); err == nil {
					// Convert local file path to base64 data URL
					avatarURL = a.GetAvatar(avatarURL)
					if avatarURL == "" {
						// If GetAvatar failed, fallback to dicebear
						avatarURL = fmt.Sprintf("https://api.dicebear.com/7.x/initials/svg?seed=%s", displayName)
					}
				} else {
					// File doesn't exist, use dicebear fallback
					avatarURL = fmt.Sprintf("https://api.dicebear.com/7.x/initials/svg?seed=%s", displayName)
				}
			}
			metaContactsMap[key] = &models.MetaContact{
				ID:             uint(len(metaContactsMap) + 1),
				DisplayName:    displayName,
				AvatarURL:      avatarURL,
				LinkedAccounts: []models.LinkedAccount{},
			}
		}

		meta := metaContactsMap[key]
		// Update avatar if the LinkedAccount has one and MetaContact doesn't
		if acc.AvatarURL != "" {
			// Check if current avatar is dicebear fallback
			isDicebear := strings.HasPrefix(meta.AvatarURL, "https://api.dicebear.com/") || meta.AvatarURL == ""
			if isDicebear {
				// Check if file exists before trying to convert
				if _, err := os.Stat(acc.AvatarURL); err == nil {
					// Convert local file path to base64 data URL
					avatarURL := a.GetAvatar(acc.AvatarURL)
					if avatarURL != "" {
						meta.AvatarURL = avatarURL
					}
				}
			}
		}
		meta.LinkedAccounts = append(meta.LinkedAccounts, acc)

		if !acc.CreatedAt.IsZero() {
			if meta.CreatedAt.IsZero() || acc.CreatedAt.Before(meta.CreatedAt) {
				meta.CreatedAt = acc.CreatedAt
			}
		}

		candidateUpdated := acc.UpdatedAt
		if candidateUpdated.IsZero() {
			candidateUpdated = acc.CreatedAt
		}
		if !candidateUpdated.IsZero() {
			if meta.UpdatedAt.IsZero() || candidateUpdated.After(meta.UpdatedAt) {
				meta.UpdatedAt = candidateUpdated
			}
		}
	}

	// Load contact aliases from database
	aliasMap := make(map[string]string)
	if db.DB != nil {
		var aliases []models.ContactAlias
		if err := db.DB.Find(&aliases).Error; err == nil {
			for _, alias := range aliases {
				aliasMap[alias.UserID] = alias.Alias
			}
		}
	}

	// Apply aliases to meta contacts
	for _, contact := range metaContactsMap {
		// Check if any linked account has an alias
		for i := range contact.LinkedAccounts {
			if alias, exists := aliasMap[contact.LinkedAccounts[i].UserID]; exists {
				// Use the alias as display name
				// Only update avatar if it's a dicebear fallback (preserve real avatars)
				if strings.HasPrefix(contact.AvatarURL, "https://api.dicebear.com/") {
					contact.AvatarURL = fmt.Sprintf("https://api.dicebear.com/7.x/initials/svg?seed=%s", alias)
				}
				contact.DisplayName = alias
				break // Use first alias found
			}
		}
	}

	// Convert map to slice
	metaContacts := make([]models.MetaContact, 0, len(metaContactsMap))
	for _, contact := range metaContactsMap {
		metaContacts = append(metaContacts, *contact)
	}

	return metaContacts, nil
}

// GetMessagesForConversation returns messages for a given conversation ID.
func (a *App) GetMessagesForConversation(conversationID string) ([]models.Message, error) {
	return a.GetMessagesForConversationBefore(conversationID, nil)
}

// GetMessagesForConversationBefore returns messages for a given conversation ID before a specific timestamp.
func (a *App) GetMessagesForConversationBefore(conversationID string, beforeTimestamp *time.Time) ([]models.Message, error) {
	// Use the provider's GetConversationHistory method
	// Limit to 20 messages by default
	activeProvider, err := a.providerManager.GetActiveProvider()
	if err != nil || activeProvider == nil {
		return nil, fmt.Errorf("no active provider available")
	}
	messages, err := activeProvider.GetConversationHistory(conversationID, 20, beforeTimestamp)
	if err != nil {
		return nil, err
	}

	// Convert avatar paths to base64 data URLs
	for i := range messages {
		if messages[i].SenderAvatarURL != "" {
			avatarURL := a.GetAvatar(messages[i].SenderAvatarURL)
			if avatarURL != "" {
				messages[i].SenderAvatarURL = avatarURL
			}
		}
	}

	return messages, nil
}

// SendMessage sends a text message.
func (a *App) SendMessage(conversationID string, text string) (*models.Message, error) {
	return a.provider.SendMessage(conversationID, text, nil, nil)
}

// SendFile sends a file to a conversation.
// fileData is the base64-encoded file content.
// fileName is the name of the file.
// mimeType is the MIME type of the file (e.g., "image/jpeg", "application/pdf").
func (a *App) SendFile(conversationID string, fileData string, fileName string, mimeType string) (*models.Message, error) {
	if a.provider == nil {
		return nil, fmt.Errorf("no active provider")
	}

	// Decode base64 file data
	data, err := base64.StdEncoding.DecodeString(fileData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode file data: %w", err)
	}

	// Create attachment
	attachment := &core.Attachment{
		FileName: fileName,
		FileSize: len(data),
		MimeType: mimeType,
		Data:     data,
	}

	return a.provider.SendFile(conversationID, attachment, nil)
}

// EditMessage edits an existing message.
func (a *App) EditMessage(conversationID string, messageID string, newText string) (*models.Message, error) {
	if a.provider == nil {
		return nil, fmt.Errorf("no active provider")
	}
	return a.provider.EditMessage(conversationID, messageID, newText)
}

// DeleteMessage deletes a message.
func (a *App) DeleteMessage(conversationID string, messageID string) error {
	log.Printf("DeleteMessage called: conversationID=%s, messageID=%s", conversationID, messageID)
	if a.provider == nil {
		log.Printf("DeleteMessage error: no active provider")
		return fmt.Errorf("no active provider")
	}
	err := a.provider.DeleteMessage(conversationID, messageID)
	if err != nil {
		log.Printf("DeleteMessage error: %v", err)
	} else {
		log.Printf("DeleteMessage success: message %s deleted", messageID)
	}
	return err
}

// SendFileFromPath sends a file to a conversation by reading it from a file path.
// This is useful for files that cannot be read via FileReader in the browser.
func (a *App) SendFileFromPath(conversationID string, filePath string) (*models.Message, error) {
	if a.provider == nil {
		return nil, fmt.Errorf("no active provider")
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", filePath)
	}

	// Read the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Determine MIME type from file extension
	mimeType := "application/octet-stream"
	ext := filepath.Ext(filePath)
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".mp4":
		mimeType = "video/mp4"
	case ".mp3":
		mimeType = "audio/mpeg"
	case ".pdf":
		mimeType = "application/pdf"
	case ".xls", ".xlsx":
		mimeType = "application/vnd.ms-excel"
	}

	// Get filename from path
	fileName := filepath.Base(filePath)

	// Create attachment
	attachment := &core.Attachment{
		FileName: fileName,
		FileSize: len(data),
		MimeType: mimeType,
		Data:     data,
	}

	return a.provider.SendFile(conversationID, attachment, nil)
}

// GetThreads returns all messages in a thread for a given parent message ID.
func (a *App) GetThreads(parentMessageID string) ([]models.Message, error) {
	return a.provider.GetThreads(parentMessageID)
}

// MarkMessageAsRead sends a read receipt for a specific message.
func (a *App) MarkMessageAsRead(conversationID string, messageID string) error {
	log.Printf("App: MarkMessageAsRead called for conversation %s, message %s", conversationID, messageID)
	if a.provider == nil {
		return fmt.Errorf("no active provider")
	}
	err := a.provider.MarkMessageAsRead(conversationID, messageID)
	if err != nil {
		log.Printf("App: Failed to mark message as read: %v", err)
		return err
	}
	log.Printf("App: Successfully marked message %s as read in conversation %s", messageID, conversationID)
	return nil
}

// GetAvatar returns the avatar image as a base64 data URL.

// GetAttachmentData reads an attachment file and returns it as a base64 data URL.
func (a *App) GetAttachmentData(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path is empty")
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist: %s", filePath)
	}

	// Read the file
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Determine MIME type from file extension
	mimeType := "application/octet-stream"
	ext := filepath.Ext(filePath)
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".mp4":
		mimeType = "video/mp4"
	case ".mp3":
		mimeType = "audio/mpeg"
	case ".pdf":
		mimeType = "application/pdf"
	case ".xls", ".xlsx":
		mimeType = "application/vnd.ms-excel"
	}

	// Encode to base64
	base64Data := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

// GetAvatar reads an avatar file and returns it as a base64 data URL.
// If the path is empty or the file doesn't exist, returns empty string.
func (a *App) GetAvatar(filePath string) string {
	if filePath == "" {
		return ""
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return ""
	}

	// Read the file
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("Failed to open avatar file %s: %v", filePath, err)
		return ""
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		log.Printf("Failed to read avatar file %s: %v", filePath, err)
		return ""
	}

	// Determine MIME type based on file extension
	mimeType := "image/jpeg"
	if len(filePath) > 4 {
		ext := filePath[len(filePath)-4:]
		switch ext {
		case ".png":
			mimeType = "image/png"
		case ".gif":
			mimeType = "image/gif"
		case ".webp":
			mimeType = "image/webp"
		}
	}

	// Encode to base64
	base64Data := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)
}

// --- Provider Management Methods ---

// GetAvailableProviders returns a list of all available providers.
func (a *App) GetAvailableProviders() ([]core.ProviderInfo, error) {
	fmt.Printf("App.GetAvailableProviders: called\n")
	if a.providerManager == nil {
		fmt.Printf("App.GetAvailableProviders: providerManager is nil\n")
		return []core.ProviderInfo{}, nil
	}
	providers := a.providerManager.GetAvailableProviders()
	fmt.Printf("App.GetAvailableProviders: returning %d providers\n", len(providers))
	return providers, nil
}

// GetConfiguredProviders returns a list of configured providers.
func (a *App) GetConfiguredProviders() ([]core.ProviderInfo, error) {
	fmt.Printf("App.GetConfiguredProviders: called\n")
	if a.providerManager == nil {
		fmt.Printf("App.GetConfiguredProviders: providerManager is nil\n")
		return []core.ProviderInfo{}, nil
	}
	providers := a.providerManager.GetConfiguredProviders()
	fmt.Printf("App.GetConfiguredProviders: returning %d providers\n", len(providers))
	return providers, nil
}

// CreateProvider creates a new provider instance.
func (a *App) CreateProvider(providerID string, config core.ProviderConfig) error {
	provider, err := a.providerManager.CreateProvider(providerID, config)
	if err != nil {
		return err
	}

	// If this is the first provider or we want to switch, make it active
	if a.provider == nil {
		if err := provider.Connect(); err != nil {
			return fmt.Errorf("failed to connect provider: %w", err)
		}
		a.provider = provider
		a.providerManager.SetActiveProvider(providerID)
	}

	// Update last sync time when creating a new provider
	if db.DB != nil {
		var providerConfig models.ProviderConfiguration
		if err := db.DB.Where("provider_id = ?", providerID).First(&providerConfig).Error; err == nil {
			now := time.Now()
			db.DB.Model(&providerConfig).Update("last_sync_at", now)
		}
	}

	return nil
}

// GetProviderQRCode returns the latest QR code for a provider (if applicable).
func (a *App) GetProviderQRCode(providerID string) (string, error) {
	provider, err := a.providerManager.GetProvider(providerID)
	if err != nil {
		return "", err
	}
	return provider.GetQRCode()
}

// ConnectProvider connects a provider and updates the database.
func (a *App) ConnectProvider(providerID string) error {
	provider, err := a.providerManager.GetProvider(providerID)
	if err != nil {
		return err
	}

	if err := provider.Connect(); err != nil {
		return err
	}

	// Set as active provider
	a.provider = provider
	if err := a.providerManager.SetActiveProvider(providerID); err != nil {
		log.Printf("Warning: Failed to set active provider: %v", err)
	} else {
		log.Printf("ConnectProvider: Successfully set provider %s as active", providerID)
	}

	// Restart event listener with the new provider
	log.Printf("ConnectProvider: Restarting event listener for provider %s", providerID)
	a.startEventListener(a.ctx)

	// Update last sync time after connection
	if db.DB != nil {
		now := time.Now()
		db.DB.Model(&models.ProviderConfiguration{}).Where("provider_id = ?", providerID).Updates(map[string]interface{}{
			"last_sync_at": now,
			"is_active":    true,
		})
	}

	log.Printf("ConnectProvider: Provider %s connected and set as active", providerID)

	return nil
}

// RemoveProvider removes a provider and deletes its config directory.
func (a *App) RemoveProvider(providerID string) error {
	// Cancel event listener if this is the active provider
	if a.provider != nil {
		currentProvider, _ := a.providerManager.GetActiveProvider()
		if currentProvider == a.provider && a.eventCancel != nil {
			a.eventCancel()
			a.eventCancel = nil
		}
	}

	// Remove provider (this will disconnect it and delete all associated data)
	if err := a.providerManager.RemoveProvider(providerID); err != nil {
		return err
	}

	// Delete provider's config directory
	configDir, err := os.UserConfigDir()
	if err == nil {
		providerConfigDir := filepath.Join(configDir, "Loom", providerID)
		if err := os.RemoveAll(providerConfigDir); err != nil {
			log.Printf("Warning: Failed to delete provider config directory %s: %v", providerConfigDir, err)
		} else {
			log.Printf("Deleted provider config directory: %s", providerConfigDir)
		}
	}

	// If this was the active provider, clear it and switch to MockProvider if available
	if a.provider != nil {
		currentProvider, _ := a.providerManager.GetActiveProvider()
		if currentProvider == nil {
			// No active provider, try to use MockProvider as fallback
			mockProvider, err := a.providerManager.GetProvider("mock")
			if err == nil {
				if err := mockProvider.Connect(); err == nil {
					a.provider = mockProvider
					a.providerManager.SetActiveProvider("mock")
					a.startEventListener(a.ctx)
				}
			} else {
				// No MockProvider available, clear active provider
				a.provider = nil
			}
		} else {
			// Update to the new active provider
			a.provider = currentProvider
			a.startEventListener(a.ctx)
		}
	}

	// Emit event to refresh contacts in frontend
	runtime.EventsEmit(a.ctx, "contacts-refresh", "{}")

	return nil
}

// SyncProvider triggers a synchronization for a specific provider.
// If providerID is empty, syncs the active provider.
func (a *App) SyncProvider(providerID string) error {
	var provider core.Provider
	var err error

	if providerID == "" {
		// Sync active provider
		if a.provider == nil {
			return fmt.Errorf("no active provider to sync")
		}
		provider = a.provider
	} else {
		// Sync specific provider
		provider, err = a.providerManager.GetProvider(providerID)
		if err != nil {
			return err
		}
	}

	// Check if this is the first sync (no LastSyncAt in database)
	var providerConfig models.ProviderConfiguration
	isFirstSync := false
	if db.DB != nil {
		if err := db.DB.Where("provider_id = ?", providerID).First(&providerConfig).Error; err != nil {
			// Provider not found, treat as first sync
			isFirstSync = true
		} else if providerConfig.LastSyncAt == nil {
			// First sync - sync last 1 year
			isFirstSync = true
		}
	}

	var since time.Time
	if isFirstSync {
		// First time sync - sync last 1 year to get all conversations
		since = time.Now().Add(-365 * 24 * time.Hour) // 1 year ago
		fmt.Printf("App.SyncProvider: First time sync for provider %s, syncing since %s\n", providerID, since.Format("2006-01-02 15:04:05"))
	} else {
		// Regular sync - sync last 24 hours
		since = time.Now().Add(-24 * time.Hour)
		fmt.Printf("App.SyncProvider: Regular sync for provider %s, syncing since %s\n", providerID, since.Format("2006-01-02 15:04:05"))
	}

	if err := provider.SyncHistory(since); err != nil {
		return fmt.Errorf("failed to sync history: %w", err)
	}

	// Update last sync time
	if db.DB != nil {
		now := time.Now()
		pid := providerID
		if pid == "" {
			// Get provider ID from active provider
			activeProvider, _ := a.providerManager.GetActiveProvider()
			if activeProvider != nil {
				// We need to find the provider ID - for now, we'll update all active providers
				db.DB.Model(&models.ProviderConfiguration{}).Where("is_active = ?", true).Update("last_sync_at", now)
			}
		} else {
			db.DB.Model(&models.ProviderConfiguration{}).Where("provider_id = ?", pid).Update("last_sync_at", now)
		}
	}

	return nil
}

// SetContactAlias sets a custom name (alias) for a contact identified by userID.
func (a *App) SetContactAlias(userID string, alias string) error {
	if db.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	if alias == "" {
		// If alias is empty, delete the alias
		return db.DB.Where("user_id = ?", userID).Delete(&models.ContactAlias{}).Error
	}

	contactAlias := models.ContactAlias{
		UserID: userID,
		Alias:  alias,
	}

	// Use FirstOrCreate to update if exists, create if not
	var existing models.ContactAlias
	result := db.DB.Where("user_id = ?", userID).First(&existing)
	if result.Error == nil {
		// Update existing
		existing.Alias = alias
		existing.UpdatedAt = time.Now()
		return db.DB.Save(&existing).Error
	} else if result.Error == gorm.ErrRecordNotFound {
		// Create new
		return db.DB.Create(&contactAlias).Error
	}
	return result.Error
}

// GetContactAliases returns all contact aliases as a map of userId -> alias.
func (a *App) GetContactAliases() (map[string]string, error) {
	if db.DB == nil {
		return make(map[string]string), nil
	}

	var aliases []models.ContactAlias
	if err := db.DB.Find(&aliases).Error; err != nil {
		return nil, err
	}

	aliasMap := make(map[string]string)
	for _, alias := range aliases {
		aliasMap[alias.UserID] = alias.Alias
	}

	return aliasMap, nil
}

// ClipboardFile represents a file retrieved from the system clipboard
type ClipboardFile struct {
	Filename string `json:"filename"`
	Base64   string `json:"base64"`
	MimeType string `json:"mimeType"`
}

// GetClipboardFile attempts to read a file from the system clipboard
// by bypassing browser security restrictions
func (a *App) GetClipboardFile() (*ClipboardFile, error) {
	var filePath string

	if goruntime.GOOS == "darwin" {
		// macOS: Use AppleScript to get the POSIX path
		// This handles the case where a file was copied from Finder
		cmd := exec.Command("osascript", "-e",
			`try
				tell application "System Events" to return POSIX path of (the clipboard as alias)
			on error
				return ""
			end try`)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to get clipboard file path: %w", err)
		}
		filePath = strings.TrimSpace(out.String())
	} else if goruntime.GOOS == "windows" {
		// Windows: PowerShell to get the file list
		cmd := exec.Command("powershell", "-command", "Get-Clipboard -Format FileDropList")
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to get clipboard file path: %w", err)
		}
		// Take the first file (split by newline if multiple)
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) > 0 {
			filePath = strings.TrimSpace(lines[0])
		}
	} else {
		// Linux: Try xclip or xsel
		// First try xclip
		cmd := exec.Command("xclip", "-selection", "clipboard", "-o")
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if err == nil {
			filePath = strings.TrimSpace(out.String())
		} else {
			// Try xsel as fallback
			cmd = exec.Command("xsel", "--clipboard", "--output")
			out.Reset()
			cmd.Stdout = &out
			err = cmd.Run()
			if err == nil {
				filePath = strings.TrimSpace(out.String())
			}
		}
	}

	// If no file found via OS
	if filePath == "" {
		return nil, fmt.Errorf("no file found in system clipboard")
	}

	// Read the actual file from disk
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Get file info
	stats, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}

	// Determine MIME type from file extension
	mimeType := "application/octet-stream"
	ext := filepath.Ext(filePath)
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".mp4":
		mimeType = "video/mp4"
	case ".mp3":
		mimeType = "audio/mpeg"
	case ".pdf":
		mimeType = "application/pdf"
	case ".xls", ".xlsx":
		mimeType = "application/vnd.ms-excel"
	}

	// Prepare response
	return &ClipboardFile{
		Filename: stats.Name(),
		Base64:   base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}, nil
}
