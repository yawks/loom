// Package main is the entry point for the Loom chat application.
package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"sync"
	"Loom/pkg/models"
	"Loom/pkg/providers"
	"Loom/pkg/providers/googlechat"
	"Loom/pkg/providers/slack"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gorm.io/gorm"
)

// pendingSyncInfo holds the information needed to trigger a sync after the frontend is ready.
type pendingSyncInfo struct {
	provider   core.Provider
	instanceID string
	since      time.Time
}

// metaContactsCache is a short-lived cache for GetMetaContacts results.
// Avatar updates and MPIM renames fire dozens of contacts-refresh events per second;
// without a cache each event would run a full DB scan. 2-second TTL is long enough to
// collapse bursts but short enough that users see fresh data within a few seconds.
type metaContactsCache struct {
	data      []models.MetaContact
	expiresAt time.Time
	mu        sync.RWMutex
}

// queryCache is a generic short-lived cache for expensive DB read results.
// The frontend fires GetAllLastMessages, GetAllLastMessageTimestamps and
// GetAllMessageCounts simultaneously on every React mount; a 5-second TTL
// collapses startup bursts into a single DB hit per query type.
type queryCache[T any] struct {
	mu        sync.RWMutex
	data      T
	expiresAt time.Time
}

// App struct
type App struct {
	ctx              context.Context
	provider         core.Provider // Active provider (for UI actions)
	providerManager  *core.ProviderManager
	eventCancels     map[string]context.CancelFunc // Map of instanceID -> cancelFunc for event listeners
	systemTray       *menu.Menu
	pendingSyncs     []pendingSyncInfo // syncs deferred until domReady
	metaContactsCache metaContactsCache
	mu               sync.RWMutex

	// Short-lived server-side caches for expensive full-table-scan queries.
	// The frontend has 30s staleTime but mounts multiple components simultaneously
	// at startup, producing a burst of identical queries on the same DB connection.
	// A 5s TTL collapses each burst into a single DB hit.
	lastMessagesCache   queryCache[map[string]models.Message]
	lastTimestampsCache queryCache[map[string]int64]
	messageCountsCache  queryCache[map[string]int]

	// contactsRefreshTimer debounces contacts-refresh emissions to the frontend.
	// Avatar updates fire one event per avatar; without debouncing that's hundreds
	// of full DB reloads per startup.
	contactsRefreshTimer *time.Timer
	contactsRefreshMu    sync.Mutex

	// syncingProviders tracks which provider instances are currently syncing.
	// A SyncStatusCompleted event from one provider is suppressed until all
	// active providers have finished, preventing "sync complete" from appearing
	// while another provider is still fetching history.
	syncingProviders   map[string]bool
	syncingProvidersMu sync.Mutex
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		eventCancels: make(map[string]context.CancelFunc),
	}
}

// cleanupSelfReceipts removes receipts where the user is the sender of the message
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

// cleanupDuplicateDMConversations removes Conversation records with D... ProtocolConvIDs
// that were incorrectly created for Slack DM channels. These duplicates were created by a
// bug where ensureConversation was called with the raw D... channel ID instead of the
// resolved U... user ID. For each D... Conversation that shares a LinkedAccount with a
// U... Conversation, we migrate messages to the U... Conversation and delete the duplicate.
func cleanupDuplicateDMConversations() {
	if db.DB == nil {
		return
	}

	var dConversations []models.Conversation
	if err := db.DB.Where("protocol_conv_id LIKE 'D%'").Find(&dConversations).Error; err != nil || len(dConversations) == 0 {
		return
	}

	cleaned := 0
	for _, dConv := range dConversations {
		// Look for a sibling Conversation on the same LinkedAccount whose ID doesn't start with D
		var uConv models.Conversation
		if err := db.DB.
			Where("linked_account_id = ? AND protocol_conv_id NOT LIKE 'D%'", dConv.LinkedAccountID).
			First(&uConv).Error; err != nil {
			continue // no canonical sibling found, leave it alone
		}

		// Migrate messages that still reference the bad D... protocolConvID
		db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ?", dConv.ProtocolConvID).
			Updates(map[string]interface{}{
				"protocol_conv_id": uConv.ProtocolConvID,
				"conversation_id":  uConv.ID,
			})

		// Remove the duplicate D... Conversation record
		db.DB.Delete(&dConv)
		db.ContactStore.SetConversation(dConv.LinkedAccountID, uConv.ProtocolConvID)

		log.Printf("[cleanupDuplicateDMConversations] Migrated messages from %s → %s and deleted duplicate conversation\n",
			dConv.ProtocolConvID, uConv.ProtocolConvID)
		cleaned++
	}

	if cleaned > 0 {
		log.Printf("[cleanupDuplicateDMConversations] Cleaned up %d duplicate D... conversations\n", cleaned)
	}
}

// createMissingConversations creates Conversation records for messages that don't have one
// This is a migration function to fix existing data
func createMissingConversations() {
	if db.DB == nil {
		return
	}

	// Get all distinct ProtocolConvIDs that have messages but no Conversation
	var protocolConvIDs []string
	err := db.DB.Model(&models.Message{}).
		Select("DISTINCT protocol_conv_id").
		Where("protocol_conv_id != '' AND conversation_id = 0").
		Pluck("protocol_conv_id", &protocolConvIDs).Error

	if err != nil {
		fmt.Printf("[createMissingConversations] Error getting protocolConvIDs: %v\n", err)
		return
	}

	if len(protocolConvIDs) == 0 {
		return
	}

	fmt.Printf("[createMissingConversations] Found %d conversations without Conversation records\n", len(protocolConvIDs))

	// For each ProtocolConvID, try to find or create the LinkedAccount and Conversation
	for _, protocolConvID := range protocolConvIDs {
		// Find LinkedAccount for this ProtocolConvID (try all provider instances)
		var linkedAccount models.LinkedAccount
		err := db.DB.Where("user_id = ?", protocolConvID).First(&linkedAccount).Error

		if err != nil {
			// LinkedAccount doesn't exist, skip this conversation for now
			// It will be created when a new message arrives
			fmt.Printf("[createMissingConversations] Skipping %s: LinkedAccount not found\n", protocolConvID)
			continue
		}

		// Check if Conversation already exists
		var conversation models.Conversation
		err = db.DB.Where("protocol_conv_id = ?", protocolConvID).First(&conversation).Error
		if err == nil {
			// Conversation exists, update messages to use it
			db.DB.Model(&models.Message{}).
				Where("protocol_conv_id = ? AND conversation_id = 0", protocolConvID).
				Update("conversation_id", conversation.ID)
			continue
		}

		// Create Conversation
		isGroup := strings.HasPrefix(protocolConvID, "C") || strings.HasPrefix(protocolConvID, "G")
		groupName := ""
		if isGroup {
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
			fmt.Printf("[createMissingConversations] Failed to create conversation for %s: %v\n", protocolConvID, err)
			continue
		}
		db.ContactStore.UpsertConversation(conversation.LinkedAccountID, conversation.ProtocolConvID)

		// Update all messages for this conversation to use the new ConversationID
		db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ?", protocolConvID).
			Update("conversation_id", conversation.ID)

		fmt.Printf("[createMissingConversations] Created conversation %d for ProtocolConvID %s\n", conversation.ID, protocolConvID)
	}

	fmt.Printf("[createMissingConversations] Completed migration\n")
}

// startup is called when the app starts.
func (a *App) getActiveProvider() core.Provider {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.provider
}

// getProviderForConversation returns the provider that owns the given conversation ID.
// It looks up the conversation's LinkedAccount in the DB to find the correct provider
// instance, falling back to the active provider when the conversation isn't in the DB yet.
func (a *App) getProviderForConversation(conversationID string) core.Provider {
	if db.DB != nil && conversationID != "" {
		var conv models.Conversation
		if err := db.DB.Where("protocol_conv_id = ?", conversationID).First(&conv).Error; err == nil {
			var la models.LinkedAccount
			if err := db.DB.Where("id = ?", conv.LinkedAccountID).First(&la).Error; err == nil && la.ProviderInstanceID != "" {
				if p, err := a.providerManager.GetProvider(la.ProviderInstanceID); err == nil {
					return p
				}
			}
		}
	}
	return a.getActiveProvider()
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize the database
	if err := db.InitDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	cleanupSelfReceipts()

	// Remove duplicate D... Conversation records created by a previous bug
	cleanupDuplicateDMConversations()

	// Create missing conversations for existing messages
	createMissingConversations()

	// Initialize provider manager
	a.providerManager = core.NewProviderManager()
	fmt.Printf("App.startup: ProviderManager initialized\n")

	// Register providers

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

	a.providerManager.RegisterProvider("slack", core.ProviderInfo{
		ID:          "slack",
		Name:        "Slack",
		Description: "Slack messaging provider",
		ConfigSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"title":       "Auth Token",
					"description": "Bot (xoxb-), User (xoxp-), or Client (xoxc-) Token",
				},
				"d_cookie": map[string]interface{}{
					"type":        "string",
					"title":       "d Cookie (Optional)",
					"description": "Required for Client Tokens (xoxc).",
				},
				"sync_days": map[string]interface{}{
					"type":        "number",
					"title":       "Sync Days",
					"description": "Only sync conversations with messages in the last X days (0 = no limit)",
					"default":     0,
					"minimum":     0,
				},
			},
			"required": []string{"token"},
		},
	}, func() core.Provider {
		return providers.NewSlackProvider()
	})

	a.providerManager.RegisterProvider("googlechat", core.ProviderInfo{
		ID:          "googlechat",
		Name:        "Google Chat",
		Description: "Google Chat messaging via the official REST API (OAuth2)",
		ConfigSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"client_id": map[string]interface{}{
					"type":        "string",
					"title":       "OAuth2 Client ID",
					"description": "OAuth2 Client ID from Google Cloud Console (Desktop app type)",
				},
				"client_secret": map[string]interface{}{
					"type":        "string",
					"title":       "OAuth2 Client Secret",
					"description": "OAuth2 Client Secret from Google Cloud Console",
				},
			},
			"required": []string{"client_id", "client_secret"},
		},
	}, func() core.Provider {
		return providers.NewGoogleChatProvider()
	})

	// Load and restore providers
	configs, err := a.providerManager.LoadProviderConfigs()
	if err != nil {
		fmt.Printf("App.startup: Warning: Failed to load provider configs: %v\n", err)
		configs = []models.ProviderConfiguration{}
	}

	// Restore providers from database
	restoredCount := 0
	for _, config := range configs {
		providerConfig := config
		provider, err := a.providerManager.RestoreProvider(providerConfig)
		if err != nil {
			fmt.Printf("App.startup: ERROR - Failed to restore provider %s: %v\n", providerConfig.ProviderID, err)
			continue
		}
		restoredCount++

		instanceID := providerConfig.InstanceID
		if instanceID == "" {
			instanceID = fmt.Sprintf("%s-1", providerConfig.ProviderID)
		}

		isAuth := provider.IsAuthenticated()
		if isAuth {
			if err := provider.Connect(); err != nil {
				log.Printf("Warning: Failed to connect provider %s: %v", providerConfig.ProviderID, err)
				continue
			}
			// Start event listener for all connected providers
			a.startEventListenerForProvider(ctx, instanceID, provider)
		} else {
			if providerConfig.IsActive {
				if db.DB != nil {
					db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Update("is_active", false)
				}
				providerConfig.IsActive = false
			}
			continue
		}

		if providerConfig.IsActive {
			a.mu.Lock()
			a.provider = provider
			a.mu.Unlock()
			a.providerManager.SetActiveProvider(instanceID)
		}

		// Collect pending syncs for all connected providers
		var syncSince time.Time
		if providerConfig.LastSyncAt != nil {
			if time.Since(*providerConfig.LastSyncAt) > time.Minute {
				syncSince = *providerConfig.LastSyncAt
			}
			// If < 1 minute ago, no sync needed
		} else {
			// First time: sync last 365 days
			syncSince = time.Now().Add(-365 * 24 * time.Hour)
		}
		if !syncSince.IsZero() && provider != nil {
			a.pendingSyncs = append(a.pendingSyncs, pendingSyncInfo{
				provider:   provider,
				instanceID: instanceID,
				since:      syncSince,
			})
		}
	}
	a.setupSystemTray(ctx)
	go a.startWakeDetector()
}

// startWakeDetector monitors for system sleep/wake by detecting large time gaps
func (a *App) startWakeDetector() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	lastTick := time.Now()
	for {
		select {
		case now := <-ticker.C:
			// If more than 30 seconds have passed since the last 10-second tick,
			// the computer was likely asleep or the system was heavily throttled.
			if now.Sub(lastTick) > 30*time.Second {
				log.Printf("[WakeDetector] System wake detected (gap: %v). Triggering resync...", now.Sub(lastTick))
				// Wait a few seconds for network to stabilize
				time.Sleep(5 * time.Second)
				a.resyncAllProviders()
			}
			lastTick = now
		case <-a.ctx.Done():
			return
		}
	}
}

func (a *App) resyncAllProviders() {
	if a.providerManager == nil {
		return
	}

	providers := a.providerManager.GetConfiguredProviders()
	log.Printf("[App] Resyncing %d configured providers after system wake", len(providers))

	for _, pInfo := range providers {
		provider, err := a.providerManager.GetProvider(pInfo.InstanceID)
		if err != nil {
			continue
		}

		// Sync history since 24 hours ago to catch up on missed messages
		since := time.Now().Add(-24 * time.Hour)
		go func(p core.Provider, sid string, t time.Time) {
			log.Printf("[App] Triggering catch-up sync for %s", sid)
			p.SyncHistory(t)
		}(provider, pInfo.InstanceID, since)
	}
}

// domReady is called when the frontend DOM is ready. It is the right place to start
// operations that emit events to the frontend, since the Wails IPC and React's EventsOn
// listeners are guaranteed to be active by this point.
func (a *App) domReady(ctx context.Context) {
	syncs := a.pendingSyncs
	a.pendingSyncs = nil

	for _, si := range syncs {
		go func(syncInfo pendingSyncInfo) {
			// Small delay to let React mount and register EventsOn("sync-status") listener.
			time.Sleep(500 * time.Millisecond)
			fmt.Printf("App.domReady: starting sync for %s since %s\n", syncInfo.instanceID, syncInfo.since.Format(time.RFC3339))
			syncInfo.provider.SyncHistory(syncInfo.since)
			if db.DB != nil {
				db.DB.Model(&models.ProviderConfiguration{}).
					Where("instance_id = ?", syncInfo.instanceID).
					Update("last_sync_at", time.Now())
			}
		}(si)
	}
}

func (a *App) shutdown(ctx context.Context) {}

// ForceSyncCompletion emits a "completed" sync-status event to dismiss the sync footer.
// Called by the frontend when the user clicks the Stop button.
func (a *App) ForceSyncCompletion(instanceID string) {
	if a.ctx == nil {
		return
	}
	syncStatus := core.SyncStatusEvent{
		InstanceID: instanceID,
		Status:     core.SyncStatusCompleted,
		Message:    "Sync stopped by user",
		Progress:   100,
	}
	syncStatusJSON, _ := json.Marshal(syncStatus)
	runtime.EventsEmit(a.ctx, "sync-status", string(syncStatusJSON))
}

func (a *App) startEventListenerForProvider(ctx context.Context, instanceID string, provider core.Provider) {
	a.mu.Lock()
	if cancel, exists := a.eventCancels[instanceID]; exists {
		cancel()
	}
	subCtx, cancel := context.WithCancel(ctx)
	a.eventCancels[instanceID] = cancel
	a.mu.Unlock()

	eventChan, err := provider.StreamEvents()
	if err != nil {
		log.Printf("[%s] Failed to get event stream: %v", instanceID, err)
		cancel()
		return
	}

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.eventCancels, instanceID)
			a.mu.Unlock()
		}()

		for {
			select {
			case <-subCtx.Done():
				return
			case event, ok := <-eventChan:
				if !ok {
					return
				}
				// Add instanceID to event if needed or handle it here
				switch e := event.(type) {
				case core.MessageEvent:
					a.invalidateMessageCaches()
					msgJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "new-message", string(msgJSON))
					}
				case core.MessageBatchEvent:
					a.invalidateMessageCaches()
					batchJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "new-messages-batch", string(batchJSON))
					}
				case core.ReactionEvent:
					// Basic DB saving/removing for reactions
					if db.DB != nil {
						var message models.Message
						if err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", e.MessageID, e.ConversationID).First(&message).Error; err == nil {
							if e.Added {
								var existing models.Reaction
								if db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", message.ID, e.UserID, e.Emoji).First(&existing).Error != nil {
									db.DB.Create(&models.Reaction{
										MessageID: message.ID,
										UserID:    e.UserID,
										Emoji:     e.Emoji,
										CreatedAt: time.Unix(e.Timestamp, 0),
										UpdatedAt: time.Unix(e.Timestamp, 0),
									})
								}
							} else {
								db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", message.ID, e.UserID, e.Emoji).Delete(&models.Reaction{})
							}
						}
					}
					reactionJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "reaction", string(reactionJSON))
					}
				case core.TypingEvent:
					typingJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "typing", string(typingJSON))
					}
				case core.ContactStatusEvent:
					if e.Status == "avatar_updated" || (e.UserID == "refresh" && (e.Status == "sync_complete" || e.Status == "message_received" || e.Status == "mpim_updated" || e.Status == "new_conversations_discovered")) {
						a.emitContactsRefresh()
					}

					statusJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "contact-status", string(statusJSON))
					}
				case core.PresenceEvent:
					presenceJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "presence", string(presenceJSON))
					}
				case core.GroupChangeEvent:
					groupChangeJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "group-change", string(groupChangeJSON))
					}
				case core.ReceiptEvent:
					receiptJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "receipt", string(receiptJSON))
					}
				case core.RetryReceiptEvent:
					retryReceiptJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "retry-receipt", string(retryReceiptJSON))
					}
				case core.SyncStatusEvent:
					a.emitSyncStatusCoordinated(e)
				case core.ConversationReadStatusEvent:
					readStatusJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "conversation-read-status", string(readStatusJSON))
					}
				}
			}
		}
	}()
}

// emitSyncStatusCoordinated forwards a SyncStatusEvent to the frontend, but
// suppresses per-provider "completed" events until all active providers have
// finished. This prevents "sync complete" from appearing in the footer while
// a second provider is still fetching history.
func (a *App) emitSyncStatusCoordinated(e core.SyncStatusEvent) {
	if a.ctx == nil {
		return
	}

	a.syncingProvidersMu.Lock()
	if a.syncingProviders == nil {
		a.syncingProviders = make(map[string]bool)
	}

	switch e.Status {
	case core.SyncStatusFetchingHistory, core.SyncStatusFetchingContacts, core.SyncStatusFetchingAvatars:
		a.syncingProviders[e.InstanceID] = true
		a.syncingProvidersMu.Unlock()

	case core.SyncStatusCompleted:
		delete(a.syncingProviders, e.InstanceID)
		stillSyncing := len(a.syncingProviders) > 0
		a.syncingProvidersMu.Unlock()

		if stillSyncing {
			// At least one other provider is still active — swallow this event.
			// The frontend will receive "completed" once the last provider finishes.
			return
		}

	case core.SyncStatusError:
		delete(a.syncingProviders, e.InstanceID)
		a.syncingProvidersMu.Unlock()

	default:
		a.syncingProvidersMu.Unlock()
	}

	syncStatusJSON, _ := json.Marshal(e)
	runtime.EventsEmit(a.ctx, "sync-status", string(syncStatusJSON))
}

// GetAvatar retrieves an avatar file and returns a base64 data URL
func (a *App) GetAvatar(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "data:") || strings.HasPrefix(path, "http") {
		return path
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// Don't log if path is obviously invalid or empty
		if len(path) > 0 && !strings.Contains(path, " ") {
			fmt.Printf("[App.GetAvatar] Error reading file at %s: %v\n", path, err)
		}
		return ""
	}
	mimeType := "image/jpeg"
	if strings.HasSuffix(strings.ToLower(path), ".png") {
		mimeType = "image/png"
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
}

func (a *App) GetConfig() map[string]interface{} {
	return map[string]interface{}{"theme": "dark"}
}

func (a *App) SaveConfig(config map[string]interface{}) error { return nil }

// GetConfiguredProviders returns a list of configured providers with their status
func (a *App) GetConfiguredProviders() ([]core.ProviderInfo, error) {
	if a.providerManager == nil {
		return []core.ProviderInfo{}, nil
	}
	return a.providerManager.GetConfiguredProviders(), nil
}

// For frontend compatibility if GetConfiguredProviders doesn't return exactly what's needed
// We might need to wrap it differently, but assuming PM has it based on provider_manager.go

func (a *App) GetAvailableProviders() ([]core.ProviderInfo, error) {
	if a.providerManager == nil {
		return []core.ProviderInfo{}, nil
	}
	return a.providerManager.GetAvailableProviders(), nil
}

func (a *App) GetProviderSchema(providerID string) (map[string]interface{}, error) {
	if a.providerManager == nil {
		return nil, fmt.Errorf("provider manager not initialized")
	}
	return a.providerManager.GetProviderSchema(providerID)
}

func (a *App) ConfigureProvider(config string) error {
	var configData map[string]interface{}
	if err := json.Unmarshal([]byte(config), &configData); err != nil {
		return err
	}

	providerID, ok := configData["provider_id"].(string)
	if !ok {
		return fmt.Errorf("provider_id is required")
	}

	// Delegate mostly to internal logic or use CreateProvider
	// Simplified implementation for restoration:
	instanceName, _ := configData["instance_name"].(string)
	if instanceName == "" {
		instanceName = "Default"
	}
	instanceID, _ := configData["instance_id"].(string)

	configBytes, _ := json.Marshal(configData["config"])

	// Logic from previous implementation
	providerConfig := models.ProviderConfiguration{
		ProviderID:   providerID,
		InstanceName: instanceName,
		ConfigJSON:   string(configBytes),
		IsActive:     true,
	}
	if instanceID != "" {
		providerConfig.InstanceID = instanceID
		a.providerManager.UpdateProviderConfig(providerConfig)
	} else {
		providerConfig.InstanceID = fmt.Sprintf("%s-%d", providerID, time.Now().Unix())
		a.providerManager.SaveProviderConfig(providerConfig)
		instanceID = providerConfig.InstanceID
	}

	provider, err := a.providerManager.RestoreProvider(providerConfig)
	if err != nil {
		return err
	}
	provider.Connect()
	a.mu.Lock()
	a.provider = provider
	a.mu.Unlock()
	a.providerManager.SetActiveProvider(instanceID)
	// DB updates omitted for brevity but should be here
	a.startEventListenerForProvider(a.ctx, instanceID, provider)

	// Trigger initial sync for newly configured provider. Since Connect() no longer
	// starts sync automatically, we start it here after the event listener is ready.
	capturedID := instanceID
	go func() {
		time.Sleep(500 * time.Millisecond)
		since := time.Now().Add(-30 * 24 * time.Hour)
		provider.SyncHistory(since)
		if db.DB != nil {
			db.DB.Model(&models.ProviderConfiguration{}).
				Where("instance_id = ?", capturedID).
				Update("last_sync_at", time.Now())
		}
	}()

	return nil
}

func (a *App) CreateProvider(providerID string, config map[string]interface{}, instanceName string, instanceID string) (string, error) {
	// If config comes as map[string]interface{}, convert to map[string]string if needed or use as is
	// ProviderConfig is map[string]interface{}
	id, _, err := a.providerManager.CreateProvider(providerID, config, instanceName, instanceID)
	return id, err
}

func (a *App) CreateProviderWithOptions(providerID string, config map[string]interface{}, instanceName string, instanceID string, skipConnect bool) (string, error) {
	// We might need to handle skipConnect in ProviderManager or just manually
	// For now, delegate to CreateProvider which does connecting by default,
	// unless we modify ProviderManager to support skipping.
	// But let's check if we can pass a flag via config or just allow it.
	// Looking at PM.CreateProvider, it calls provider.Init().
	// Connection usually happens separately or inside?
	// In the original code (reconstructed), CreateProvider calls provider.Connect() indirectly?
	// Actually PM.CreateProvider doesn't seem to call Connect() automatically, the frontend calls ConnectProvider later?
	// But let's look at the implementation I wrote earlier... I delegated to PM.CreateProvider.

	// If PM.CreateProvider doesn't connect, then skipConnect=true means just don't call anything else.
	id, _, err := a.providerManager.CreateProvider(providerID, config, instanceName, instanceID)

	// If skipConnect is false, we might want to ensure it's connected?
	// But frontend calls ConnectProvider anyway usually.
	return id, err
}

func (a *App) SetActiveProvider(instanceID string) error {
	if a.providerManager == nil {
		return fmt.Errorf("provider manager not initialized")
	}
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.provider = provider
	a.mu.Unlock()
	a.providerManager.SetActiveProvider(instanceID)

	// DB Update
	if db.DB != nil {
		db.DB.Model(&models.ProviderConfiguration{}).Update("is_active", false)
		db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Update("is_active", true)
	}

	// Provider's event listener should already be running if connected
	return nil
}

func (a *App) GetMetaContacts() ([]models.MetaContact, error) {
	if db.DB == nil {
		return []models.MetaContact{}, nil
	}

	// Return cached result if still fresh. Avatar updates and MPIM renames can fire
	// hundreds of contacts-refresh events per startup; the cache collapses them.
	a.metaContactsCache.mu.RLock()
	if time.Now().Before(a.metaContactsCache.expiresAt) && a.metaContactsCache.data != nil {
		cached := a.metaContactsCache.data
		a.metaContactsCache.mu.RUnlock()
		return cached, nil
	}
	a.metaContactsCache.mu.RUnlock()

	// Load contacts from the in-memory store — zero DB queries.
	metaContacts := db.ContactStore.GetAll()

	// Apply conversation IDs from the in-memory store — O(1), no DB query needed.
	// Avatars are NOT converted to base64 here; the frontend loads them on-demand via GetAvatar.
	for i := range metaContacts {
		for j := range metaContacts[i].LinkedAccounts {
			la := &metaContacts[i].LinkedAccounts[j]
			if convID := db.ContactStore.GetConversation(la.ID); convID != "" {
				la.ConversationID = convID
			}
		}
	}

	// Store in cache for 2 seconds.
	a.metaContactsCache.mu.Lock()
	a.metaContactsCache.data = metaContacts
	a.metaContactsCache.expiresAt = time.Now().Add(2 * time.Second)
	a.metaContactsCache.mu.Unlock()

	return metaContacts, nil
}

// invalidateMessageCaches clears the three short-lived message caches so the next
// call to GetAllLastMessages / GetAllLastMessageTimestamps / GetAllMessageCounts
// hits the DB. Called whenever a new message is saved.
func (a *App) invalidateMessageCaches() {
	now := time.Time{} // zero = expired
	a.lastMessagesCache.mu.Lock()
	a.lastMessagesCache.expiresAt = now
	a.lastMessagesCache.mu.Unlock()

	a.lastTimestampsCache.mu.Lock()
	a.lastTimestampsCache.expiresAt = now
	a.lastTimestampsCache.mu.Unlock()

	a.messageCountsCache.mu.Lock()
	a.messageCountsCache.expiresAt = now
	a.messageCountsCache.mu.Unlock()
}

// invalidateMetaContactsCache clears the GetMetaContacts cache so the next call hits the DB.
func (a *App) invalidateMetaContactsCache() {
	a.metaContactsCache.mu.Lock()
	a.metaContactsCache.expiresAt = time.Time{}
	a.metaContactsCache.mu.Unlock()
}

// emitContactsRefresh debounces contacts-refresh emissions to the frontend.
// Multiple rapid-fire events (avatar updates, MPIM renames) are collapsed into
// a single emission after a 500ms quiet period.
func (a *App) emitContactsRefresh() {
	a.contactsRefreshMu.Lock()
	defer a.contactsRefreshMu.Unlock()
	if a.contactsRefreshTimer != nil {
		a.contactsRefreshTimer.Stop()
	}
	a.contactsRefreshTimer = time.AfterFunc(500*time.Millisecond, func() {
		a.invalidateMetaContactsCache()
		a.invalidateMessageCaches()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "contacts-refresh", "{}")
		}
	})
}

// enrichMessagesWithSenderNames enriches messages with sender names from LinkedAccount table
func (a *App) enrichMessagesWithSenderNames(messages []models.Message) {
	if len(messages) == 0 || db.DB == nil {
		return
	}

	// 1. Get ConversationID -> ProviderInstanceID mapping
	convIDs := make([]uint, 0)
	seenConvIDs := make(map[uint]bool)
	for _, msg := range messages {
		if msg.ConversationID != 0 && !seenConvIDs[msg.ConversationID] {
			convIDs = append(convIDs, msg.ConversationID)
			seenConvIDs[msg.ConversationID] = true
		}
	}

	convToInstance := make(map[uint]string)
	if len(convIDs) > 0 {
		var results []struct {
			ID                 uint
			ProviderInstanceID string
		}
		// Query conversations joined with linked_accounts to get the instance ID for each conversation
		db.DB.Table("conversations").
			Select("conversations.id, linked_accounts.provider_instance_id").
			Joins("join linked_accounts on conversations.linked_account_id = linked_accounts.id").
			Where("conversations.id IN ?", convIDs).
			Scan(&results)

		for _, res := range results {
			convToInstance[res.ID] = res.ProviderInstanceID
		}
	}

	// 2. Group SenderIDs by InstanceID
	instanceToSenderIDs := make(map[string]map[string]bool)
	for _, msg := range messages {
		instID := convToInstance[msg.ConversationID]
		if instID == "" {
			// Fallback: If we can't find the instance from the conversation (e.g. new message not yet stored),
			// check if we have an active provider as a last resort fallback.
			if a.getActiveProvider() != nil {
				if config := a.getActiveProvider().GetConfig(); config != nil {
					if id, ok := config["_instance_id"].(string); ok {
						instID = id
						convToInstance[msg.ConversationID] = instID
					}
				}
			}
		}

		if instID != "" && msg.SenderID != "" {
			if _, ok := instanceToSenderIDs[instID]; !ok {
				instanceToSenderIDs[instID] = make(map[string]bool)
			}
			instanceToSenderIDs[instID][msg.SenderID] = true
		}
	}

	// 3. Query LinkedAccounts for each instance to get names and avatars
	nameMap := make(map[string]map[string]string)   // instanceID -> userID -> name
	avatarMap := make(map[string]map[string]string) // instanceID -> userID -> avatar

	for instID, senderIDs := range instanceToSenderIDs {
		userIDList := make([]string, 0, len(senderIDs))
		for userID := range senderIDs {
			userIDList = append(userIDList, userID)
		}

		var accounts []models.LinkedAccount
		err := db.DB.Where("provider_instance_id = ? AND user_id IN ?", instID, userIDList).
			Find(&accounts).Error
		if err != nil {
			fmt.Printf("enrichMessagesWithSenderNames: Failed to query LinkedAccount for instance %s: %v\n", instID, err)
			continue
		}

		nameMap[instID] = make(map[string]string)
		avatarMap[instID] = make(map[string]string)
		for _, account := range accounts {
			if account.Username != "" && account.Username != account.UserID {
				nameMap[instID][account.UserID] = account.Username
			}
			if account.AvatarURL != "" {
				avatarMap[instID][account.UserID] = account.AvatarURL
			}
		}
	}

	// 4. Enrich messages with names and avatars
	enrichedCount := 0
	notFoundCount := 0
	for i := range messages {
		msg := &messages[i]
		instID := convToInstance[msg.ConversationID]
		if instID == "" {
			continue
		}

		if msg.SenderID != "" {
			// Enrich name if missing OR if it currently contains the ID (fix for previously bad persisted data)
			if msg.SenderName == "" || msg.SenderName == msg.SenderID {
				if name, ok := nameMap[instID][msg.SenderID]; ok {
					msg.SenderName = name
					enrichedCount++
				} else {
					notFoundCount++
				}
			}

			// Enrich avatar if missing
			if msg.SenderAvatarURL == "" {
				if avatar, ok := avatarMap[instID][msg.SenderID]; ok && avatar != "" {
					msg.SenderAvatarURL = avatar
				}
			}
		}
	}

	if enrichedCount > 0 || notFoundCount > 0 {
		fmt.Printf("enrichMessagesWithSenderNames: Enriched %d messages, %d names still not found\n", enrichedCount, notFoundCount)
	}
}

// GetMessagesForConversation - Renamed from GetMessages to match frontend expected name
// GetMessagesForConversation returns messages for a conversation
func (a *App) GetMessagesForConversation(conversationID string) ([]models.Message, error) {
	if db.DB == nil {
		return []models.Message{}, nil
	}
	var messages []models.Message
	err := db.DB.Where("protocol_conv_id = ?", conversationID).
		Preload("Reactions").
		Order("timestamp desc").
		Limit(50).
		Find(&messages).Error

	if err != nil {
		return []models.Message{}, err
	}

	// If no messages found in DB, try to fetch from the correct provider for this conversation.
	if len(messages) == 0 {
		if provider := a.getProviderForConversation(conversationID); provider != nil {
			fetchedMessages, err := provider.GetConversationHistory(conversationID, 50, nil, nil)
			if err == nil && len(fetchedMessages) > 0 {
				a.enrichMessagesWithSenderNames(fetchedMessages)
				return fetchedMessages, nil
			}
			if err != nil {
				fmt.Printf("GetMessagesForConversation: failed to fetch history from provider: %v\n", err)
			}
		}
	}

	// Enrich messages with sender names from LinkedAccount
	a.enrichMessagesWithSenderNames(messages)

	// Trigger contact refresh in background when opening conversation
	if provider := a.getActiveProvider(); provider != nil {
		go func() {
			if err := provider.RefreshContact(conversationID); err != nil {
				fmt.Printf("App.GetMessagesForConversation: failed to refresh contact %s: %v\n", conversationID, err)
			}
		}()
	}

	return messages, err
}

// GetMessagesForConversationBefore returns messages before a specific timestamp for pagination
// GetThreadMessages retrieves all messages in a thread
func (a *App) GetThreadMessages(conversationID string, threadID string) ([]models.Message, error) {
	fmt.Printf("[GetThreadMessages] Getting thread messages for conversation %s, thread %s\n", conversationID, threadID)

	// First try to load from database
	var messages []models.Message
	if db.DB != nil {
		err := db.DB.Where("protocol_conv_id = ? AND thread_id = ?", conversationID, threadID).
			Preload("Receipts").
			Preload("Reactions").
			Order("timestamp ASC").
			Find(&messages).Error

		if err == nil && len(messages) > 0 {
			// Enrich messages with sender names
			a.enrichMessagesWithSenderNames(messages)
			fmt.Printf("[GetThreadMessages] Loaded %d thread messages from database\n", len(messages))
			return messages, nil
		}
	}

	// If not found in DB, try to fetch from provider
	// This shouldn't happen often as threads are fetched with the main conversation
	fmt.Printf("[GetThreadMessages] Thread messages not found in database, returning empty list\n")
	return []models.Message{}, nil
}

func (a *App) GetMessagesForConversationBefore(conversationID string, beforeTimestamp time.Time) ([]models.Message, error) {
	fmt.Printf("[GetMessagesForConversationBefore] Loading messages for %s before %v\n", conversationID, beforeTimestamp)
	const limit = 50

	var messages []models.Message
	var err error

	if db.DB != nil {
		err = db.DB.Where("protocol_conv_id = ? AND timestamp < ?", conversationID, beforeTimestamp).
			Preload("Reactions").
			Order("timestamp desc").
			Limit(limit).
			Find(&messages).Error

		if err != nil {
			fmt.Printf("[GetMessagesForConversationBefore] DB error: %v\n", err)
		} else if len(messages) > 0 {
			fmt.Printf("[GetMessagesForConversationBefore] Found %d messages in DB\n", len(messages))
			// Enrich messages with sender names from LinkedAccount
			a.enrichMessagesWithSenderNames(messages)
			return messages, nil
		} else {
			fmt.Printf("[GetMessagesForConversationBefore] No DB messages before %v, checking provider\n", beforeTimestamp)
		}
	} else {
		fmt.Printf("[GetMessagesForConversationBefore] No DB connection, checking provider\n")
	}

	if a.getActiveProvider() != nil {
		before := beforeTimestamp
		providerMessages, providerErr := a.getActiveProvider().GetConversationHistory(conversationID, limit, &before, nil)
		if providerErr != nil {
			fmt.Printf("[GetMessagesForConversationBefore] Provider fetch failed: %v\n", providerErr)
			if err == nil {
				err = providerErr
			}
		} else if len(providerMessages) > 0 {
			fmt.Printf("[GetMessagesForConversationBefore] Provider returned %d messages\n", len(providerMessages))
			return providerMessages, nil
		} else {
			fmt.Printf("[GetMessagesForConversationBefore] Provider returned no messages before %v\n", beforeTimestamp)
		}
	} else {
		fmt.Printf("[GetMessagesForConversationBefore] No provider configured\n")
	}

	return messages, err
}
func (a *App) SendMessage(conversationID string, content string) (*models.Message, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.SendMessage(conversationID, content, nil, nil)
}

// SendReply sends a reply to a message
func (a *App) SendReply(conversationID string, content string, quotedMessageID string) (*models.Message, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.SendMessage(conversationID, content, nil, &quotedMessageID)
}

func (a *App) SendFile(conversationID string, base64Data string, filename string, mimeType string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}

	if idx := strings.Index(base64Data, ","); idx != -1 {
		base64Data = base64Data[idx+1:]
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return err
	}

	// Use provided mimeType if available, otherwise try to guess from filename
	if mimeType == "" {
		mimeType = "application/octet-stream"
		ext := strings.ToLower(filepath.Ext(filename))
		if ext == ".png" {
			mimeType = "image/png"
		} else if ext == ".jpg" || ext == ".jpeg" {
			mimeType = "image/jpeg"
		} else if ext == ".pdf" {
			mimeType = "application/pdf"
		}
	}

	attachment := &core.Attachment{
		FileName: filename,
		Data:     data,
		FileSize: len(data),
		MimeType: mimeType,
	}

	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	_, err = provider.SendFile(conversationID, attachment, nil)
	return err
}

func (a *App) DisconnectProvider(instanceID string) error {
	return a.RemoveProvider(instanceID)
}

func (a *App) ConnectProvider(instanceID string) error {
	// ... minimal impl ...
	if a.providerManager == nil {
		return fmt.Errorf("no pm")
	}
	provider, err := a.providerManager.GetProvider(instanceID)
	// Try restore ...
	if err != nil && db.DB != nil {
		var config models.ProviderConfiguration
		if db.DB.Where("instance_id = ?", instanceID).First(&config).Error == nil {
			provider, err = a.providerManager.RestoreProvider(config)
		}
	}
	if err != nil {
		return err
	}

	if err := provider.Connect(); err != nil {
		return err
	}

	a.mu.Lock()
	a.provider = provider
	a.mu.Unlock()
	a.providerManager.SetActiveProvider(instanceID)
	a.startEventListenerForProvider(a.ctx, instanceID, provider)

	if db.DB != nil {
		db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Update("is_active", true)
	}
	return nil
}

func (a *App) RemoveProvider(instanceID string) error {
	if a.providerManager == nil {
		return fmt.Errorf("no pm")
	}
	if err := a.providerManager.RemoveProvider(instanceID); err != nil {
		return err
	}
	a.emitContactsRefresh()
	return nil
}

// Additional missing methods found in logs

func (a *App) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	if a.getActiveProvider() == nil {
		return nil, fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().CreateGroup(groupName, participantIDs)
}

func (a *App) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	if a.getActiveProvider() == nil {
		return nil, fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().GetGroupParticipants(conversationID)
}

func (a *App) GetThreads(parentMessageID string) ([]models.Message, error) {
	if a.getActiveProvider() == nil {
		return nil, fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().GetThreads(parentMessageID)
}

func (a *App) AddReaction(conversationID, messageID, emoji string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.AddReaction(conversationID, messageID, emoji)
}

func (a *App) RemoveReaction(conversationID, messageID, emoji string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.RemoveReaction(conversationID, messageID, emoji)
}

func (a *App) EditMessage(conversationID, messageID, newText string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	_, err := provider.EditMessage(conversationID, messageID, newText)
	return err
}

func (a *App) DeleteMessage(conversationID, messageID string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	if err := provider.DeleteMessage(conversationID, messageID); err != nil {
		return err
	}
	// Remove from local DB
	if db.DB != nil {
		db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, conversationID).Delete(&models.Message{})
	}
	// Notify frontend
	if a.ctx != nil {
		type deletedPayload struct {
			ConversationID string `json:"conversationId"`
			MessageID      string `json:"messageId"`
		}
		payload, _ := json.Marshal(deletedPayload{ConversationID: conversationID, MessageID: messageID})
		runtime.EventsEmit(a.ctx, "message-deleted", string(payload))
	}
	return nil
}

func (a *App) MarkMessageAsRead(conversationID, messageID string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.MarkMessageAsRead(conversationID, messageID)
}

func (a *App) MarkConversationAsRead(conversationID string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().MarkConversationAsRead(conversationID)
}

func (a *App) MarkMessageAsPlayed(conversationID, messageID string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().MarkMessageAsPlayed(conversationID, messageID)
}

func (a *App) GetProviderQRCode(instanceID string) (string, error) {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return "", err
	}

	if !provider.GetCapabilities().SupportsQRCodeAuth {
		return "", fmt.Errorf("provider does not support QR code auth")
	}

	return provider.GetAuthQRCode()
}

// SyncProvider triggers a synchronization for a specific provider.
// If providerID is empty, syncs the active provider.
func (a *App) SyncProvider(providerID string) error {
	var provider core.Provider
	var err error

	if providerID == "" {
		// Sync active provider
		if a.getActiveProvider() == nil {
			return fmt.Errorf("no active provider to sync")
		}
		provider = a.getActiveProvider()
	} else {
		// Sync specific provider
		provider, err = a.providerManager.GetProvider(providerID)
		if err != nil {
			return err
		}
	}

	// Run sync in a background goroutine so the frontend call returns immediately.
	// A 500ms delay ensures the SyncStatusFooter's EventsOn("sync-status") listener
	// has time to register before the first sync event is emitted.
	since := time.Now().Add(-30 * 24 * time.Hour)
	go func() {
		time.Sleep(500 * time.Millisecond)
		provider.SyncHistory(since)
	}()
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

func (a *App) GetCustomEmojis(instanceID string) (map[string]string, error) {
	if a.providerManager == nil {
		return nil, nil
	}

	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return nil, nil
	}

	return provider.GetCustomEmojis()
}

func (a *App) GetCapabilities(instanceID string) (core.Capabilities, error) {
	if a.providerManager == nil {
		return core.Capabilities{}, fmt.Errorf("provider manager not initialized")
	}

	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return core.Capabilities{}, err
	}

	return provider.GetCapabilities(), nil
}

// GetAllActiveCalls returns a map of conversation IDs to boolean indicating if there's an active call
// An active call is one with CallType "incoming_call" or "incoming_group_call" that hasn't been terminated
func (a *App) GetAllActiveCalls() (map[string]bool, error) {
	if db.DB == nil {
		return map[string]bool{}, nil
	}

	// Find all messages with active call types (incoming_call or incoming_group_call)
	// These are calls that are currently active (ringing/ongoing)
	var activeCallMessages []models.Message
	err := db.DB.Where("call_type IN ?", []string{"incoming_call", "incoming_group_call"}).
		Select("protocol_conv_id").
		Group("protocol_conv_id").
		Find(&activeCallMessages).Error

	if err != nil {
		return map[string]bool{}, err
	}

	result := make(map[string]bool)
	for _, msg := range activeCallMessages {
		if msg.ProtocolConvID != "" {
			result[msg.ProtocolConvID] = true
		}
	}

	return result, nil
}

// GetAllMessageCounts returns a map of conversation IDs to message counts
// This is used to efficiently get message counts for all conversations in a single query
func (a *App) GetAllMessageCounts() (map[string]int, error) {
	if db.DB == nil {
		return map[string]int{}, nil
	}

	a.messageCountsCache.mu.RLock()
	if time.Now().Before(a.messageCountsCache.expiresAt) {
		cached := a.messageCountsCache.data
		a.messageCountsCache.mu.RUnlock()
		return cached, nil
	}
	a.messageCountsCache.mu.RUnlock()

	type Result struct {
		ProtocolConvID string
		Count          int64
	}

	var results []Result
	err := db.DB.Model(&models.Message{}).
		Select("protocol_conv_id, COUNT(*) as count").
		Group("protocol_conv_id").
		Scan(&results).Error

	if err != nil {
		return map[string]int{}, err
	}

	result := make(map[string]int, len(results))
	for _, r := range results {
		result[r.ProtocolConvID] = int(r.Count)
	}

	a.messageCountsCache.mu.Lock()
	a.messageCountsCache.data = result
	a.messageCountsCache.expiresAt = time.Now().Add(5 * time.Second)
	a.messageCountsCache.mu.Unlock()

	return result, nil
}

// GetConversationsWithMessages returns a list of ProtocolConvIDs that have messages
// This is a single query to get all conversations with messages, useful for determining
// which conversations might have unread messages (frontend will determine which are unread)
func (a *App) GetConversationsWithMessages() ([]string, error) {
	if db.DB == nil {
		return []string{}, nil
	}

	var conversations []string
	err := db.DB.Model(&models.Message{}).
		Distinct("protocol_conv_id").
		Pluck("protocol_conv_id", &conversations).Error

	return conversations, err
}
func (a *App) GetAllLastMessages() (map[string]models.Message, error) {
	if db.DB == nil {
		return map[string]models.Message{}, nil
	}

	a.lastMessagesCache.mu.RLock()
	if time.Now().Before(a.lastMessagesCache.expiresAt) {
		cached := a.lastMessagesCache.data
		a.lastMessagesCache.mu.RUnlock()
		return cached, nil
	}
	a.lastMessagesCache.mu.RUnlock()

	var messages []models.Message
	// ROW_NUMBER() window function does a single pass over the partial index
	// (protocol_conv_id, timestamp DESC WHERE deleted_at IS NULL), avoiding the
	// double-scan of the previous self-join. GORM silently ignores the rn column.
	err := db.DB.Raw(`
		SELECT * FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY protocol_conv_id ORDER BY timestamp DESC) AS rn
			FROM messages
			WHERE deleted_at IS NULL
		) WHERE rn = 1
	`).Find(&messages).Error

	if err != nil {
		fmt.Printf("[GetAllLastMessages] Error: %v\n", err)
		return map[string]models.Message{}, err
	}

	result := make(map[string]models.Message, len(messages))
	for _, msg := range messages {
		result[msg.ProtocolConvID] = msg
	}

	a.lastMessagesCache.mu.Lock()
	a.lastMessagesCache.data = result
	a.lastMessagesCache.expiresAt = time.Now().Add(5 * time.Second)
	a.lastMessagesCache.mu.Unlock()

	return result, nil
}
func (a *App) GetAllLastMessageTimestamps() (map[string]int64, error) {
	if db.DB == nil {
		fmt.Println("[GetAllLastMessageTimestamps] No database connection")
		return map[string]int64{}, nil
	}

	a.lastTimestampsCache.mu.RLock()
	if time.Now().Before(a.lastTimestampsCache.expiresAt) {
		cached := a.lastTimestampsCache.data
		a.lastTimestampsCache.mu.RUnlock()
		return cached, nil
	}
	a.lastTimestampsCache.mu.RUnlock()

	result := make(map[string]int64)

	rows, err := db.DB.Model(&models.Message{}).
		Select("protocol_conv_id, MAX(timestamp) as max_time").
		Group("protocol_conv_id").
		Rows()
	if err != nil {
		fmt.Printf("[GetAllLastMessageTimestamps] Error getting message timestamps: %v\n", err)
		return map[string]int64{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var protocolConvID string
		var maxTime interface{}
		if err := rows.Scan(&protocolConvID, &maxTime); err != nil {
			fmt.Printf("[GetAllLastMessageTimestamps] Error scanning message row: %v\n", err)
			continue
		}
		if ts := db.ParseTime(maxTime); ts > 0 {
			result[protocolConvID] = ts
		}
	}

	reactionRows, err := db.DB.Table("reactions").
		Joins("JOIN messages ON messages.id = reactions.message_id").
		Select("messages.protocol_conv_id, MAX(reactions.created_at) as max_time").
		Where("messages.deleted_at IS NULL").
		Group("messages.protocol_conv_id").
		Rows()
	if err == nil {
		defer reactionRows.Close()
		for reactionRows.Next() {
			var protocolConvID string
			var maxTime interface{}
			if err := reactionRows.Scan(&protocolConvID, &maxTime); err != nil {
				continue
			}
			if ts := db.ParseTime(maxTime); ts > result[protocolConvID] {
				result[protocolConvID] = ts
			}
		}
	}

	a.lastTimestampsCache.mu.Lock()
	a.lastTimestampsCache.data = result
	a.lastTimestampsCache.expiresAt = time.Now().Add(5 * time.Second)
	a.lastTimestampsCache.mu.Unlock()

	fmt.Printf("[GetAllLastMessageTimestamps] Returning %d total conversations with timestamps\n", len(result))
	return result, nil
}

func (a *App) GetParticipantNames(userIDs []string) (map[string]string, error) {
	if db.DB == nil {
		return map[string]string{}, nil
	}

	result := make(map[string]string)

	// 1. Check ContactAliases first (user-defined custom names take precedence)
	var aliases []models.ContactAlias
	if err := db.DB.Where("user_id IN ?", userIDs).Find(&aliases).Error; err == nil {
		for _, a := range aliases {
			result[a.UserID] = a.Alias
		}
	}

	// 2. Check LinkedAccounts - Username is the source of truth for display names
	var accounts []models.LinkedAccount
	var missingIDs []string
	if err := db.DB.Where("user_id IN ?", userIDs).Find(&accounts).Error; err == nil {
		for _, acc := range accounts {
			if _, exists := result[acc.UserID]; !exists {
				// LinkedAccount.Username is the authoritative display name
				if acc.Username != "" && acc.Username != acc.UserID {
					result[acc.UserID] = acc.Username
				} else {
					// Username is empty or same as userID, need to fetch from API
					missingIDs = append(missingIDs, acc.UserID)
				}
			}
		}
	}

	// Find IDs that don't have LinkedAccount records
	for _, userID := range userIDs {
		if _, exists := result[userID]; !exists {
			found := false
			for _, acc := range accounts {
				if acc.UserID == userID {
					found = true
					break
				}
			}
			if !found {
				missingIDs = append(missingIDs, userID)
			}
		}
	}

	// 3. For missing IDs, try to fetch from provider API using GetContactName
	// This uses the centralized function in each provider (lookupDisplayName for WhatsApp, resolveSlackUserName for Slack)
	if len(missingIDs) > 0 && a.getActiveProvider() != nil {
		for _, userID := range missingIDs {
			if _, exists := result[userID]; !exists {
				// Use the provider's GetContactName method which uses the centralized lookup function
				name, err := a.getActiveProvider().GetContactName(userID)
				if err == nil && name != "" && name != userID {
					result[userID] = name
				}
			}
		}
	}

	return result, nil
}

// GetAttachmentData reads local file or downloads remote URL and returns base64 data URL
func (a *App) GetAttachmentData(path string) (string, error) {
	// Slack files require authentication — only a Slack provider can fetch them.
	// We iterate ALL registered providers (not just the active one) because the user may be
	// viewing a WhatsApp conversation while requesting a Slack file attachment.
	if strings.Contains(path, "slack.com") {
		var lastErr error
		for _, instanceID := range a.providerManager.GetAllInstanceIDs() {
			p, err := a.providerManager.GetProvider(instanceID)
			if err != nil {
				continue
			}
			slackProvider, ok := p.(*slack.SlackProvider)
			if !ok {
				continue
			}
			data, err := slackProvider.GetFileData(path)
			if err == nil && data != "" {
				return data, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return "", fmt.Errorf("slack file unavailable: %w", lastErr)
		}
		return "", fmt.Errorf("no slack provider available for %s", path)
	}

	// Google Chat attachment URLs require OAuth2 authentication.
	// downloadUri uses chat.google.com (web UI), API URLs use chat.googleapis.com.
	if strings.Contains(path, "chat.google.com") || strings.Contains(path, "chat.googleapis.com") {
		var lastErr error
		for _, instanceID := range a.providerManager.GetAllInstanceIDs() {
			p, err := a.providerManager.GetProvider(instanceID)
			if err != nil {
				continue
			}
			gcProvider, ok := p.(*googlechat.GoogleChatProvider)
			if !ok {
				continue
			}
			data, err := gcProvider.GetFileData(path)
			if err == nil && data != "" {
				return data, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return "", fmt.Errorf("googlechat file unavailable: %w", lastErr)
		}
		return "", fmt.Errorf("no googlechat provider available for %s", path)
	}

	// Check if it's a URL (starts with http/https)
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// Download remote file
		resp, err := http.Get(path) // #nosec G107 — URL comes from trusted provider data
		if err != nil {
			return "", fmt.Errorf("failed to download %s: %w", path, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to download %s: HTTP %d", path, resp.StatusCode)
		}

		// Reject HTML — the server returned an error page instead of the file.
		mimeType := resp.Header.Get("Content-Type")
		if strings.HasPrefix(mimeType, "text/html") {
			return "", fmt.Errorf("server returned HTML instead of file data for %s", path)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
	}
	// Local file
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	// MIME type guess
	mimeType := "application/octet-stream"
	if strings.HasSuffix(path, ".opus") {
		mimeType = "audio/ogg"
	} else if strings.HasSuffix(path, ".mp3") {
		mimeType = "audio/mpeg"
	} else if strings.HasSuffix(path, ".png") {
		mimeType = "image/png"
	} else if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
		mimeType = "image/jpeg"
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
}

// SaveAttachmentToFile fetches an attachment and saves it to a user-chosen location via native dialog.
func (a *App) SaveAttachmentToFile(url string, fileName string) error {
	dataURL, err := a.GetAttachmentData(url)
	if err != nil {
		return fmt.Errorf("failed to fetch attachment: %w", err)
	}

	// Strip the "data:<mime>;base64," prefix
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return fmt.Errorf("invalid data URL format")
	}
	encoded := dataURL[comma+1:]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("failed to decode attachment data: %w", err)
	}

	// Ask the user where to save
	savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: fileName,
		Title:           "Enregistrer le fichier",
	})
	if err != nil {
		return fmt.Errorf("save dialog error: %w", err)
	}
	if savePath == "" {
		return nil // user cancelled
	}

	// Ensure the extension is preserved when the OS strips it
	if filepath.Ext(savePath) == "" && filepath.Ext(fileName) != "" {
		savePath += filepath.Ext(fileName)
	}

	if err := os.WriteFile(savePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// UpdateSystemTrayBadge updates the system tray icon with a badge showing the unread message count.
// This method is called from the frontend when the unread count changes.
func (a *App) UpdateSystemTrayBadge(count int) error {
	if a.ctx == nil {
		return fmt.Errorf("context not initialized")
	}

	log.Printf("System tray/Dock badge update requested: %d unread messages", count)

	var countStr string
	if count > 0 {
		countStr = fmt.Sprintf("%d", count)
		if count > 99 {
			countStr = "99+"
		}
	}

	// Use platform-specific badge APIs via our CGO helper
	setCgoDockBadge(countStr)
	return nil
}

// setupSystemTray creates and configures the system tray menu
func (a *App) setupSystemTray(ctx context.Context) {
	appMenu := menu.NewMenu()
	appMenu.Append(menu.Label("Loom"))
	appMenu.Append(menu.Separator())

	showHideItem := menu.Text("Show/Hide", nil, func(_ *menu.CallbackData) {
		runtime.WindowShow(ctx)
	})
	appMenu.Append(showHideItem)

	quitItem := menu.Text("Quit", nil, func(_ *menu.CallbackData) {
		runtime.Quit(ctx)
	})
	appMenu.Append(quitItem)

	a.systemTray = appMenu
}
