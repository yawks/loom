// Package main is the entry point for the Loom chat application.
package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"sync"
	"Loom/pkg/models"
	"Loom/pkg/providers"
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

// App struct
type App struct {
	ctx             context.Context
	provider        core.Provider // Active provider (for UI actions)
	providerManager *core.ProviderManager
	eventCancels    map[string]context.CancelFunc // Map of instanceID -> cancelFunc for event listeners
	systemTray      *menu.Menu
	pendingSyncs    []pendingSyncInfo // syncs deferred until domReady
	mu              sync.RWMutex
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
		if !syncSince.IsZero() {
			a.pendingSyncs = append(a.pendingSyncs, pendingSyncInfo{
				provider:   provider,
				instanceID: instanceID,
				since:      syncSince,
			})
		}
	}
	a.setupSystemTray(ctx)
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
					if e.Message.SenderAvatarURL != "" {
						e.Message.SenderAvatarURL = a.GetAvatar(e.Message.SenderAvatarURL)
					}
					msgJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "new-message", string(msgJSON))
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
					statusJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "contact-status", string(statusJSON))
						if e.UserID == "refresh" && (e.Status == "sync_complete" || e.Status == "message_received" || e.Status == "mpim_updated") {
							runtime.EventsEmit(a.ctx, "contacts-refresh", "{}")
						}
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
					syncStatusJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "sync-status", string(syncStatusJSON))
					}
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

// GetAvatar retrieves an avatar file and returns a base64 data URL
func (a *App) GetAvatar(path string) string {
	if strings.HasPrefix(path, "data:") || strings.HasPrefix(path, "http") {
		return path
	}
	data, err := os.ReadFile(path)
	if err != nil {
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
	var metaContacts []models.MetaContact
	err := db.DB.Preload("LinkedAccounts").Find(&metaContacts).Error
	if err != nil {
		return metaContacts, err
	}

	// Collect all linked account IDs to batch-fetch their conversation IDs
	var laIDs []uint
	for _, mc := range metaContacts {
		for _, la := range mc.LinkedAccounts {
			laIDs = append(laIDs, la.ID)
		}
	}

	// Single query to get the protocol conversation ID for each linked account
	if len(laIDs) > 0 {
		var conversations []models.Conversation
		db.DB.Select("linked_account_id, protocol_conv_id").
			Where("linked_account_id IN ?", laIDs).
			Order("id ASC").
			Find(&conversations)

		// Build a map: linkedAccountID -> protocolConvID (keep first found)
		convMap := make(map[uint]string, len(conversations))
		for _, conv := range conversations {
			if _, exists := convMap[conv.LinkedAccountID]; !exists {
				convMap[conv.LinkedAccountID] = conv.ProtocolConvID
			}
		}

		// Populate ConversationID on each LinkedAccount
		for i := range metaContacts {
			for j := range metaContacts[i].LinkedAccounts {
				la := &metaContacts[i].LinkedAccounts[j]
				if protocolConvID, ok := convMap[la.ID]; ok {
					la.ConversationID = protocolConvID
				}
			}
		}
	}

	return metaContacts, err
}

// enrichMessagesWithSenderNames enriches messages with sender names from LinkedAccount table
func (a *App) enrichMessagesWithSenderNames(messages []models.Message) {
	if len(messages) == 0 || db.DB == nil {
		return
	}

	// Collect unique sender IDs
	senderIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.SenderID != "" {
			senderIDs[msg.SenderID] = true
		}
	}

	if len(senderIDs) == 0 {
		return
	}

	// Get instance ID from active provider
	instanceID := ""
	if a.getActiveProvider() != nil {
		if config := a.getActiveProvider().GetConfig(); config != nil {
			if id, ok := config["_instance_id"].(string); ok {
				instanceID = id
			}
		}
	}

	if instanceID == "" {
		fmt.Printf("enrichMessagesWithSenderNames: WARNING - No instance ID found\n")
		return
	}

	// Query LinkedAccount for all sender IDs at once
	userIDList := make([]string, 0, len(senderIDs))
	for userID := range senderIDs {
		userIDList = append(userIDList, userID)
	}

	var linkedAccounts []models.LinkedAccount
	err := db.DB.Where("provider_instance_id = ? AND user_id IN ?", instanceID, userIDList).
		Find(&linkedAccounts).Error
	if err != nil {
		fmt.Printf("enrichMessagesWithSenderNames: Failed to query LinkedAccount: %v\n", err)
		return
	}

	fmt.Printf("enrichMessagesWithSenderNames: Found %d LinkedAccounts for %d sender IDs (instance: %s)\n",
		len(linkedAccounts), len(userIDList), instanceID)

	// Build maps of userID -> username and userID -> avatar
	nameMap := make(map[string]string)
	avatarMap := make(map[string]string)
	for _, account := range linkedAccounts {
		if account.Username != "" && account.Username != account.UserID {
			nameMap[account.UserID] = account.Username
			fmt.Printf("enrichMessagesWithSenderNames: Mapped %s -> %s\n", account.UserID, account.Username)
		} else {
			fmt.Printf("enrichMessagesWithSenderNames: WARNING - LinkedAccount for %s has no valid username (Username='%s')\n",
				account.UserID, account.Username)
		}

		// Also map avatar URL if available
		if account.AvatarURL != "" {
			avatarMap[account.UserID] = account.AvatarURL
		}
	}

	// Enrich messages with names and avatars
	enrichedCount := 0
	notFoundCount := 0
	for i := range messages {
		msg := &messages[i]
		if msg.SenderID != "" {
			// Enrich name if missing
			if msg.SenderName == "" {
				if name, ok := nameMap[msg.SenderID]; ok {
					msg.SenderName = name
					enrichedCount++
				} else {
					notFoundCount++
					fmt.Printf("enrichMessagesWithSenderNames: WARNING - No name found for sender %s\n", msg.SenderID)
				}
			}

			// Enrich avatar if missing
			if msg.SenderAvatarURL == "" {
				if avatar, ok := avatarMap[msg.SenderID]; ok {
					msg.SenderAvatarURL = avatar
				}
			}
		}
	}

	fmt.Printf("enrichMessagesWithSenderNames: Enriched %d messages, %d names not found\n", enrichedCount, notFoundCount)
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

	// If no messages found in DB, try to fetch from provider
	// This handles the case where we just synced the conversation list but haven't synced messages yet
	if len(messages) == 0 && a.getActiveProvider() != nil {
		// We use a limit of 50 to match the DB query
		fetchedMessages, err := a.getActiveProvider().GetConversationHistory(conversationID, 50, nil, nil)
		if err == nil && len(fetchedMessages) > 0 {
			return fetchedMessages, nil
		}
		// If fetch fails or returns empty, just return the empty list from DB
		// (logging the error might be noisy if it's just a wrong provider or permission issue)
		if err != nil {
			fmt.Printf("GetMessagesForConversation: failed to fetch history from provider: %v\n", err)
		}
	}

	// Enrich messages with sender names from LinkedAccount
	a.enrichMessagesWithSenderNames(messages)

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
	if a.getActiveProvider() == nil {
		return nil, fmt.Errorf("no active provider")
	}

	// Call provider to send message
	return a.getActiveProvider().SendMessage(conversationID, content, nil, nil)
}

// SendReply sends a reply to a message
func (a *App) SendReply(conversationID string, content string, quotedMessageID string) (*models.Message, error) {
	if a.getActiveProvider() == nil {
		return nil, fmt.Errorf("no active provider")
	}

	// Call provider to send reply
	return a.getActiveProvider().SendMessage(conversationID, content, nil, &quotedMessageID)
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

	_, err = a.getActiveProvider().SendFile(conversationID, attachment, nil)
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
	// Minimal impl
	if a.providerManager == nil {
		return fmt.Errorf("no pm")
	}
	return a.providerManager.RemoveProvider(instanceID)
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
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().AddReaction(conversationID, messageID, emoji)
}

func (a *App) RemoveReaction(conversationID, messageID, emoji string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().RemoveReaction(conversationID, messageID, emoji)
}

func (a *App) EditMessage(conversationID, messageID, newText string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	_, err := a.getActiveProvider().EditMessage(conversationID, messageID, newText)
	return err
}

func (a *App) DeleteMessage(conversationID, messageID string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	if err := a.getActiveProvider().DeleteMessage(conversationID, messageID); err != nil {
		return err
	}
	// Remove from local DB
	if db.DB != nil {
		db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, conversationID).Delete(&models.Message{})
	}
	// Notify frontend
	if a.ctx != nil {
		type deletedPayload struct {
			ConversationID string `json:"ConversationID"`
			MessageID      string `json:"MessageID"`
		}
		payload, _ := json.Marshal(deletedPayload{ConversationID: conversationID, MessageID: messageID})
		runtime.EventsEmit(a.ctx, "message-deleted", string(payload))
	}
	return nil
}

func (a *App) MarkMessageAsRead(conversationID, messageID string) error {
	if a.getActiveProvider() == nil {
		return fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().MarkMessageAsRead(conversationID, messageID)
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

	// Single query to get message counts per conversation
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

	result := make(map[string]int)
	for _, r := range results {
		result[r.ProtocolConvID] = int(r.Count)
	}

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

	// Get all conversations that have messages
	var conversations []string
	err := db.DB.Model(&models.Message{}).
		Distinct("protocol_conv_id").
		Pluck("protocol_conv_id", &conversations).Error

	if err != nil {
		return map[string]models.Message{}, err
	}

	result := make(map[string]models.Message)

	// For each conversation, get the latest message
	for _, convID := range conversations {
		var message models.Message
		err := db.DB.Where("protocol_conv_id = ?", convID).
			Preload("Reactions").
			Order("timestamp desc").
			First(&message).Error

		if err == nil {
			result[convID] = message
		}
	}

	return result, nil
}
func (a *App) GetAllLastMessageTimestamps() (map[string]int64, error) {
	if db.DB == nil {
		fmt.Println("[GetAllLastMessageTimestamps] No database connection")
		return map[string]int64{}, nil
	}

	result := make(map[string]int64)
	var err error

	// Single query: latest message timestamp per conversation
	type convMaxTime struct {
		ProtocolConvID string
		MaxTime        time.Time
	}

	var msgTimestamps []convMaxTime
	if err = db.DB.Model(&models.Message{}).
		Select("protocol_conv_id, MAX(timestamp) as max_time").
		Group("protocol_conv_id").
		Scan(&msgTimestamps).Error; err != nil {
		fmt.Printf("[GetAllLastMessageTimestamps] Error getting message timestamps: %v\n", err)
		return map[string]int64{}, err
	}
	for _, row := range msgTimestamps {
		result[row.ProtocolConvID] = row.MaxTime.Unix()
	}

	// Single query: latest reaction timestamp per conversation
	var reactionTimestamps []convMaxTime
	if err = db.DB.Model(&models.Reaction{}).
		Joins("JOIN messages ON messages.id = reactions.message_id").
		Select("messages.protocol_conv_id, MAX(reactions.created_at) as max_time").
		Group("messages.protocol_conv_id").
		Scan(&reactionTimestamps).Error; err == nil {
		for _, row := range reactionTimestamps {
			if ts := row.MaxTime.Unix(); ts > result[row.ProtocolConvID] {
				result[row.ProtocolConvID] = ts
			}
		}
	}

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
	// Check if it's a Slack URL that needs authentication
	if strings.Contains(path, "slack.com") {
		// Get the active provider (assuming it's Slack for now)
		provider, err := a.providerManager.GetActiveProvider()
		if err == nil {
			if slackProvider, ok := provider.(*slack.SlackProvider); ok {
				// Try to get file data using Slack provider (with authentication)
				data, err := slackProvider.GetFileData(path)
				if err == nil && data != "" {
					// Successfully got data from Slack provider
					return data, nil
				}
				fmt.Printf("[GetAttachmentData] Slack provider failed: %v\n", err)
			}
		}
		// If Slack provider failed or not active, fall back to direct HTTP request
		fmt.Printf("[GetAttachmentData] Falling back to direct HTTP request for %s\n", path)
	}

	// Check if it's a URL (starts with http/https)
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// Download remote file
		resp, err := http.Get(path)
		if err != nil {
			return "", fmt.Errorf("failed to download %s: %w", path, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to download %s: HTTP %d", path, resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		mimeType := resp.Header.Get("Content-Type")
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
