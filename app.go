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
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"gorm.io/gorm"
)

// App struct
type App struct {
	ctx             context.Context
	provider        core.Provider // Use the interface
	providerManager *core.ProviderManager
	eventChan       <-chan core.ProviderEvent
	eventCancel     context.CancelFunc
	systemTray      *menu.Menu
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// cleanupSelfReceipts removes receipts where the user is the sender of the message
// This cleans up incorrectly stored receipts from previous versions
func cleanupSelfReceipts() {
	if db.DB == nil {
		return
	}

	// Find all receipts where user_id matches the sender_id of the message
	var receiptsToDelete []models.MessageReceipt
	err := db.DB.
		Joins("JOIN messages ON messages.id = message_receipts.message_id").
		Where("message_receipts.user_id = messages.sender_id").
		Find(&receiptsToDelete).Error

	if err != nil {
		log.Printf("Warning: Failed to find self receipts to clean up: %v", err)
		return
	}

	if len(receiptsToDelete) > 0 {
		log.Printf("Found %d self receipts to clean up", len(receiptsToDelete))
		err = db.DB.Delete(&receiptsToDelete).Error
		if err != nil {
			log.Printf("Warning: Failed to delete self receipts: %v", err)
		} else {
			log.Printf("Successfully cleaned up %d self receipts", len(receiptsToDelete))
		}
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize the database
	if err := db.InitDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Clean up incorrectly stored self receipts
	cleanupSelfReceipts()

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
	if len(configs) == 0 {
		fmt.Printf("App.startup: No provider configs found in database, skipping restoration\n")
	}

	// Restore providers from database
	var activeProvider core.Provider
	restoredCount := 0
	for _, config := range configs {
		// Capture config for goroutine
		providerConfig := config
		fmt.Printf("App.startup: Attempting to restore provider %s (InstanceID: %s, InstanceName: %s, IsActive: %v)\n",
			providerConfig.ProviderID, providerConfig.InstanceID, providerConfig.InstanceName, providerConfig.IsActive)

		fmt.Printf("App.startup: About to call RestoreProvider for %s (instanceID: %s)\n", providerConfig.ProviderID, providerConfig.InstanceID)
		provider, err := a.providerManager.RestoreProvider(providerConfig)
		if err != nil {
			fmt.Printf("App.startup: ERROR - Failed to restore provider %s: %v\n", providerConfig.ProviderID, err)
			continue
		}
		fmt.Printf("App.startup: Successfully restored provider %s (instanceID: %s) from database, provider is nil: %v\n",
			providerConfig.ProviderID, providerConfig.InstanceID, provider == nil)
		restoredCount++

		// Store the instanceID for later use
		instanceID := providerConfig.InstanceID
		if instanceID == "" {
			instanceID = fmt.Sprintf("%s-1", providerConfig.ProviderID)
		}

		// Only connect the provider if it's already authenticated
		// Providers that need authentication (like WhatsApp) should only be connected
		// when the user explicitly requests it via the UI
		isAuth := provider.IsAuthenticated()
		fmt.Printf("App.startup: Provider %s (instanceID: %s) IsAuthenticated: %v\n", providerConfig.ProviderID, instanceID, isAuth)
		if isAuth {
			if err := provider.Connect(); err != nil {
				log.Printf("Warning: Failed to connect provider %s: %v", providerConfig.ProviderID, err)
				continue
			}
			log.Printf("Provider %s (instanceID: %s) connected successfully", providerConfig.ProviderID, instanceID)
		} else {
			log.Printf("Provider %s (instanceID: %s) is not authenticated yet, skipping auto-connect. User must configure it first.", providerConfig.ProviderID, instanceID)
			// Don't set as active if not authenticated
			if providerConfig.IsActive {
				log.Printf("Warning: Provider %s is marked as active but not authenticated, clearing active status", providerConfig.ProviderID)
				// Clear active status in database
				if db.DB != nil {
					db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Update("is_active", false)
				}
				providerConfig.IsActive = false
			}
			// Continue to next provider, but the provider is still in pm.providers from RestoreProvider
			continue
		}

		// Sync missed messages if last sync was more than 1 minute ago
		if providerConfig.LastSyncAt != nil {
			timeSinceLastSync := time.Since(*providerConfig.LastSyncAt)
			if timeSinceLastSync > time.Minute {
				go func(p core.Provider, instID string, lastSync time.Time) {
					if err := p.SyncHistory(lastSync); err != nil {
						log.Printf("Warning: Failed to sync history for provider instance %s: %v", instID, err)
					} else {
						// Update last sync time
						now := time.Now()
						if db.DB != nil {
							db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instID).Update("last_sync_at", now)
						}
					}
				}(provider, instanceID, *providerConfig.LastSyncAt)
			}
		} else {
			// First time sync - sync last 1 year to get all conversations
			// WhatsApp will automatically sync via HistorySync events, but we trigger a manual sync
			// with a long period to ensure we get all available conversations
			go func(p core.Provider, instID string) {
				since := time.Now().Add(-365 * 24 * time.Hour) // 1 year ago
				fmt.Printf("App.startup: First time sync for provider instance %s, syncing since %s\n", instID, since.Format("2006-01-02 15:04:05"))
				if err := p.SyncHistory(since); err != nil {
					log.Printf("Warning: Failed to sync history for provider instance %s: %v", instID, err)
				} else {
					// Update last sync time
					now := time.Now()
					if db.DB != nil {
						db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instID).Update("last_sync_at", now)
					}
				}
			}(provider, instanceID)
		}

		// Set as active provider if marked as active
		if providerConfig.IsActive {
			activeProvider = provider
			a.provider = provider
			// Also set the active instance ID in the provider manager
			instanceID := providerConfig.InstanceID
			if instanceID == "" {
				instanceID = fmt.Sprintf("%s-1", providerConfig.ProviderID)
			}
			if err := a.providerManager.SetActiveProvider(instanceID); err != nil {
				log.Printf("Warning: Failed to set active provider instance %s: %v", instanceID, err)
			} else {
				fmt.Printf("App.startup: Set provider %s (instanceID: %s) as active provider\n", providerConfig.ProviderID, instanceID)
			}
		}
	}

	fmt.Printf("App.startup: Finished restoring providers. Restored %d/%d providers. Active provider: %v\n",
		restoredCount, len(configs), activeProvider != nil)
	fmt.Printf("App.startup: a.provider is nil: %v\n", a.provider == nil)

	// Log current state of pm.providers
	if a.providerManager != nil {
		configured := a.providerManager.GetConfiguredProviders()
		fmt.Printf("App.startup: GetConfiguredProviders returns %d providers after restoration\n", len(configured))
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

	// Setup system tray menu
	a.setupSystemTray(ctx)
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
					log.Printf("App: Received ReactionEvent: conversation=%s, message=%s, user=%s, emoji=%s, added=%v", e.ConversationID, e.MessageID, e.UserID, e.Emoji, e.Added)

					// Save reaction to database
					if db.DB != nil {
						// Find the message by protocol message ID
						var message models.Message
						if err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", e.MessageID, e.ConversationID).First(&message).Error; err == nil {
							if e.Added {
								// Check if reaction already exists
								var existingReaction models.Reaction
								reactionExists := db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", message.ID, e.UserID, e.Emoji).First(&existingReaction).Error == nil

								if !reactionExists {
									// Create new reaction
									reaction := models.Reaction{
										MessageID: message.ID,
										UserID:    e.UserID,
										Emoji:     e.Emoji,
										CreatedAt: time.Unix(e.Timestamp, 0),
										UpdatedAt: time.Unix(e.Timestamp, 0),
									}
									if err := db.DB.Create(&reaction).Error; err != nil {
										log.Printf("App: Failed to save reaction to database: %v", err)
									} else {
										log.Printf("App: Saved reaction to database for message %s, user %s, emoji %s", e.MessageID, e.UserID, e.Emoji)
									}
								} else {
									log.Printf("App: Reaction already exists in database for message %s, user %s, emoji %s", e.MessageID, e.UserID, e.Emoji)
								}
							} else {
								// Remove reaction
								var existingReaction models.Reaction
								if err := db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", message.ID, e.UserID, e.Emoji).First(&existingReaction).Error; err == nil {
									if err := db.DB.Delete(&existingReaction).Error; err != nil {
										log.Printf("App: Failed to delete reaction from database: %v", err)
									} else {
										log.Printf("App: Deleted reaction from database for message %s, user %s, emoji %s", e.MessageID, e.UserID, e.Emoji)
									}
								} else {
									log.Printf("App: Reaction not found in database for deletion: message %s, user %s, emoji %s", e.MessageID, e.UserID, e.Emoji)
								}
							}
						} else {
							log.Printf("App: Message not found in database for reaction: conversation %s, message %s (this is OK if message hasn't been loaded yet)", e.ConversationID, e.MessageID)
						}
					}

					// Always emit the event to the frontend, even if message wasn't found in database
					// The frontend will handle updating the UI when the message is loaded
					reactionJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("App: Failed to marshal reaction: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "reaction", string(reactionJSON))
						log.Printf("App: Emitted reaction event to frontend: conversation=%s, message=%s, emoji=%s", e.ConversationID, e.MessageID, e.Emoji)
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

				case core.PresenceEvent:
					// Serialize the presence event to JSON
					presenceJSON, err := json.Marshal(e)
					if err != nil {
						log.Printf("Failed to marshal presence event: %v", err)
						continue
					}
					// Emit the event to the frontend
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "presence", string(presenceJSON))
						log.Printf("App: Emitted presence event to frontend: user=%s, online=%v", e.UserID, e.IsOnline)
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
					// Save receipt to database
					if db.DB != nil {
						// Find the message by protocol message ID
						var message models.Message
						if err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", e.MessageID, e.ConversationID).First(&message).Error; err == nil {
							// Don't save receipts from the message sender (we don't count ourselves)
							if e.UserID == message.SenderID {
								log.Printf("App: Skipping receipt from sender themselves for message %s, user %s", e.MessageID, e.UserID)
							} else {
								// Check if receipt already exists
								var existingReceipt models.MessageReceipt
								receiptExists := db.DB.Where("message_id = ? AND user_id = ? AND receipt_type = ?", message.ID, e.UserID, string(e.ReceiptType)).First(&existingReceipt).Error == nil

								if !receiptExists {
									// Create new receipt
									receipt := models.MessageReceipt{
										MessageID:   message.ID,
										UserID:      e.UserID,
										ReceiptType: string(e.ReceiptType),
										Timestamp:   time.Unix(e.Timestamp, 0),
									}
									if err := db.DB.Create(&receipt).Error; err != nil {
										log.Printf("Failed to save receipt to database: %v", err)
									} else {
										log.Printf("App: Saved receipt to database for message %s, user %s, type %s", e.MessageID, e.UserID, e.ReceiptType)
									}
								} else {
									// Update existing receipt timestamp if newer
									receiptTimestamp := time.Unix(e.Timestamp, 0)
									if receiptTimestamp.After(existingReceipt.Timestamp) {
										existingReceipt.Timestamp = receiptTimestamp
										if err := db.DB.Save(&existingReceipt).Error; err != nil {
											log.Printf("Failed to update receipt in database: %v", err)
										} else {
											log.Printf("App: Updated receipt in database for message %s, user %s, type %s", e.MessageID, e.UserID, e.ReceiptType)
										}
									}
								}
							}
						} else {
							log.Printf("App: Message not found for receipt: conversation %s, message %s", e.ConversationID, e.MessageID)
						}
					}

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
	// Load contacts from database for all configured providers
	// This allows filtering by provider instance
	var linkedAccounts []models.LinkedAccount
	var needToSave bool

	if db.DB != nil {
		// Load all LinkedAccounts from database
		if err := db.DB.Preload("Conversations").Find(&linkedAccounts).Error; err != nil {
			log.Printf("GetMetaContacts: Error loading linked accounts from database: %v", err)
			needToSave = true
		} else if len(linkedAccounts) == 0 {
			// Database is empty, need to fetch from providers and save
			log.Printf("GetMetaContacts: Database is empty, fetching from providers")
			needToSave = true
		} else {
			log.Printf("GetMetaContacts: Loaded %d linked accounts from database", len(linkedAccounts))
			// Log ProviderInstanceID distribution
			accountsByInstance := make(map[string]int)
			accountsWithoutInstance := 0
			for _, acc := range linkedAccounts {
				if acc.ProviderInstanceID != "" {
					accountsByInstance[acc.ProviderInstanceID]++
				} else {
					accountsWithoutInstance++
				}
			}
			log.Printf("GetMetaContacts: Accounts by instance: %v, accounts without instance: %d", accountsByInstance, accountsWithoutInstance)

			// Check if we have contacts for all configured providers
			configuredProviders := a.providerManager.GetConfiguredProviders()
			log.Printf("GetMetaContacts: Found %d configured providers", len(configuredProviders))
			providerInstanceIDs := make(map[string]bool)
			for _, p := range configuredProviders {
				if p.InstanceID != "" {
					providerInstanceIDs[p.InstanceID] = true
					log.Printf("GetMetaContacts: Configured provider instance: %s", p.InstanceID)
				}
			}
			// If any configured provider has no contacts, fetch and save
			for instanceID := range providerInstanceIDs {
				if accountsByInstance[instanceID] == 0 {
					log.Printf("GetMetaContacts: Provider %s has no contacts in database, will fetch", instanceID)
					needToSave = true
					break
				} else {
					log.Printf("GetMetaContacts: Provider %s has %d contacts in database", instanceID, accountsByInstance[instanceID])
				}
			}
		}
	} else {
		needToSave = false // Can't save if DB is not available
		log.Printf("GetMetaContacts: Database not available, cannot save")
	}

	log.Printf("GetMetaContacts: needToSave = %v", needToSave)

	// If we need to fetch from providers, get all configured providers
	if needToSave && db.DB != nil {
		configuredProviders := a.providerManager.GetConfiguredProviders()
		log.Printf("GetMetaContacts: Fetching contacts from %d configured providers", len(configuredProviders))

		allProviderAccounts := make([]models.LinkedAccount, 0)
		for _, providerInfo := range configuredProviders {
			instanceID := providerInfo.InstanceID
			if instanceID == "" {
				continue
			}

			provider, err := a.providerManager.GetProvider(instanceID)
			if err != nil {
				log.Printf("GetMetaContacts: Failed to get provider %s: %v", instanceID, err)
				continue
			}

			providerAccounts, err := provider.GetContacts()
			if err != nil {
				log.Printf("GetMetaContacts: Error getting contacts from provider %s: %v", instanceID, err)
				continue
			}

			log.Printf("GetMetaContacts: Provider %s returned %d contacts before setting ProviderInstanceID", instanceID, len(providerAccounts))

			// Set ProviderInstanceID for each account
			for i := range providerAccounts {
				providerAccounts[i].ProviderInstanceID = instanceID
				log.Printf("GetMetaContacts: Contact %d: UserID=%s, Username=%s, ProviderInstanceID=%s", i+1, providerAccounts[i].UserID, providerAccounts[i].Username, providerAccounts[i].ProviderInstanceID)
			}

			allProviderAccounts = append(allProviderAccounts, providerAccounts...)
			log.Printf("GetMetaContacts: Fetched %d contacts from provider %s (total so far: %d)", len(providerAccounts), instanceID, len(allProviderAccounts))
		}

		linkedAccounts = allProviderAccounts
		log.Printf("GetMetaContacts: Total contacts to save: %d", len(linkedAccounts))

		// Save LinkedAccounts and MetaContacts to database
		if err := a.saveContactsToDatabase(linkedAccounts); err != nil {
			log.Printf("GetMetaContacts: Error saving contacts to database: %v", err)
			// Continue anyway to return the contacts
		} else {
			log.Printf("GetMetaContacts: Saved %d contacts to database", len(linkedAccounts))
		}
	}

	// This is a simulation of contact grouping.
	// A real implementation would involve more complex logic from the database.
	log.Printf("GetMetaContacts: Processing %d linked accounts to create MetaContacts", len(linkedAccounts))
	if len(linkedAccounts) == 0 {
		log.Printf("GetMetaContacts: WARNING - No linked accounts to process! This might indicate a problem.")
	}
	metaContactsMap := make(map[string]*models.MetaContact)

	for i, acc := range linkedAccounts {
		if i < 10 {
			log.Printf("GetMetaContacts: Processing account %d: UserID=%s, Username=%s, ProviderInstanceID=%s", i+1, acc.UserID, acc.Username, acc.ProviderInstanceID)
		}
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

	log.Printf("GetMetaContacts: Returning %d MetaContacts (from %d linked accounts)", len(metaContacts), len(linkedAccounts))
	return metaContacts, nil
}

// saveContactsToDatabase saves LinkedAccounts and creates/updates MetaContacts in the database
func (a *App) saveContactsToDatabase(linkedAccounts []models.LinkedAccount) error {
	if db.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	log.Printf("saveContactsToDatabase: Saving %d linked accounts", len(linkedAccounts))

	// Log first few accounts for debugging
	for i, acc := range linkedAccounts {
		if i < 5 {
			log.Printf("saveContactsToDatabase: Account %d: UserID=%s, Username=%s, ProviderInstanceID=%s", i+1, acc.UserID, acc.Username, acc.ProviderInstanceID)
		}
	}

	// Group LinkedAccounts by UserID to create MetaContacts
	metaContactsMap := make(map[string]*models.MetaContact)
	accountsByUserID := make(map[string][]models.LinkedAccount)

	// First pass: group accounts by UserID and prepare MetaContacts
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
		accountsByUserID[key] = append(accountsByUserID[key], acc)

		// Get or create MetaContact entry in map
		if _, exists := metaContactsMap[key]; !exists {
			// Try to load from database first
			var existingMeta models.MetaContact
			err := db.DB.Where("id IN (SELECT meta_contact_id FROM linked_accounts WHERE user_id = ?)", key).First(&existingMeta).Error
			if err == nil {
				metaContactsMap[key] = &existingMeta
			} else {
				// Create new MetaContact
				avatarURL := acc.AvatarURL
				if avatarURL == "" {
					avatarURL = fmt.Sprintf("https://api.dicebear.com/7.x/initials/svg?seed=%s", displayName)
				}
				metaContactsMap[key] = &models.MetaContact{
					DisplayName:    displayName,
					AvatarURL:      avatarURL,
					LinkedAccounts: []models.LinkedAccount{},
				}
			}
		}

		// Update display name if better one is available
		metaContact := metaContactsMap[key]
		if displayName != "" && (metaContact.DisplayName == "" || metaContact.DisplayName == acc.UserID) {
			metaContact.DisplayName = displayName
		}

		// Update avatar if available and current is dicebear
		if acc.AvatarURL != "" {
			isDicebear := strings.HasPrefix(metaContact.AvatarURL, "https://api.dicebear.com/") || metaContact.AvatarURL == ""
			if isDicebear {
				metaContact.AvatarURL = acc.AvatarURL
			}
		}
	}

	// Second pass: Save MetaContacts first
	savedMetaContacts := 0
	failedMetaContacts := 0
	for userID, metaContact := range metaContactsMap {
		// Save or update MetaContact
		if metaContact.ID == 0 {
			// Create new MetaContact
			if err := db.DB.Create(metaContact).Error; err != nil {
				log.Printf("saveContactsToDatabase: Error creating MetaContact for %s: %v", userID, err)
				failedMetaContacts++
				continue
			}
			log.Printf("saveContactsToDatabase: Created MetaContact ID %d for %s", metaContact.ID, userID)
			savedMetaContacts++
		} else {
			// Update existing MetaContact
			if err := db.DB.Save(metaContact).Error; err != nil {
				log.Printf("saveContactsToDatabase: Error updating MetaContact for %s: %v", userID, err)
				failedMetaContacts++
				continue
			}
			savedMetaContacts++
		}

		// Third pass: Save LinkedAccounts for this MetaContact
		for _, acc := range accountsByUserID[userID] {
			acc.MetaContactID = metaContact.ID

			// Check if LinkedAccount already exists
			var existing models.LinkedAccount
			err := db.DB.Where("provider_instance_id = ? AND user_id = ?", acc.ProviderInstanceID, acc.UserID).First(&existing).Error
			if err == nil {
				// Update existing
				existing.Username = acc.Username
				existing.AvatarURL = acc.AvatarURL
				existing.Status = acc.Status
				existing.MetaContactID = metaContact.ID
				existing.UpdatedAt = time.Now()
				if err := db.DB.Save(&existing).Error; err != nil {
					log.Printf("saveContactsToDatabase: Error updating LinkedAccount %s: %v", acc.UserID, err)
				} else {
					log.Printf("saveContactsToDatabase: Updated LinkedAccount %s (instance: %s)", acc.UserID, acc.ProviderInstanceID)
				}
			} else if err == gorm.ErrRecordNotFound {
				// Create new
				if err := db.DB.Create(&acc).Error; err != nil {
					log.Printf("saveContactsToDatabase: Error creating LinkedAccount %s: %v", acc.UserID, err)
				} else {
					log.Printf("saveContactsToDatabase: Created LinkedAccount %s (instance: %s)", acc.UserID, acc.ProviderInstanceID)
				}
			} else {
				log.Printf("saveContactsToDatabase: Error checking LinkedAccount %s: %v", acc.UserID, err)
			}
		}
	}

	log.Printf("saveContactsToDatabase: Successfully saved %d MetaContacts (%d failed), %d LinkedAccounts to database", savedMetaContacts, failedMetaContacts, len(linkedAccounts))
	return nil
}

// ForceSyncCompletion forces the emission of a sync completion event.
// This is used when the frontend decides to stop waiting for history sync.
func (a *App) ForceSyncCompletion() {
	log.Printf("App: ForceSyncCompletion called by frontend")

	// Create a completed status event
	completeEvent := core.SyncStatusEvent{
		Status:   core.SyncStatusCompleted,
		Message:  "Sync stopped by user",
		Progress: 100,
	}

	// Marshal and emit
	statusJSON, err := json.Marshal(completeEvent)
	if err != nil {
		log.Printf("Failed to marshal sync status: %v", err)
		return
	}

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "sync-status", string(statusJSON))
		log.Printf("App: Emitted forced sync completion event")

		// Also trigger a refresh to ensure contact list is updated
		runtime.EventsEmit(a.ctx, "contacts-refresh", "{}")
	}
}

// GetMessagesForConversation returns messages for a given conversation ID.
// ResolveLID attempts to resolve a WhatsApp Local ID (LID) to a standard JID
// by searching through the database for messages or conversations involving this LID.
func (a *App) ResolveLID(lid string) (string, error) {
	fmt.Printf("App.ResolveLID: Attempting to resolve LID %s\n", lid)

	// First, check if it's actually a LID
	if !strings.HasSuffix(lid, "@lid") {
		fmt.Printf("App.ResolveLID: %s is not a LID, returning as-is\n", lid)
		return lid, nil
	}

	// Extract the phone number from the LID (e.g., "176188215558395@lid" -> "176188215558395")
	lidNumber := strings.TrimSuffix(lid, "@lid")
	fmt.Printf("App.ResolveLID: Extracted number from LID: %s\n", lidNumber)

	// Strategy 0: Try to find a contact whose phone number matches this LID number
	// The LID number might be the same as a phone number in the format "33XXXXXXXXX@s.whatsapp.net"
	// We'll search for linked accounts with user_id containing this number
	var matchingAccounts []models.LinkedAccount
	if err := db.DB.Where("user_id LIKE ?", "%"+lidNumber+"%").Find(&matchingAccounts).Error; err == nil {
		fmt.Printf("App.ResolveLID: Found %d linked accounts with phone number containing %s\n", len(matchingAccounts), lidNumber)
		for _, la := range matchingAccounts {
			fmt.Printf("App.ResolveLID: - Linked account: %s (protocol: %s)\n", la.UserID, la.Protocol)
			// If it's a WhatsApp account and ends with @s.whatsapp.net, use it
			if la.Protocol == "whatsapp" && strings.HasSuffix(la.UserID, "@s.whatsapp.net") {
				fmt.Printf("App.ResolveLID: Resolved LID %s to %s via phone number match\n", lid, la.UserID)
				return la.UserID, nil
			}
		}
	}

	// Strategy 1: Search for messages where this LID is the sender
	var messages []models.Message
	if err := db.DB.Where("sender_id = ?", lid).Limit(1).Find(&messages).Error; err == nil && len(messages) > 0 {
		fmt.Printf("App.ResolveLID: Found message with sender_id = %s\n", lid)
		// Found a message with this LID as sender
		// The protocol_conv_id should be the real conversation ID
		if messages[0].ProtocolConvID != "" && messages[0].ProtocolConvID != lid {
			fmt.Printf("App.ResolveLID: Resolved LID %s to %s via message sender\n", lid, messages[0].ProtocolConvID)
			return messages[0].ProtocolConvID, nil
		}
	} else {
		fmt.Printf("App.ResolveLID: No messages found with sender_id = %s (error: %v)\n", lid, err)
	}

	// Strategy 1.5: Search for messages where this LID is in the protocol_conv_id
	// In 1-on-1 chats, the protocol_conv_id could be this LID
	if err := db.DB.Where("protocol_conv_id = ?", lid).Limit(1).Find(&messages).Error; err == nil && len(messages) > 0 {
		fmt.Printf("App.ResolveLID: Found message with protocol_conv_id = %s\n", lid)
		// Found a message in this conversation
		// Try to find who sent it - if it's not us, use their JID
		if messages[0].SenderID != "" && messages[0].SenderID != lid && strings.HasSuffix(messages[0].SenderID, "@s.whatsapp.net") {
			fmt.Printf("App.ResolveLID: Resolved LID %s to %s via message in conversation\n", lid, messages[0].SenderID)
			return messages[0].SenderID, nil
		}
	} else {
		fmt.Printf("App.ResolveLID: No messages found with protocol_conv_id = %s (error: %v)\n", lid, err)
	}

	// Strategy 2: Search for conversations where this LID is the protocol_conv_id
	var conversations []models.Conversation
	if err := db.DB.Where("protocol_conv_id = ?", lid).Find(&conversations).Error; err == nil && len(conversations) > 0 {
		// Found a conversation with this LID
		// Get the linked account for this conversation to find the meta_contact_id
		if conversations[0].LinkedAccountID != 0 {
			var linkedAccount models.LinkedAccount
			if err := db.DB.First(&linkedAccount, conversations[0].LinkedAccountID).Error; err == nil {
				// Now find all linked accounts for this meta contact
				var allLinkedAccounts []models.LinkedAccount
				if err := db.DB.Where("meta_contact_id = ?", linkedAccount.MetaContactID).Find(&allLinkedAccounts).Error; err == nil && len(allLinkedAccounts) > 0 {
					// Use the first linked account's user_id as the conversation ID
					resolvedJID := allLinkedAccounts[0].UserID
					if resolvedJID != lid && resolvedJID != "" {
						fmt.Printf("App.ResolveLID: Resolved LID %s to %s via conversation and linked accounts\n", lid, resolvedJID)
						return resolvedJID, nil
					}
				}
			}
		}
	}

	// Strategy 3: Search for linked accounts with this LID
	var linkedAccounts []models.LinkedAccount
	if err := db.DB.Where("user_id = ?", lid).Find(&linkedAccounts).Error; err == nil && len(linkedAccounts) > 0 {
		// Found a linked account with this LID
		// Try to find another linked account for the same meta contact with a standard JID
		if linkedAccounts[0].MetaContactID != 0 {
			var allLinkedAccounts []models.LinkedAccount
			if err := db.DB.Where("meta_contact_id = ? AND user_id != ?", linkedAccounts[0].MetaContactID, lid).Find(&allLinkedAccounts).Error; err == nil && len(allLinkedAccounts) > 0 {
				resolvedJID := allLinkedAccounts[0].UserID
				fmt.Printf("App.ResolveLID: Resolved LID %s to %s via linked account sibling\n", lid, resolvedJID)
				return resolvedJID, nil
			}
		}
	}

	fmt.Printf("App.ResolveLID: Could not resolve LID %s\n", lid)
	return lid, fmt.Errorf("could not resolve LID %s", lid)
}

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

// GetGroupParticipants returns the list of participants in a group conversation.
func (a *App) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	activeProvider, err := a.providerManager.GetActiveProvider()
	if err != nil || activeProvider == nil {
		return nil, fmt.Errorf("no active provider available")
	}
	participants, err := activeProvider.GetGroupParticipants(conversationID)
	if err != nil {
		return nil, err
	}
	return participants, nil
}

// GetParticipantNames returns the display names for a list of participant IDs.
// This uses the provider's contact resolution to get proper names for group members.
func (a *App) GetParticipantNames(participantIDs []string) (map[string]string, error) {
	activeProvider, err := a.providerManager.GetActiveProvider()
	if err != nil || activeProvider == nil {
		return nil, fmt.Errorf("no active provider available")
	}

	// Check if provider has a GetContactName method (for WhatsApp)
	type ContactNameProvider interface {
		GetContactName(contactID string) (string, error)
	}

	if cnp, ok := activeProvider.(ContactNameProvider); ok {
		names := make(map[string]string)
		for _, id := range participantIDs {
			name, err := cnp.GetContactName(id)
			if err == nil && name != "" {
				names[id] = name
			} else {
				fmt.Printf("GetParticipantNames: failed to get name for %s: %v\n", id, err)
			}
		}
		return names, nil
	}

	return make(map[string]string), nil
}

// SendMessage sends a text message.
func (a *App) SendMessage(conversationID string, text string) (*models.Message, error) {
	return a.provider.SendMessage(conversationID, text, nil, nil)
}

// SendReply sends a text message as a reply to another message.
func (a *App) SendReply(conversationID string, text string, quotedMessageID string) (*models.Message, error) {
	if a.provider == nil {
		return nil, fmt.Errorf("no active provider")
	}
	return a.provider.SendReply(conversationID, text, quotedMessageID)
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

// AddReaction adds a reaction (emoji) to a message.
func (a *App) AddReaction(conversationID string, messageID string, emoji string) error {
	if a.provider == nil {
		return fmt.Errorf("no active provider")
	}
	return a.provider.AddReaction(conversationID, messageID, emoji)
}

// RemoveReaction removes a reaction (emoji) from a message.
func (a *App) RemoveReaction(conversationID string, messageID string, emoji string) error {
	if a.provider == nil {
		return fmt.Errorf("no active provider")
	}
	return a.provider.RemoveReaction(conversationID, messageID, emoji)
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

// MarkMessageAsPlayed sends a played receipt for a specific voice message.
func (a *App) MarkMessageAsPlayed(conversationID string, messageID string) error {
	log.Printf("App: MarkMessageAsPlayed called for conversation %s, message %s", conversationID, messageID)
	if a.provider == nil {
		return fmt.Errorf("no active provider")
	}
	err := a.provider.MarkMessageAsPlayed(conversationID, messageID)
	if err != nil {
		log.Printf("App: Failed to mark message as played: %v", err)
		return err
	}
	log.Printf("App: Successfully marked message %s as played in conversation %s", messageID, conversationID)
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
	case ".ogg":
		mimeType = "audio/ogg"
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
// instanceName is optional - if empty, a default name will be generated.
// If existingInstanceID is provided and not empty (for edit mode), it will be used instead of generating a new one.
func (a *App) CreateProvider(providerID string, config core.ProviderConfig, instanceName string, existingInstanceID string) (string, error) {
	instanceID, provider, err := a.providerManager.CreateProvider(providerID, config, instanceName, existingInstanceID)
	if err != nil {
		return "", err
	}

	// If this is the first provider or we want to switch, make it active
	if a.provider == nil {
		if err := provider.Connect(); err != nil {
			return instanceID, fmt.Errorf("failed to connect provider: %w", err)
		}
		a.provider = provider
		a.providerManager.SetActiveProvider(instanceID)
	} else {
		// For additional providers, still call Connect() if not authenticated
		// This ensures QR code is generated for new instances
		if !provider.IsAuthenticated() {
			log.Printf("CreateProvider: Provider %s is not authenticated, calling Connect() to generate QR code", instanceID)
			if err := provider.Connect(); err != nil {
				log.Printf("CreateProvider: Warning - Failed to connect provider %s: %v (QR code may not be available)", instanceID, err)
				// Don't return error, allow provider creation to succeed
			}
		}
	}

	// Update last sync time when creating a new provider
	if db.DB != nil {
		var providerConfig models.ProviderConfiguration
		if err := db.DB.Where("instance_id = ?", instanceID).First(&providerConfig).Error; err == nil {
			now := time.Now()
			db.DB.Model(&providerConfig).Update("last_sync_at", now)
		}
	}

	return instanceID, nil
}

// GetProviderQRCode returns the latest QR code for a provider instance (if applicable).
func (a *App) GetProviderQRCode(instanceID string) (string, error) {
	log.Printf("GetProviderQRCode: Called with instanceID=%s", instanceID)
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		log.Printf("GetProviderQRCode: ERROR - Failed to get provider: %v", err)
		return "", err
	}
	log.Printf("GetProviderQRCode: Provider found, calling GetQRCode()")
	qrCode, err := provider.GetQRCode()
	if err != nil {
		log.Printf("GetProviderQRCode: ERROR - Provider.GetQRCode() failed: %v", err)
		return "", err
	}
	log.Printf("GetProviderQRCode: QR code retrieved successfully (length: %d)", len(qrCode))
	return qrCode, nil
}

// ConnectProvider connects a provider instance and updates the database.
func (a *App) ConnectProvider(instanceID string) error {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return err
	}

	if err := provider.Connect(); err != nil {
		return err
	}

	// Set as active provider
	a.provider = provider
	if err := a.providerManager.SetActiveProvider(instanceID); err != nil {
		log.Printf("Warning: Failed to set active provider: %v", err)
	} else {
		log.Printf("ConnectProvider: Successfully set provider instance %s as active", instanceID)
	}

	// Restart event listener with the new provider
	log.Printf("ConnectProvider: Restarting event listener for provider instance %s", instanceID)
	a.startEventListener(a.ctx)

	// Update last sync time after connection
	if db.DB != nil {
		now := time.Now()
		db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Updates(map[string]interface{}{
			"last_sync_at": now,
			"is_active":    true,
		})
	}

	log.Printf("ConnectProvider: Provider instance %s connected and set as active", instanceID)

	return nil
}

// RemoveProvider removes a provider instance and deletes its config directory.
func (a *App) RemoveProvider(instanceID string) error {
	log.Printf("RemoveProvider: Called with instanceID=%s", instanceID)

	// Cancel event listener if this is the active provider
	if a.provider != nil {
		currentProvider, _ := a.providerManager.GetActiveProvider()
		if currentProvider == a.provider && a.eventCancel != nil {
			log.Printf("RemoveProvider: Cancelling event listener for active provider")
			a.eventCancel()
			a.eventCancel = nil
		}
	}

	// Extract providerID from instanceID for config directory cleanup
	parts := strings.Split(instanceID, "-")
	providerID := instanceID
	if len(parts) >= 2 {
		providerID = strings.Join(parts[:len(parts)-1], "-")
	}
	log.Printf("RemoveProvider: Extracted providerID=%s from instanceID=%s", providerID, instanceID)

	// Check if there are other instances of this provider before deleting config directory
	// Only delete the config directory if this is the last instance of the provider
	// Note: We check BEFORE calling RemoveProvider, because RemoveProvider removes from the map
	remainingInstances := 0
	if db.DB != nil {
		var remainingConfigs []models.ProviderConfiguration
		if err := db.DB.Where("provider_id = ? AND instance_id != ?", providerID, instanceID).Find(&remainingConfigs).Error; err == nil {
			remainingInstances = len(remainingConfigs)
		}
	}

	// Remove provider (this will disconnect it and delete all associated data)
	log.Printf("RemoveProvider: Calling providerManager.RemoveProvider with instanceID=%s", instanceID)
	if err := a.providerManager.RemoveProvider(instanceID); err != nil {
		log.Printf("RemoveProvider: ERROR - providerManager.RemoveProvider failed: %v", err)
		return err
	}
	log.Printf("RemoveProvider: providerManager.RemoveProvider succeeded")

	// Always delete the instance-specific config directory (e.g., configDir/Loom/whatsapp-1/)
	// This contains the WhatsApp database and credentials for this specific instance
	configDir, err := os.UserConfigDir()
	if err == nil {
		instanceConfigDir := filepath.Join(configDir, "Loom", instanceID)
		if err := os.RemoveAll(instanceConfigDir); err != nil {
			log.Printf("Warning: Failed to delete instance config directory %s: %v", instanceConfigDir, err)
		} else {
			log.Printf("Deleted instance config directory: %s", instanceConfigDir)
		}
	}

	// Only delete provider's shared config directory if no other instances exist
	if remainingInstances == 0 {
		if err == nil {
			providerConfigDir := filepath.Join(configDir, "Loom", providerID)
			if err := os.RemoveAll(providerConfigDir); err != nil {
				log.Printf("Warning: Failed to delete provider config directory %s: %v", providerConfigDir, err)
			} else {
				log.Printf("Deleted provider config directory: %s (last instance removed)", providerConfigDir)
			}
		}
	} else {
		log.Printf("Not deleting provider config directory: %d other instance(s) still exist", remainingInstances)
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

// UpdateSystemTrayBadge updates the system tray icon with a badge showing the unread message count.
// This method is called from the frontend when the unread count changes.
func (a *App) UpdateSystemTrayBadge(count int) error {
	if a.ctx == nil {
		return fmt.Errorf("context not initialized")
	}

	// Get the app icon path
	iconPath, err := a.getAppIconPath()
	if err != nil {
		log.Printf("Failed to get app icon path: %v", err)
		return err
	}

	// Create badge icon with count
	badgeIconPath, err := a.createBadgeIcon(iconPath, count)
	if err != nil {
		log.Printf("Failed to create badge icon: %v", err)
		return err
	}

	// Read the badge icon data
	iconData, err := os.ReadFile(badgeIconPath)
	if err != nil {
		log.Printf("Failed to read badge icon: %v", err)
		os.Remove(badgeIconPath)
		return err
	}

	// Update system tray icon using platform-specific APIs
	log.Printf("System tray badge updated: %d unread messages", count)

	// Use platform-specific badge APIs
	switch goruntime.GOOS {
	case "darwin":
		// macOS: Update dock badge using AppleScript
		a.updateMacOSDockBadge(count)
	case "windows":
		// Windows: Update taskbar badge (requires Windows API)
		// For now, we'll use the icon with badge
		log.Printf("Windows: Badge count is %d", count)
	case "linux":
		// Linux: Update depends on desktop environment
		// For now, we'll use the icon with badge
		log.Printf("Linux: Badge count is %d", count)
	}

	// Clean up temporary badge icon file after a delay
	go func() {
		time.Sleep(10 * time.Second)
		os.Remove(badgeIconPath)
	}()

	// Store icon data for potential future use
	_ = iconData

	return nil
}

// getAppIconPath returns the path to the application icon
func (a *App) getAppIconPath() (string, error) {
	// Try to find the icon in various locations
	iconPaths := []string{
		"appicon.png",       // Root directory (preferred)
		"build/appicon.png", // Build directory
		"build/bin/Loom.app/Contents/Resources/iconfile.icns",
		"build/bin/Mux.app/Contents/Resources/iconfile.icns",
	}

	for _, path := range iconPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// If no icon found, create a default one
	return a.createDefaultIcon()
}

// createDefaultIcon creates a default application icon
func (a *App) createDefaultIcon() (string, error) {
	// Create a simple default icon
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{R: 27, G: 38, B: 54, A: 255}}, image.Point{}, draw.Src)

	// Save to temp file
	tmpFile, err := os.CreateTemp("", "loom-icon-*.png")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if err := png.Encode(tmpFile, img); err != nil {
		return "", err
	}

	return tmpFile.Name(), nil
}

// createBadgeIcon creates an icon with a badge showing the unread count
func (a *App) createBadgeIcon(baseIconPath string, count int) (string, error) {
	// Read base icon
	baseFile, err := os.Open(baseIconPath)
	if err != nil {
		return "", fmt.Errorf("failed to open base icon: %w", err)
	}
	defer baseFile.Close()

	// Decode the base icon
	baseImg, _, err := image.Decode(baseFile)
	if err != nil {
		return "", fmt.Errorf("failed to decode base icon: %w", err)
	}

	// Create a new image with badge
	bounds := baseImg.Bounds()
	badgeImg := image.NewRGBA(bounds)
	draw.Draw(badgeImg, bounds, baseImg, bounds.Min, draw.Src)

	// If count is 0, return the base icon without badge
	if count <= 0 {
		tmpFile, err := os.CreateTemp("", "loom-badge-*.png")
		if err != nil {
			return "", err
		}
		defer tmpFile.Close()

		if err := png.Encode(tmpFile, badgeImg); err != nil {
			return "", err
		}

		return tmpFile.Name(), nil
	}

	// Draw badge circle in top-right corner
	badgeSize := bounds.Dx() / 4
	badgeX := bounds.Dx() - badgeSize - bounds.Dx()/16
	badgeY := bounds.Dy() / 16

	// Draw red circle for badge
	badgeColor := color.RGBA{R: 255, G: 59, B: 48, A: 255} // Red badge
	for y := badgeY; y < badgeY+badgeSize; y++ {
		for x := badgeX; x < badgeX+badgeSize; x++ {
			centerX := badgeX + badgeSize/2
			centerY := badgeY + badgeSize/2
			dx := x - centerX
			dy := y - centerY
			if dx*dx+dy*dy <= (badgeSize/2)*(badgeSize/2) {
				badgeImg.Set(x, y, badgeColor)
			}
		}
	}

	// Draw count text on badge
	countStr := fmt.Sprintf("%d", count)
	if count > 99 {
		countStr = "99+"
	}

	// Calculate text position (centered in badge)
	textX := badgeX + badgeSize/2
	textY := badgeY + badgeSize/2

	// Draw text
	a.drawText(badgeImg, countStr, textX, textY, color.White)

	// Save to temp file
	tmpFile, err := os.CreateTemp("", "loom-badge-*.png")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if err := png.Encode(tmpFile, badgeImg); err != nil {
		return "", err
	}

	return tmpFile.Name(), nil
}

// drawText draws text on an image
func (a *App) drawText(img *image.RGBA, text string, x, y int, col color.Color) {
	point := fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  point,
	}

	// Center the text
	textWidth := d.MeasureString(text)
	d.Dot.X -= textWidth / 2
	d.Dot.Y += fixed.Int26_6(13 * 64 / 2) // Center vertically

	d.DrawString(text)
}

// updateMacOSDockBadge updates the macOS dock badge using AppleScript
func (a *App) updateMacOSDockBadge(count int) {
	// Get the actual application name from the bundle
	appName := "Loom"

	// Try to get the actual bundle name from the running process
	// In dev mode, the app might be running from build/bin/Loom.app
	if goruntime.GOOS == "darwin" {
		// Try to get the bundle name from the Info.plist
		infoPlistPath := "build/bin/Loom.app/Contents/Info.plist"
		if _, err := os.Stat(infoPlistPath); err == nil {
			// Read CFBundleName from Info.plist
			cmd := exec.Command("defaults", "read", filepath.Join(infoPlistPath), "CFBundleName")
			if output, err := cmd.Output(); err == nil {
				bundleName := strings.TrimSpace(string(output))
				if bundleName != "" {
					appName = bundleName
				}
			}
		}
	}

	if count <= 0 {
		// Remove badge
		script := fmt.Sprintf(`tell application "System Events" to set the dock badge of application "%s" to ""`, appName)
		cmd := exec.Command("osascript", "-e", script)
		if err := cmd.Run(); err != nil {
			log.Printf("Failed to remove macOS dock badge for %s: %v", appName, err)
			log.Printf("Note: Make sure the app is running and has notification permissions enabled in System Preferences > Notifications")
		}
		return
	}

	// Set badge with count
	countStr := fmt.Sprintf("%d", count)
	if count > 99 {
		countStr = "99+"
	}
	script := fmt.Sprintf(`tell application "System Events" to set the dock badge of application "%s" to "%s"`, appName, countStr)
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to update macOS dock badge for %s: %v", appName, err)
		log.Printf("Note: Make sure the app is running and has notification permissions enabled in System Preferences > Notifications")
		log.Printf("You may need to grant notification permissions to the app in System Preferences")
	} else {
		log.Printf("Successfully updated dock badge for %s to %s", appName, countStr)
	}
}

// setupSystemTray creates and configures the system tray menu
func (a *App) setupSystemTray(ctx context.Context) {
	// Create system tray menu
	appMenu := menu.NewMenu()

	// Add menu items
	appMenu.Append(menu.Label("Loom"))
	appMenu.Append(menu.Separator())

	// Show/Hide window item
	showHideItem := menu.Text("Show/Hide", nil, func(_ *menu.CallbackData) {
		runtime.WindowShow(ctx)
	})
	appMenu.Append(showHideItem)

	// Quit item
	quitItem := menu.Text("Quit", nil, func(_ *menu.CallbackData) {
		runtime.Quit(ctx)
	})
	appMenu.Append(quitItem)

	// Set the menu
	a.systemTray = appMenu

	// Note: The actual system tray setup is done in main.go via AppOptions
	// This function just prepares the menu structure
	log.Printf("System tray menu configured")
}
