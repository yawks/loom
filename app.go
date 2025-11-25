// Package main is the entry point for the Loom chat application.
package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
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
		log.Printf("Warning: Failed to load provider configs: %v", err)
		configs = []models.ProviderConfiguration{}
	}

	// Restore providers from database
	var activeProvider core.Provider
	for _, config := range configs {
		// Capture config for goroutine
		providerConfig := config

		provider, err := a.providerManager.RestoreProvider(providerConfig)
		if err != nil {
			log.Printf("Warning: Failed to restore provider %s: %v", providerConfig.ProviderID, err)
			continue
		}

		// Only connect the provider if it's already authenticated
		// Providers that need authentication (like WhatsApp) should only be connected
		// when the user explicitly requests it via the UI
		if provider.IsAuthenticated() {
			if err := provider.Connect(); err != nil {
				log.Printf("Warning: Failed to connect provider %s: %v", providerConfig.ProviderID, err)
				continue
			}
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
			// First time sync - sync last 24 hours
			go func(p core.Provider, providerID string) {
				since := time.Now().Add(-24 * time.Hour)
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
		}
	}

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
	a.startEventListener(ctx)
}

// startEventListener starts listening to provider events and sends them to the frontend.
func (a *App) startEventListener(ctx context.Context) {
	// Cancel previous listener if any
	if a.eventCancel != nil {
		a.eventCancel()
	}

	// Check if provider is initialized
	if a.provider == nil {
		log.Printf("Warning: No provider available, skipping event stream setup")
		return
	}

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
					// Serialize the message to JSON
					msgJSON, err := json.Marshal(e.Message)
					if err != nil {
						log.Printf("Failed to marshal message: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "new-message", string(msgJSON))

				case core.ReactionEvent:
					// Serialize the reaction to JSON
					reactionJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal reaction: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "reaction", string(reactionJSON))

				case core.TypingEvent:
					// Serialize the typing indicator to JSON
					typingJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal typing indicator: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "typing", string(typingJSON))

				case core.ContactStatusEvent:
					// Serialize the contact status to JSON
					statusJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal contact status: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "contact-status", string(statusJSON))
					// If this is a refresh event, also invalidate the contacts query
					if e.UserID == "refresh" && e.Status == "sync_complete" {
						runtime.EventsEmit(ctx, "contacts-refresh", "{}")
					}

				case core.GroupChangeEvent:
					// Serialize the group change to JSON
					groupChangeJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal group change: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "group-change", string(groupChangeJSON))

				case core.ReceiptEvent:
					// Serialize the receipt to JSON
					receiptJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal receipt: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "receipt", string(receiptJSON))

				case core.RetryReceiptEvent:
					// Serialize the retry receipt to JSON
					retryReceiptJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal retry receipt: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "retry-receipt", string(retryReceiptJSON))
				case core.SyncStatusEvent:
					// Serialize the sync status to JSON
					syncStatusJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal sync status: %v", err)
						continue
					}
					// Emit the event to the frontend
					runtime.EventsEmit(ctx, "sync-status", string(syncStatusJSON))
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
		displayName := acc.Username
		if _, exists := metaContactsMap[displayName]; !exists {
			metaContactsMap[displayName] = &models.MetaContact{
				ID:             uint(len(metaContactsMap) + 1),
				DisplayName:    displayName,
				AvatarURL:      fmt.Sprintf("https://api.dicebear.com/7.x/initials/svg?seed=%s", displayName),
				LinkedAccounts: []models.LinkedAccount{},
			}
		}

		meta := metaContactsMap[displayName]
		meta.LinkedAccounts = append(meta.LinkedAccounts, acc)

		if acc.CreatedAt.IsZero() == false {
			if meta.CreatedAt.IsZero() || acc.CreatedAt.Before(meta.CreatedAt) {
				meta.CreatedAt = acc.CreatedAt
			}
		}

		candidateUpdated := acc.UpdatedAt
		if candidateUpdated.IsZero() {
			candidateUpdated = acc.CreatedAt
		}
		if candidateUpdated.IsZero() == false {
			if meta.UpdatedAt.IsZero() || candidateUpdated.After(meta.UpdatedAt) {
				meta.UpdatedAt = candidateUpdated
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
	// Use the provider's GetConversationHistory method
	// Limit to 100 messages by default
	activeProvider, err := a.providerManager.GetActiveProvider()
	if err != nil || activeProvider == nil {
		return nil, fmt.Errorf("no active provider available")
	}
	return activeProvider.GetConversationHistory(conversationID, 100)
}

// SendMessage sends a text message.
func (a *App) SendMessage(conversationID string, text string) (*models.Message, error) {
	return a.provider.SendMessage(conversationID, text, nil, nil)
}

// GetThreads returns all messages in a thread for a given parent message ID.
func (a *App) GetThreads(parentMessageID string) ([]models.Message, error) {
	return a.provider.GetThreads(parentMessageID)
}

// --- Provider Management Methods ---

// GetAvailableProviders returns a list of all available providers.
func (a *App) GetAvailableProviders() ([]core.ProviderInfo, error) {
	return a.providerManager.GetAvailableProviders(), nil
}

// GetConfiguredProviders returns a list of configured providers.
func (a *App) GetConfiguredProviders() ([]core.ProviderInfo, error) {
	return a.providerManager.GetConfiguredProviders(), nil
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

// RemoveProvider removes a provider.
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

	// Sync last 24 hours
	since := time.Now().Add(-24 * time.Hour)
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
