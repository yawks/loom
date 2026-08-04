// Package main is the entry point for the Loom chat application.
package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers"
	"Loom/pkg/providers/googlechat"
	googlemessages "Loom/pkg/providers/googlemessages"
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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/net/html"
	"gorm.io/gorm"
)

// pendingSyncInfo holds the information needed to trigger a sync after the frontend is ready.
type pendingSyncInfo struct {
	provider   core.Provider
	instanceID string
	since      time.Time
}

// History remains fully persisted in SQLite. The frontend only needs a recent
// window to refresh previews and read state after a background synchronization.
// Larger batches create several JSON/JavaScript copies inside WebKit.
const maxFrontendSyncBatchMessages = 100

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

// LinkPreview holds Open Graph metadata for a URL.
type LinkPreview struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"imageURL"`
	URL         string `json:"url"`
}

type linkPreviewEntry struct {
	preview   LinkPreview
	expiresAt time.Time
}

// App struct
type App struct {
	ctx               context.Context
	mockMode          bool
	provider          core.Provider // Active provider (for UI actions)
	providerManager   *core.ProviderManager
	eventCancels      map[string]context.CancelFunc // Map of instanceID -> cancelFunc for event listeners
	systemTray        *menu.Menu
	pendingSyncs      []pendingSyncInfo // syncs deferred until domReady
	metaContactsCache metaContactsCache
	mu                sync.RWMutex

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

	// syncInProgress prevents a wake-up, a manual refresh, and a startup catch-up
	// from running the same provider sync concurrently.
	syncInProgress   map[string]bool
	syncInProgressMu sync.Mutex

	// providerErrors holds the last startup error per provider instance (instanceID → message).
	// Populated in startup() when a provider fails IsAuthenticated or Connect.
	// Exposed via GetConfiguredProviders so the sidebar can show a warning badge without
	// relying on a one-shot event that might fire before the listener is registered.
	providerErrors   map[string]string
	providerErrorsMu sync.RWMutex

	// linkPreviewCache caches fetched Open Graph previews (1-hour TTL).
	linkPreviewCache   map[string]linkPreviewEntry
	linkPreviewCacheMu sync.RWMutex
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		eventCancels:   make(map[string]context.CancelFunc),
		syncInProgress: make(map[string]bool),
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

// persistMessageReceipt stores remote delivery/read acknowledgements so message
// status survives navigation and application restarts.
func persistMessageReceipt(event core.ReceiptEvent) {
	if db.DB == nil || (event.ReceiptType != core.ReceiptTypeDelivery && event.ReceiptType != core.ReceiptTypeRead) {
		return
	}

	var message models.Message
	err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", event.MessageID, event.ConversationID).First(&message).Error
	if err == gorm.ErrRecordNotFound {
		// WhatsApp may emit the chat as a LID while the message was normalized to
		// its phone-number JID. ProtocolMsgID is globally unique in Loom.
		err = db.DB.Where("protocol_msg_id = ?", event.MessageID).First(&message).Error
	}
	if err != nil {
		log.Printf("Warning: receipt for unknown message %s in %s: %v", event.MessageID, event.ConversationID, err)
		return
	}

	timestamp := time.Unix(event.Timestamp, 0)
	if event.Timestamp <= 0 {
		timestamp = time.Now()
	}
	var receipt models.MessageReceipt
	result := db.DB.Where(
		"message_id = ? AND user_id = ? AND receipt_type = ?",
		message.ID, event.UserID, string(event.ReceiptType),
	).First(&receipt)
	if result.Error == nil {
		if timestamp.After(receipt.Timestamp) {
			db.DB.Model(&receipt).Updates(map[string]interface{}{"timestamp": timestamp, "updated_at": time.Now()})
		}
		return
	}
	if result.Error != gorm.ErrRecordNotFound {
		log.Printf("Warning: failed to query receipt for message %s: %v", event.MessageID, result.Error)
		return
	}
	if err := db.DB.Create(&models.MessageReceipt{
		MessageID:   message.ID,
		UserID:      event.UserID,
		ReceiptType: string(event.ReceiptType),
		Timestamp:   timestamp,
	}).Error; err != nil {
		log.Printf("Warning: failed to persist %s receipt for message %s: %v", event.ReceiptType, event.MessageID, err)
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
		// Strip namespace prefix to get raw user ID for LinkedAccount lookup
		rawConvID := core.StripConvID(protocolConvID)
		instanceID := ""
		if idx := strings.Index(protocolConvID, "::"); idx > 0 {
			instanceID = protocolConvID[:idx]
		}

		// A raw WhatsApp JID can exist in several provider instances. Always use
		// the namespace carried by ProtocolConvID instead of selecting the first
		// matching LinkedAccount from another SIM/account.
		var linkedAccount models.LinkedAccount
		accountQuery := db.DB.Where("user_id = ?", rawConvID)
		if instanceID != "" {
			accountQuery = accountQuery.Where("provider_instance_id = ?", instanceID)
		}
		err := accountQuery.First(&linkedAccount).Error

		if err != nil {
			// LinkedAccount doesn't exist, skip this conversation for now
			// It will be created when a new message arrives
			fmt.Printf("[createMissingConversations] Skipping %s: LinkedAccount not found\n", protocolConvID)
			continue
		}

		// Check if Conversation already exists (use the namespaced ID as stored)
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
		isGroup := strings.HasPrefix(rawConvID, "C") || strings.HasPrefix(rawConvID, "G")
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
	// A namespaced ID carries its provider instance even when the conversation
	// has not been persisted yet (for example, a brand-new Slack DM).
	if idx := strings.Index(conversationID, "::"); idx > 0 && a.providerManager != nil {
		if p, err := a.providerManager.GetProvider(conversationID[:idx]); err == nil {
			return p
		}
	}

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

		// Contacts without any loaded message do not have a Conversation row
		// yet. Resolve their provider directly from the LinkedAccount instead
		// of incorrectly falling back to whichever provider is currently active.
		var accounts []models.LinkedAccount
		if a.providerManager != nil {
			if err := db.DB.
				Where("user_id = ?", core.StripConvID(conversationID)).
				Find(&accounts).Error; err == nil {
				var matchedProvider core.Provider
				matchedInstanceID := ""
				for _, account := range accounts {
					if account.ProviderInstanceID == "" || account.ProviderInstanceID == matchedInstanceID {
						continue
					}
					p, err := a.providerManager.GetProvider(account.ProviderInstanceID)
					if err != nil {
						continue
					}
					// Do not guess when the same raw account ID exists in several
					// configured provider instances.
					if matchedProvider != nil {
						return nil
					}
					matchedProvider = p
					matchedInstanceID = account.ProviderInstanceID
				}
				if matchedProvider != nil {
					return matchedProvider
				}
			}
		}
	}
	return a.getActiveProvider()
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize the database
	var databaseErr error
	if a.mockMode {
		databaseErr = db.InitMockDatabase()
	} else {
		databaseErr = db.InitDatabase()
	}
	if databaseErr != nil {
		log.Fatalf("Failed to initialize database: %v", databaseErr)
	}
	if a.mockMode {
		if err := seedMockData(); err != nil {
			log.Fatalf("Failed to seed mock data: %v", err)
		}
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
				"workspace_url": map[string]interface{}{
					"type":        "string",
					"title":       "Workspace URL",
					"description": "Your Slack workspace URL, e.g. mycompany.slack.com (optional — leave empty to open the generic Slack sign-in page)",
				},
				"token": map[string]interface{}{
					"type":        "string",
					"title":       "Auth Token (advanced)",
					"description": "Bot (xoxb-) or OAuth user (xoxp-) token. Leave empty when using browser login.",
				},
				"d_cookie": map[string]interface{}{
					"type":        "string",
					"title":       "d Cookie (advanced)",
					"description": "Required only for manual Client Token (xoxc-) authentication.",
				},
				"sync_days": map[string]interface{}{
					"type":        "number",
					"title":       "Sync Days",
					"description": "Only sync conversations with messages in the last X days (0 = no limit)",
					"default":     0,
					"minimum":     0,
				},
			},
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

	a.providerManager.RegisterProvider("googlemessages", core.ProviderInfo{
		ID:          "googlemessages",
		Name:        "Google Messages",
		Description: "Google Messages via a paired Android phone",
		ConfigSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}, func() core.Provider {
		return providers.NewGoogleMessagesProvider()
	})

	a.providerManager.RegisterProvider("teams", core.ProviderInfo{
		ID:          "teams",
		Name:        "Microsoft Teams",
		Description: "Microsoft Teams for work and school accounts",
		ConfigSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tenant": map[string]interface{}{
					"type":        "string",
					"title":       "Tenant (optional)",
					"description": "Tenant GUID or verified domain. Leave empty to use your default organization.",
				},
			},
		},
	}, func() core.Provider {
		return providers.NewTeamsProvider()
	})

	if a.mockMode {
		a.setupSystemTray(ctx)
		return
	}

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
				a.setProviderError(instanceID, fmt.Sprintf("Connection failed: %v", err))
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
			a.setProviderError(instanceID, "Session expired — please re-authenticate")
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
	unlockEvents := systemSessionUnlockEvents()
	for {
		select {
		case <-unlockEvents:
			log.Printf("[WakeDetector] Session unlock detected. Triggering resync...")
			go a.resyncAllProviders()
		case <-ticker.C:
			// Do not use the timestamp carried by ticker.C here. After system
			// sleep, that value may be a stale tick which was buffered before
			// the machine woke up and would hide the actual elapsed time.
			now := time.Now()
			// If more than 30 seconds have passed since the last 10-second tick,
			// the computer was likely asleep or the system was heavily throttled.
			resumeReason := ""
			if now.Sub(lastTick) > 30*time.Second {
				resumeReason = fmt.Sprintf("system wake (gap: %v)", now.Sub(lastTick))
			}
			if resumeReason != "" {
				log.Printf("[WakeDetector] %s detected. Triggering resync...", resumeReason)
				// Do not block the detector: the catch-up routine waits for the
				// network and retries transient failures in the background.
				go a.resyncAllProviders()
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
		if _, err := a.providerManager.GetProvider(pInfo.InstanceID); err != nil {
			continue
		}

		log.Printf("[App] Triggering catch-up sync for %s", pInfo.InstanceID)
		if pInfo.ID == "teams" || pInfo.ID == "googlemessages" {
			go a.resyncProviderAfterWake(pInfo.InstanceID)
		} else {
			go a.syncProviderHistory(pInfo.InstanceID, a.syncSince(pInfo.InstanceID, 24*time.Hour), "system wake")
		}
	}
}

// resyncProviderAfterWake performs a second pass using the exact same lower
// bound. Teams and Google Messages can accept requests as soon as Wi-Fi is
// back while their server-side conversation view or phone relay is still
// catching up. Advancing LastSyncAt after that first, apparently successful,
// empty response would otherwise leave a permanent hole until a manual full
// synchronization.
func (a *App) resyncProviderAfterWake(instanceID string) {
	since := a.syncSince(instanceID, 24*time.Hour)
	a.syncProviderHistory(instanceID, since, "system wake")

	select {
	case <-time.After(30 * time.Second):
	case <-a.ctx.Done():
		return
	}
	a.syncProviderHistory(instanceID, since, "post-wake verification")
}

// syncSince returns the last successful sync with a small overlap. The overlap makes
// sync idempotent while covering messages delivered around a sleep or network loss.
func (a *App) syncSince(instanceID string, fallback time.Duration) time.Time {
	since := time.Now().Add(-fallback)
	if db.DB == nil {
		return since
	}

	var config models.ProviderConfiguration
	if err := db.DB.Where("instance_id = ?", instanceID).First(&config).Error; err == nil && config.LastSyncAt != nil {
		candidate := config.LastSyncAt.Add(-5 * time.Minute)
		if candidate.After(since) {
			return candidate
		}
	}
	return since
}

// syncProviderHistory serializes syncs per provider and retries a failed sync after
// reconnecting. A laptop often resumes before its network is usable; treating that
// first error as final used to leave the provider permanently stale or in error.
func (a *App) syncProviderHistory(instanceID string, since time.Time, reason string) {
	a.syncInProgressMu.Lock()
	if a.syncInProgress[instanceID] {
		a.syncInProgressMu.Unlock()
		log.Printf("[App] Ignoring %s sync for %s: one is already running", reason, instanceID)
		return
	}
	a.syncInProgress[instanceID] = true
	a.syncInProgressMu.Unlock()
	defer func() {
		a.syncInProgressMu.Lock()
		delete(a.syncInProgress, instanceID)
		a.syncInProgressMu.Unlock()
	}()

	// Give the operating system a moment to restore Wi-Fi/VPN after waking.
	if reason == "system wake" {
		select {
		case <-time.After(5 * time.Second):
		case <-a.ctx.Done():
			return
		}
	}

	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		a.setProviderError(instanceID, fmt.Sprintf("Sync unavailable: %v", err))
		return
	}

	// A socket can still look connected to the application after a laptop wake
	// even though the underlying network session is dead. Reconnect before the
	// catch-up sync so both missed messages and subsequent live events resume.
	if reason == "system wake" {
		_ = provider.Disconnect()
		if err := provider.Connect(); err != nil {
			log.Printf("[App] %s initial reconnect for %s failed: %v", reason, instanceID, err)
		} else {
			a.startEventListenerForProvider(a.ctx, instanceID, provider)
		}
	}

	var lastErr error
	for attempt, delay := range []time.Duration{0, 5 * time.Second, 15 * time.Second} {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-a.ctx.Done():
				return
			}
			// Reset stale network sessions before retrying. Providers own their
			// connection lifecycle; restarting the event listener keeps Wails
			// attached to the restored event stream.
			_ = provider.Disconnect()
			if err := provider.Connect(); err != nil {
				lastErr = fmt.Errorf("reconnect: %w", err)
				log.Printf("[App] %s reconnect attempt %d for %s failed: %v", reason, attempt+1, instanceID, err)
				continue
			}
			a.startEventListenerForProvider(a.ctx, instanceID, provider)
		}

		if err := provider.SyncHistory(since); err == nil {
			if db.DB != nil {
				db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Update("last_sync_at", time.Now())
			}
			a.clearProviderError(instanceID)
			return
		} else {
			lastErr = err
			log.Printf("[App] %s sync attempt %d for %s failed: %v", reason, attempt+1, instanceID, err)
		}
	}

	if lastErr != nil {
		message := fmt.Sprintf("Synchronization failed: %v", lastErr)
		a.setProviderError(instanceID, message)
		a.emitSyncStatusCoordinated(core.SyncStatusEvent{InstanceID: instanceID, Status: core.SyncStatusError, Message: message, Progress: -1})
	}
}

// domReady is called when the frontend DOM is ready. It is the right place to start
// operations that emit events to the frontend, since the Wails IPC and React's EventsOn
// listeners are guaranteed to be active by this point.
func (a *App) setProviderError(instanceID, message string) {
	a.providerErrorsMu.Lock()
	defer a.providerErrorsMu.Unlock()
	if a.providerErrors == nil {
		a.providerErrors = make(map[string]string)
	}
	a.providerErrors[instanceID] = message
}

func (a *App) clearProviderError(instanceID string) {
	a.providerErrorsMu.Lock()
	defer a.providerErrorsMu.Unlock()
	delete(a.providerErrors, instanceID)
}

func (a *App) domReady(ctx context.Context) {
	syncs := a.pendingSyncs
	a.pendingSyncs = nil

	for _, si := range syncs {
		go func(syncInfo pendingSyncInfo) {
			// Small delay to let React mount and register EventsOn("sync-status") listener.
			time.Sleep(500 * time.Millisecond)
			fmt.Printf("App.domReady: starting sync for %s since %s\n", syncInfo.instanceID, syncInfo.since.Format(time.RFC3339))
			a.syncProviderHistory(syncInfo.instanceID, syncInfo.since, "startup")
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
					if len(e.Messages) > maxFrontendSyncBatchMessages {
						e.Messages = e.Messages[len(e.Messages)-maxFrontendSyncBatchMessages:]
					}
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
					a.invalidateMessageCaches()
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
					a.emitContactsRefresh()
					groupChangeJSON, _ := json.Marshal(e)
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "group-change", string(groupChangeJSON))
					}
				case core.ReceiptEvent:
					persistMessageReceipt(e)
					a.invalidateMessageCaches()
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
		// Provider is active again — clear any startup error so the sidebar badge disappears.
		a.clearProviderError(e.InstanceID)

	case core.SyncStatusCompleted:
		delete(a.syncingProviders, e.InstanceID)
		stillSyncing := len(a.syncingProviders) > 0
		a.syncingProvidersMu.Unlock()
		a.clearProviderError(e.InstanceID)

		if stillSyncing {
			// At least one other provider is still active — swallow this event.
			// The frontend will receive "completed" once the last provider finishes.
			return
		}

	case core.SyncStatusError:
		delete(a.syncingProviders, e.InstanceID)
		a.syncingProvidersMu.Unlock()

	case core.SyncStatusNeedsReauth:
		delete(a.syncingProviders, e.InstanceID)
		a.syncingProvidersMu.Unlock()
		// Persist the error so the orange badge appears in the providers list
		// immediately (picked up by GetConfiguredProviders on next call).
		a.setProviderError(e.InstanceID, e.Message)

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
	return map[string]interface{}{"theme": "dark", "mockMode": a.mockMode}
}

func (a *App) SaveConfig(config map[string]interface{}) error { return nil }

// GetConfiguredProviders returns a list of configured providers with their status.
// Each entry's SyncError field is non-empty when the provider failed to authenticate
// or connect at startup, so the frontend can show a warning badge immediately on mount.
func (a *App) GetConfiguredProviders() ([]core.ProviderInfo, error) {
	if a.mockMode {
		return mockProviderInfos(), nil
	}
	if a.providerManager == nil {
		return []core.ProviderInfo{}, nil
	}
	providers := a.providerManager.GetConfiguredProviders()
	a.providerErrorsMu.RLock()
	defer a.providerErrorsMu.RUnlock()
	for i := range providers {
		if msg, ok := a.providerErrors[providers[i].InstanceID]; ok {
			providers[i].SyncError = msg
		}
	}
	return providers, nil
}

// For frontend compatibility if GetConfiguredProviders doesn't return exactly what's needed
// We might need to wrap it differently, but assuming PM has it based on provider_manager.go

func (a *App) GetAvailableProviders() ([]core.ProviderInfo, error) {
	if a.mockMode {
		return []core.ProviderInfo{}, nil
	}
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
		a.syncProviderHistory(capturedID, a.syncSince(capturedID, 30*24*time.Hour), "initial")
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

// GetProviderContacts returns people from one provider instance, never groups or
// channels. Calling the provider first also refreshes directory-backed providers.
func (a *App) GetProviderContacts(instanceID string) ([]models.MetaContact, error) {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return nil, err
	}
	accounts, err := provider.GetContacts()
	if err != nil {
		return nil, err
	}
	// Include directory-only contacts persisted by remote people pickers.
	accounts = append(accounts, db.ContactStore.FindByProvider(instanceID)...)
	contacts := providerAccountsToMetaContacts(instanceID, accounts)
	refreshContactStatuses(provider, contacts)
	return contacts, nil
}

// SearchProviderContacts supports remote people pickers (Teams) while using
// the already synchronized directory for providers such as WhatsApp and Slack.
func (a *App) SearchProviderContacts(instanceID, query string) ([]models.MetaContact, error) {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return nil, err
	}
	if searcher, ok := provider.(core.ContactSearcher); ok {
		accounts, err := searcher.SearchContacts(query)
		if err != nil {
			return nil, err
		}
		return providerAccountsToMetaContacts(instanceID, accounts), nil
	}
	contacts, err := a.GetProviderContacts(instanceID)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return contacts, nil
	}
	filtered := contacts[:0]
	for _, contact := range contacts {
		if strings.Contains(strings.ToLower(contact.DisplayName), query) {
			filtered = append(filtered, contact)
		}
	}
	refreshContactStatuses(provider, filtered)
	return filtered, nil
}

func refreshContactStatuses(provider core.Provider, contacts []models.MetaContact) {
	refresher, ok := provider.(core.ContactStatusRefresher)
	if !ok {
		return
	}
	limit := min(30, len(contacts))
	userIDs := make([]string, 0, limit)
	for _, contact := range contacts[:limit] {
		if len(contact.LinkedAccounts) > 0 && !contact.LinkedAccounts[0].IsGroup {
			userIDs = append(userIDs, contact.LinkedAccounts[0].UserID)
		}
	}
	statuses := refresher.RefreshContactStatuses(userIDs)
	for index := range contacts {
		if len(contacts[index].LinkedAccounts) == 0 {
			continue
		}
		if status, exists := statuses[contacts[index].LinkedAccounts[0].UserID]; exists {
			contacts[index].LinkedAccounts[0].Status = status
		}
	}
}

func providerAccountsToMetaContacts(instanceID string, accounts []models.LinkedAccount) []models.MetaContact {
	contacts := make([]models.MetaContact, 0, len(accounts))
	seen := make(map[string]bool)
	for _, account := range accounts {
		// Teams historically persisted DMs by their technical thread ID. The
		// canonical contact is now the participant MRI; hide stale legacy rows.
		if account.Protocol == "teams" && !account.IsGroup && strings.HasPrefix(account.UserID, "19:") {
			continue
		}
		if account.IsGroup || account.UserID == "" || seen[account.UserID] {
			continue
		}
		seen[account.UserID] = true
		account.ProviderInstanceID = instanceID
		// Providers generally expose raw remote IDs, while messages and the
		// timestamp cache use namespaced conversation IDs. Normalize here so
		// contact recency works identically for initial and searched results.
		account.ConversationID = core.BuildConvID(instanceID, account.ConversationID)
		if stored, ok := db.ContactStore.FindByProviderUser(instanceID, account.UserID); ok {
			account.ID = stored.ID
			account.MetaContactID = stored.MetaContactID
			if account.Status == "" {
				account.Status = stored.Status
			}
			if account.Extra == "" {
				account.Extra = stored.Extra
			}
			if account.ConversationID == "" {
				account.ConversationID = db.ContactStore.GetConversation(stored.ID)
			}
			if meta, ok := db.ContactStore.FindMetaContact(stored.MetaContactID); ok {
				if account.Username == "" {
					account.Username = meta.DisplayName
				}
				contacts = append(contacts, models.MetaContact{ID: meta.ID, DisplayName: meta.DisplayName, AvatarURL: meta.AvatarURL, LinkedAccounts: []models.LinkedAccount{account}})
				continue
			}
		}
		name := account.Username
		if name == "" {
			name = account.UserID
		}
		contacts = append(contacts, models.MetaContact{DisplayName: name, AvatarURL: account.AvatarURL, LinkedAccounts: []models.LinkedAccount{account}})
	}
	lastExchange := make(map[string]time.Time)
	conversationIDs := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if len(contact.LinkedAccounts) > 0 && contact.LinkedAccounts[0].ConversationID != "" {
			conversationIDs = append(conversationIDs, contact.LinkedAccounts[0].ConversationID)
		}
	}
	if db.DB != nil && len(conversationIDs) > 0 {
		var rows []struct {
			ProtocolConvID string
			LastExchange   time.Time
		}
		db.DB.Model(&models.Message{}).
			Select("protocol_conv_id, MAX(timestamp) AS last_exchange").
			Where("protocol_conv_id IN ? AND deleted_at IS NULL", conversationIDs).
			Group("protocol_conv_id").Scan(&rows)
		for _, row := range rows {
			lastExchange[row.ProtocolConvID] = row.LastExchange
		}
	}
	sort.SliceStable(contacts, func(i, j int) bool {
		iConv, jConv := contacts[i].LinkedAccounts[0].ConversationID, contacts[j].LinkedAccounts[0].ConversationID
		iTime, jTime := lastExchange[iConv], lastExchange[jConv]
		if !iTime.Equal(jTime) {
			return iTime.After(jTime)
		}
		return strings.ToLower(contacts[i].DisplayName) < strings.ToLower(contacts[j].DisplayName)
	})
	return contacts
}

func sameParticipantSet(selected map[string]bool, participants []models.GroupParticipant, directoryUsers map[string]bool, selfID string) bool {
	found := make(map[string]bool)
	for _, participant := range participants {
		if selfID != "" && strings.EqualFold(participant.UserID, selfID) {
			continue
		}
		// Providers commonly return the authenticated user as a participant. It is
		// absent from the selectable directory, so ignore it here.
		if selfID != "" || directoryUsers[participant.UserID] {
			found[participant.UserID] = true
		}
	}
	if len(found) != len(selected) {
		return false
	}
	for id := range selected {
		if !found[id] {
			return false
		}
	}
	return true
}

// OpenConversation reuses exact existing conversations and only creates when
// there is no match. Multiple matches are returned for an explicit user choice.
func (a *App) OpenConversation(request models.OpenConversationRequest) (models.ConversationResolution, error) {
	resolution := models.ConversationResolution{Matches: []models.MetaContact{}}
	if request.ProviderInstanceID == "" || len(request.ParticipantIDs) == 0 {
		return resolution, fmt.Errorf("provider and at least one participant are required")
	}
	provider, err := a.providerManager.GetProvider(request.ProviderInstanceID)
	if err != nil {
		return resolution, err
	}
	caps := provider.GetCapabilities()
	selfID := ""
	if withCurrentUser, ok := provider.(core.CurrentUserProvider); ok {
		selfID = withCurrentUser.CurrentUserID()
	}
	directory, err := a.GetProviderContacts(request.ProviderInstanceID)
	if err != nil {
		return resolution, err
	}
	directoryUsers := make(map[string]bool, len(directory))
	selected := make(map[string]bool, len(request.ParticipantIDs))
	for _, contact := range directory {
		if len(contact.LinkedAccounts) > 0 {
			directoryUsers[contact.LinkedAccounts[0].UserID] = true
		}
	}
	for _, id := range request.ParticipantIDs {
		if !directoryUsers[id] {
			return resolution, fmt.Errorf("participant %s does not belong to provider %s", id, request.ProviderInstanceID)
		}
		selected[id] = true
	}

	if len(selected) == 1 {
		for _, contact := range directory {
			account := contact.LinkedAccounts[0]
			if selected[account.UserID] && account.ConversationID != "" {
				resolution.Matches = append(resolution.Matches, contact)
				return resolution, nil
			}
		}
		if !caps.SupportsDirectConversation {
			return resolution, fmt.Errorf("provider does not support direct conversation creation")
		}
		for _, contact := range directory {
			account := contact.LinkedAccounts[0]
			if selected[account.UserID] {
				if creator, ok := provider.(core.DirectConversationCreator); ok {
					conversation, err := creator.CreateDirectConversation(account.UserID)
					if err != nil {
						return resolution, err
					}
					account.ConversationID = core.BuildConvID(request.ProviderInstanceID, conversation.ProtocolConvID)
				} else {
					account.ConversationID = core.BuildConvID(request.ProviderInstanceID, account.UserID)
				}
				contact.LinkedAccounts[0] = account
				resolution.Created = &contact
				return resolution, nil
			}
		}
	}

	for _, account := range db.ContactStore.FindByProvider(request.ProviderInstanceID) {
		if !account.IsGroup {
			continue
		}
		convID := db.ContactStore.GetConversation(account.ID)
		if convID == "" {
			continue
		}
		var storedConversation models.Conversation
		if err := db.DB.Where("protocol_conv_id = ?", convID).First(&storedConversation).Error; err == nil &&
			storedConversation.ConversationType != "" && request.ConversationType != "" &&
			storedConversation.ConversationType != request.ConversationType {
			continue
		}
		participants, err := provider.GetGroupParticipants(core.StripConvID(convID))
		if err != nil || !sameParticipantSet(selected, participants, directoryUsers, selfID) {
			continue
		}
		if meta, ok := db.ContactStore.FindMetaContact(account.MetaContactID); ok {
			account.ConversationID = convID
			meta.LinkedAccounts = []models.LinkedAccount{account}
			resolution.Matches = append(resolution.Matches, meta)
		}
	}
	if len(resolution.Matches) > 0 {
		return resolution, nil
	}
	if !caps.SupportsGroupConversation {
		return resolution, fmt.Errorf("provider does not support group conversation creation")
	}
	if caps.RequiresGroupTitle && request.ConversationType != "group_message" && strings.TrimSpace(request.Title) == "" {
		return resolution, fmt.Errorf("this conversation type requires a title")
	}
	var conversation *models.Conversation
	if creator, ok := provider.(core.ConversationCreator); ok {
		conversation, err = creator.CreateConversation(request.ConversationType, request.Title, request.ParticipantIDs)
	} else {
		conversation, err = provider.CreateGroup(request.Title, request.ParticipantIDs)
	}
	if err != nil {
		return resolution, err
	}
	protocol := ""
	if len(directory) > 0 && len(directory[0].LinkedAccounts) > 0 {
		protocol = directory[0].LinkedAccounts[0].Protocol
	}
	created, err := a.persistCreatedConversation(request.ProviderInstanceID, protocol, request.ConversationType, request.Title, conversation)
	if err != nil {
		return resolution, err
	}
	resolution.Created = created
	a.emitContactsRefresh()
	return resolution, nil
}

func (a *App) persistCreatedConversation(instanceID, protocol, conversationType, title string, conversation *models.Conversation) (*models.MetaContact, error) {
	if db.DB == nil || conversation == nil || conversation.ProtocolConvID == "" {
		return nil, fmt.Errorf("provider returned an invalid conversation")
	}
	conversation.ProtocolConvID = core.BuildConvID(instanceID, conversation.ProtocolConvID)
	conversation.ConversationType = conversationType
	if conversation.GroupName == "" {
		conversation.GroupName = title
	}
	var meta models.MetaContact
	var account models.LinkedAccount
	userID := core.StripConvID(conversation.ProtocolConvID)
	persist := func() error {
		return db.DB.Transaction(func(tx *gorm.DB) error {
			account = models.LinkedAccount{}
			result := tx.Where("provider_instance_id = ? AND user_id = ?", instanceID, userID).First(&account)
			if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
				return result.Error
			}
			if result.Error == gorm.ErrRecordNotFound {
				meta = models.MetaContact{DisplayName: conversation.GroupName}
				if err := tx.Create(&meta).Error; err != nil {
					return err
				}
				account = models.LinkedAccount{
					MetaContactID: meta.ID, Protocol: protocol, ProviderInstanceID: instanceID,
					UserID: userID, Username: conversation.GroupName, IsGroup: true, Status: "offline",
				}
				if err := tx.Create(&account).Error; err != nil {
					return err
				}
			} else {
				account.IsGroup = true
				if protocol != "" {
					account.Protocol = protocol
				}
				if conversation.GroupName != "" {
					account.Username = conversation.GroupName
				}
				if account.MetaContactID == 0 {
					meta = models.MetaContact{DisplayName: account.Username}
					if err := tx.Create(&meta).Error; err != nil {
						return err
					}
					account.MetaContactID = meta.ID
				} else if err := tx.First(&meta, account.MetaContactID).Error; err != nil {
					return err
				}
				if conversation.GroupName != "" {
					meta.DisplayName = conversation.GroupName
				}
				if err := tx.Save(&meta).Error; err != nil {
					return err
				}
				if err := tx.Save(&account).Error; err != nil {
					return err
				}
			}

			var storedConversation models.Conversation
			result = tx.Where("protocol_conv_id = ?", conversation.ProtocolConvID).First(&storedConversation)
			if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
				return result.Error
			}
			if result.Error == gorm.ErrRecordNotFound {
				conversation.LinkedAccountID = account.ID
				return tx.Create(conversation).Error
			}
			storedConversation.LinkedAccountID = account.ID
			storedConversation.IsGroup = conversation.IsGroup
			storedConversation.ConversationType = conversation.ConversationType
			storedConversation.GroupName = conversation.GroupName
			if err := tx.Save(&storedConversation).Error; err != nil {
				return err
			}
			*conversation = storedConversation
			return nil
		})
	}
	err := persist()
	// A realtime provider event may insert the same remote conversation between
	// our initial lookup and insert. The transaction rolls back cleanly; retrying
	// then follows the existing-record path above.
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		err = persist()
	}
	if err != nil {
		return nil, err
	}
	db.ContactStore.UpsertMetaContact(meta)
	db.ContactStore.UpsertLinkedAccount(account)
	db.ContactStore.UpsertConversation(account.ID, conversation.ProtocolConvID)
	account.ConversationID = conversation.ProtocolConvID
	meta.LinkedAccounts = []models.LinkedAccount{account}
	return &meta, nil
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
		Preload("Receipts").
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
				go func() {
					if err := provider.RefreshContact(conversationID); err != nil {
						fmt.Printf("App.GetMessagesForConversation: failed to refresh contact %s: %v\n", conversationID, err)
					}
				}()
				return fetchedMessages, nil
			}
			if err != nil {
				fmt.Printf("GetMessagesForConversation: failed to fetch history from provider: %v\n", err)
			}
		}
	}

	// Enrich messages with sender names from LinkedAccount
	a.enrichMessagesWithSenderNames(messages)

	// Trigger avatar/metadata refresh for the conversation's provider when opening a conversation
	if provider := a.getProviderForConversation(conversationID); provider != nil {
		go func() {
			if err := provider.RefreshContact(conversationID); err != nil {
				fmt.Printf("App.GetMessagesForConversation: failed to refresh contact %s: %v\n", conversationID, err)
			}
		}()
	}

	return messages, err
}

// SearchMessages searches persisted message bodies, newest first. Offset-based
// pagination is sufficient here because search pages are deliberately small.
func (a *App) SearchMessages(query string, offset int) (models.MessageSearchPage, error) {
	const pageSize = 15
	page := models.MessageSearchPage{Items: []models.MessageSearchResult{}}
	query = strings.TrimSpace(query)
	if db.DB == nil || len([]rune(query)) < 3 {
		return page, nil
	}
	if offset < 0 {
		offset = 0
	}

	var messages []models.Message
	escapedQuery := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(strings.ToLower(query))
	pattern := "%" + escapedQuery + "%"
	err := db.DB.
		Where(`deleted_at IS NULL AND is_deleted = ? AND LOWER(body) LIKE ? ESCAPE '\'`, false, pattern).
		Order("timestamp DESC").
		Limit(pageSize + 1).
		Offset(offset).
		Find(&messages).Error
	if err != nil {
		return page, err
	}
	if len(messages) > pageSize {
		page.HasMore = true
		messages = messages[:pageSize]
	}

	type conversationMeta struct {
		ConversationID     uint
		MetaContactID      uint
		ConversationName   string
		ConversationAvatar string
		Protocol           string
		ProviderInstanceID string
	}
	var metadata []conversationMeta
	conversationIDs := make([]uint, 0, len(messages))
	for _, message := range messages {
		conversationIDs = append(conversationIDs, message.ConversationID)
	}
	err = db.DB.Table("conversations AS c").
		Select(`c.id AS conversation_id,
			mc.id AS meta_contact_id,
			mc.display_name AS conversation_name,
			mc.avatar_url AS conversation_avatar,
			la.protocol,
			la.provider_instance_id`).
		Joins("JOIN linked_accounts AS la ON la.id = c.linked_account_id").
		Joins("JOIN meta_contacts AS mc ON mc.id = la.meta_contact_id").
		Where("c.id IN ?", conversationIDs).
		Scan(&metadata).Error
	if err != nil {
		return page, err
	}
	byConversation := make(map[uint]conversationMeta, len(metadata))
	for _, meta := range metadata {
		byConversation[meta.ConversationID] = meta
	}
	for _, message := range messages {
		meta, ok := byConversation[message.ConversationID]
		if !ok {
			continue
		}
		page.Items = append(page.Items, models.MessageSearchResult{
			Message:            message,
			MetaContactID:      meta.MetaContactID,
			ConversationName:   meta.ConversationName,
			ConversationAvatar: meta.ConversationAvatar,
			Protocol:           meta.Protocol,
			ProviderInstanceID: meta.ProviderInstanceID,
		})
	}
	return page, nil
}

// GetMessagesForConversationBefore returns messages before a specific timestamp for pagination
// GetThreadMessages retrieves all messages in a thread
func (a *App) GetThreadMessages(conversationID string, threadID string) ([]models.Message, error) {
	fmt.Printf("[GetThreadMessages] Getting thread messages for conversation %s, thread %s\n", conversationID, threadID)

	// First try to load from database
	var messages []models.Message
	if db.DB != nil {
		err := db.DB.Where("thread_id = ?", threadID).
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
	if provider := a.getProviderForConversation(conversationID); provider != nil {
		fmt.Printf("[GetThreadMessages] Fetching thread messages from provider for conversation %s, thread %s\n", conversationID, threadID)
		fetchedMessages, err := provider.GetThreads(threadID)
		if err == nil && len(fetchedMessages) > 0 {
			a.enrichMessagesWithSenderNames(fetchedMessages)
			return fetchedMessages, nil
		}
	}

	fmt.Printf("[GetThreadMessages] Thread messages not found in database or provider, returning empty list\n")
	return []models.Message{}, nil
}

func (a *App) GetMessagesForConversationBefore(conversationID string, beforeTimestamp time.Time) ([]models.Message, error) {
	fmt.Printf("[GetMessagesForConversationBefore] Loading messages for %s before %v\n", conversationID, beforeTimestamp)
	const limit = 50

	var messages []models.Message
	var err error

	if db.DB != nil {
		err = db.DB.Where("protocol_conv_id = ? AND timestamp < ?", conversationID, beforeTimestamp).
			Preload("Receipts").
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

func (a *App) ScheduleMessage(conversationID, content string, scheduledAt time.Time) (*models.ScheduledMessage, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	scheduler, ok := provider.(core.ScheduledMessageProvider)
	if !ok || !provider.GetCapabilities().SupportsScheduledMessages {
		return nil, fmt.Errorf("scheduled messages are not supported for this provider")
	}
	return scheduler.ScheduleMessage(conversationID, content, scheduledAt)
}

func (a *App) GetScheduledMessages(conversationID string) ([]models.ScheduledMessage, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	scheduler, ok := provider.(core.ScheduledMessageProvider)
	if !ok || !provider.GetCapabilities().SupportsListScheduledMessages {
		return nil, fmt.Errorf("listing scheduled messages is not supported for this provider")
	}
	return scheduler.ListScheduledMessages(conversationID)
}

func (a *App) CancelScheduledMessage(conversationID, scheduledMessageID string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	scheduler, ok := provider.(core.ScheduledMessageProvider)
	if !ok || !provider.GetCapabilities().SupportsScheduledMessages {
		return fmt.Errorf("scheduled messages are not supported for this provider")
	}
	return scheduler.CancelScheduledMessage(conversationID, scheduledMessageID)
}

// PinMessage pins a provider message and persists the provider-neutral pin metadata.
func (a *App) PinMessage(conversationID, messageID string) (*models.MessagePin, error) {
	provider := a.getProviderForConversation(conversationID)
	pinProvider, ok := provider.(core.MessagePinProvider)
	if !ok || !provider.GetCapabilities().SupportsPinMessage {
		return nil, fmt.Errorf("message pinning is not supported for this provider")
	}
	pin, err := pinProvider.PinMessage(conversationID, messageID)
	if err != nil {
		return nil, err
	}
	if pin.ProviderInstanceID == "" {
		if idx := strings.Index(conversationID, "::"); idx > 0 {
			pin.ProviderInstanceID = conversationID[:idx]
		}
	}
	if pin.Resolution == "" {
		pin.Resolution = models.MessagePinResolutionUnresolved
	}
	if db.DB != nil {
		if err := db.DB.Where("provider_instance_id = ? AND protocol_msg_id = ?", pin.ProviderInstanceID, pin.ProtocolMsgID).
			Assign(*pin).FirstOrCreate(pin).Error; err != nil {
			return nil, err
		}
	}
	return pin, nil
}

// UnpinMessage removes both the remote pin and Loom's persisted metadata.
func (a *App) UnpinMessage(conversationID, messageID string) error {
	provider := a.getProviderForConversation(conversationID)
	pinProvider, ok := provider.(core.MessagePinProvider)
	if !ok || !provider.GetCapabilities().SupportsPinMessage {
		return fmt.Errorf("message pinning is not supported for this provider")
	}
	if err := pinProvider.UnpinMessage(conversationID, messageID); err != nil {
		return err
	}
	if db.DB != nil {
		return db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", conversationID, messageID).Delete(&models.MessagePin{}).Error
	}
	return nil
}

// GetPinnedMessages refreshes provider pin state and resolves locally available messages.
func (a *App) GetPinnedMessages(conversationID string) ([]models.MessagePin, error) {
	provider := a.getProviderForConversation(conversationID)
	pinProvider, ok := provider.(core.MessagePinProvider)
	if !ok || !provider.GetCapabilities().SupportsListMessagePins {
		return []models.MessagePin{}, nil
	}
	pins, err := pinProvider.ListMessagePins(conversationID)
	if err != nil {
		return nil, err
	}
	for i := range pins {
		if pins[i].Resolution == "" {
			pins[i].Resolution = models.MessagePinResolutionUnresolved
		}
		if pins[i].Message == nil && db.DB != nil {
			var message models.Message
			if db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", conversationID, pins[i].ProtocolMsgID).
				Preload("Receipts").Preload("Reactions").First(&message).Error == nil {
				pins[i].Message = &message
				pins[i].MessageTimestamp = &message.Timestamp
				pins[i].Resolution = models.MessagePinResolutionResolved
			}
		}
		if db.DB != nil {
			_ = db.DB.Where("provider_instance_id = ? AND protocol_msg_id = ?", pins[i].ProviderInstanceID, pins[i].ProtocolMsgID).
				Assign(pins[i]).FirstOrCreate(&pins[i]).Error
		}
	}
	return pins, nil
}

// GetPinnedMessageContext resolves a pin independently of the currently loaded
// renderer history and returns a bounded window around it.
func (a *App) GetPinnedMessageContext(conversationID, messageID string) (*models.MessageContext, error) {
	provider := a.getProviderForConversation(conversationID)
	pinProvider, ok := provider.(core.MessagePinProvider)
	if !ok {
		return nil, fmt.Errorf("message pinning is not supported for this provider")
	}
	var target models.Message
	resolvedRemotely := false
	if db.DB == nil || db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", conversationID, messageID).
		Preload("Receipts").Preload("Reactions").First(&target).Error != nil {
		resolved, err := pinProvider.ResolvePinnedMessage(conversationID, messageID)
		if err != nil {
			return nil, err
		}
		target = *resolved
		resolvedRemotely = true
	}
	const side = 25
	// A directly resolved old message may be the only local row in that period.
	// Ask the provider for both sides before building the bounded DB window.
	beforeTarget := target.Timestamp.Add(time.Nanosecond)
	_, _ = provider.GetConversationHistory(conversationID, side, &beforeTarget, nil)
	before := []models.Message{}
	after := []models.Message{}
	if db.DB != nil {
		_ = db.DB.Where("protocol_conv_id = ? AND timestamp < ?", conversationID, target.Timestamp).
			Preload("Receipts").Preload("Reactions").Order("timestamp DESC").Limit(side + 1).Find(&before).Error
		if !resolvedRemotely {
			_ = db.DB.Where("protocol_conv_id = ? AND timestamp > ?", conversationID, target.Timestamp).
				Preload("Receipts").Preload("Reactions").Order("timestamp ASC").Limit(side + 1).Find(&after).Error
		}
	}
	hasBefore, hasAfter := len(before) > side, len(after) > side
	if hasBefore {
		before = before[:side]
	}
	if hasAfter {
		after = after[:side]
	}
	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}
	messages := append(before, target)
	messages = append(messages, after...)
	a.enrichMessagesWithSenderNames(messages)
	return &models.MessageContext{TargetMessageID: messageID, Messages: messages, HasMoreBefore: hasBefore, HasMoreAfter: hasAfter}, nil
}

// SendReply sends a quoted reply to a message in the main conversation thread
func (a *App) SendReply(conversationID string, content string, quotedMessageID string) (*models.Message, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.SendReply(conversationID, content, quotedMessageID)
}

// SendThreadMessage sends a reply inside a thread
func (a *App) SendThreadMessage(conversationID string, content string, threadID string) (*models.Message, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.SendMessage(conversationID, content, nil, &threadID)
}

// SendThreadReply sends a quoted reply to a specific message inside a thread
func (a *App) SendThreadReply(conversationID string, content string, threadID string, quotedMessageID string) (*models.Message, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.SendThreadReply(conversationID, content, threadID, quotedMessageID)
}

func (a *App) SendFile(conversationID string, base64Data string, filename string, mimeType string) error {
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

// ForwardAttachment lets a provider preserve a remote attachment when it has
// a more appropriate native representation (for example a SharePoint link).
// Providers without such support use the regular download-and-upload path.
func (a *App) ForwardAttachment(conversationID, sourceURL, filename, mimeType string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	type attachmentForwarder interface {
		ForwardAttachment(string, string, string, string) error
	}
	if forwarder, ok := provider.(attachmentForwarder); ok {
		return forwarder.ForwardAttachment(conversationID, sourceURL, filename, mimeType)
	}
	data, err := a.GetAttachmentData(sourceURL)
	if err != nil {
		return err
	}
	return a.SendFile(conversationID, data, filename, mimeType)
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
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.GetGroupParticipants(conversationID)
}

func (a *App) AddGroupParticipants(conversationID string, participantIDs []string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	if !provider.GetCapabilities().SupportsAddGroupMembers {
		return fmt.Errorf("provider does not support adding group members")
	}
	if err := provider.AddGroupParticipants(conversationID, participantIDs); err != nil {
		return err
	}
	a.emitContactsRefresh()
	return nil
}

func (a *App) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	if !provider.GetCapabilities().SupportsRemoveGroupMembers {
		return fmt.Errorf("provider does not support removing group members")
	}
	if err := provider.RemoveGroupParticipants(conversationID, participantIDs); err != nil {
		return err
	}
	a.emitContactsRefresh()
	return nil
}

func (a *App) GetGroupDetails(conversationID string) (*models.GroupDetails, error) {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return nil, fmt.Errorf("no provider for conversation %s", conversationID)
	}
	detailsProvider, ok := provider.(core.GroupDetailsProvider)
	if !ok {
		return nil, fmt.Errorf("provider does not expose group details")
	}
	return detailsProvider.GetGroupDetails(conversationID)
}

func (a *App) UpdateGroupName(conversationID, name string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil || !provider.GetCapabilities().SupportsRenameGroup {
		return fmt.Errorf("provider does not support renaming groups")
	}
	name = strings.TrimSpace(name)
	if err := provider.UpdateGroupName(conversationID, name); err != nil {
		return err
	}
	if db.DB != nil {
		var conversation models.Conversation
		if err := db.DB.Where("protocol_conv_id = ?", conversationID).First(&conversation).Error; err == nil {
			conversation.GroupName = name
			if err := db.DB.Save(&conversation).Error; err != nil {
				fmt.Printf("Warning: group renamed remotely but local conversation could not be updated: %v\n", err)
			}
			var account models.LinkedAccount
			if err := db.DB.First(&account, conversation.LinkedAccountID).Error; err == nil {
				account.Username = name
				if err := db.DB.Save(&account).Error; err != nil {
					fmt.Printf("Warning: group renamed remotely but local account could not be updated: %v\n", err)
				} else {
					db.ContactStore.UpsertLinkedAccount(account)
					if meta, ok := db.ContactStore.FindMetaContact(account.MetaContactID); ok {
						meta.DisplayName = name
						if err := db.DB.Save(&meta).Error; err != nil {
							fmt.Printf("Warning: group renamed remotely but local contact could not be updated: %v\n", err)
						} else {
							db.ContactStore.UpsertMetaContact(meta)
						}
					}
				}
			}
		}
	}
	a.emitContactsRefresh()
	return nil
}

func (a *App) UpdateGroupDescription(conversationID, description string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil || !provider.GetCapabilities().SupportsGroupDescription {
		return fmt.Errorf("provider does not support group descriptions")
	}
	detailsProvider, ok := provider.(core.GroupDetailsProvider)
	if !ok {
		return fmt.Errorf("provider does not support group descriptions")
	}
	return detailsProvider.UpdateGroupDescription(conversationID, description)
}

func (a *App) UpdateGroupPhoto(conversationID, encodedPhoto string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil || !provider.GetCapabilities().SupportsGroupPhoto {
		return fmt.Errorf("provider does not support group photos")
	}
	detailsProvider, ok := provider.(core.GroupDetailsProvider)
	if !ok {
		return fmt.Errorf("provider does not support group photos")
	}
	if comma := strings.Index(encodedPhoto, ","); comma >= 0 {
		encodedPhoto = encodedPhoto[comma+1:]
	}
	photo, err := base64.StdEncoding.DecodeString(encodedPhoto)
	if err != nil {
		return fmt.Errorf("invalid group photo: %w", err)
	}
	if err := detailsProvider.UpdateGroupPhoto(conversationID, photo); err != nil {
		return err
	}
	a.emitContactsRefresh()
	return nil
}

func (a *App) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil || !provider.GetCapabilities().SupportsGroupAdminRoles {
		return fmt.Errorf("provider does not support group admin roles")
	}
	return provider.PromoteGroupAdmins(conversationID, participantIDs)
}

func (a *App) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil || !provider.GetCapabilities().SupportsGroupAdminRoles {
		return fmt.Errorf("provider does not support group admin roles")
	}
	return provider.DemoteGroupAdmins(conversationID, participantIDs)
}

// LeaveGroup leaves a group through the provider that owns the conversation.
// Capability checks live here so callers do not need provider-specific logic.
func (a *App) LeaveGroup(conversationID string) error {
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	if !provider.GetCapabilities().SupportsLeaveGroup {
		return fmt.Errorf("provider does not support leaving groups")
	}
	if err := provider.LeaveGroup(conversationID); err != nil {
		return err
	}
	a.emitContactsRefresh()
	return nil
}

func (a *App) GetThreads(parentMessageID string) ([]models.Message, error) {
	if a.getActiveProvider() == nil {
		return nil, fmt.Errorf("no active provider")
	}
	return a.getActiveProvider().GetThreads(parentMessageID)
}

// resolveMessageConversation returns the conversation recorded for a message.
// ThreadView can hold a cached contact ID while its messages have a normalized
// provider conversation ID (for example, Slack DMs). Actions must use the
// message's own conversation so they reach the correct remote channel.
func (a *App) resolveMessageConversation(conversationID, messageID string) string {
	if db.DB == nil || messageID == "" {
		return conversationID
	}

	var message models.Message
	if err := db.DB.Select("protocol_conv_id").Where("protocol_msg_id = ?", messageID).First(&message).Error; err == nil && message.ProtocolConvID != "" {
		return message.ProtocolConvID
	}
	return conversationID
}

func (a *App) AddReaction(conversationID, messageID, emoji string) error {
	conversationID = a.resolveMessageConversation(conversationID, messageID)
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.AddReaction(conversationID, messageID, emoji)
}

func (a *App) RemoveReaction(conversationID, messageID, emoji string) error {
	conversationID = a.resolveMessageConversation(conversationID, messageID)
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	return provider.RemoveReaction(conversationID, messageID, emoji)
}

func (a *App) EditMessage(conversationID, messageID, newText string) error {
	conversationID = a.resolveMessageConversation(conversationID, messageID)
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	_, err := provider.EditMessage(conversationID, messageID, newText)
	return err
}

func (a *App) DeleteMessage(conversationID, messageID string) error {
	conversationID = a.resolveMessageConversation(conversationID, messageID)
	provider := a.getProviderForConversation(conversationID)
	if provider == nil {
		return fmt.Errorf("no provider for conversation %s", conversationID)
	}
	if err := provider.DeleteMessage(conversationID, messageID); err != nil {
		return err
	}
	// Remove from local DB
	if db.DB != nil {
		db.DB.Where("protocol_msg_id = ? OR (protocol_msg_id = ? AND protocol_conv_id = ?)", messageID, messageID, conversationID).Delete(&models.Message{})
	}
	a.invalidateMessageCaches()
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

type googleMessagesAccountPairer interface {
	StartGoogleAccountPairing(cookieJSON string) (string, error)
	CompleteGoogleAccountPairing() error
}

// StartGoogleMessagesLogin starts Google account pairing without storing the
// submitted browser cookies in the provider configuration database.
func (a *App) StartGoogleMessagesLogin(instanceID string, cookieJSON string) (string, error) {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return "", err
	}
	pairer, ok := provider.(googleMessagesAccountPairer)
	if !ok {
		return "", fmt.Errorf("provider %s does not support Google account pairing", instanceID)
	}
	return pairer.StartGoogleAccountPairing(cookieJSON)
}

// AutoPairGoogleMessages opens a Chrome window on the Google login page, waits
// for the user to authenticate (credentials and 2FA entered in the browser),
// then extracts session cookies and starts the Gaia pairing flow.
// Returns the pairing emoji to confirm on the phone.
func (a *App) AutoPairGoogleMessages(instanceID string) (string, error) {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return "", err
	}
	pairer, ok := provider.(googleMessagesAccountPairer)
	if !ok {
		return "", fmt.Errorf("provider %s does not support Google account pairing", instanceID)
	}

	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()

	cookies, err := googlemessages.FetchGoogleCookiesViaLogin(ctx)
	if err != nil {
		return "", err
	}

	cookiesJSON, err := json.Marshal(cookies)
	if err != nil {
		return "", err
	}

	return pairer.StartGoogleAccountPairing(string(cookiesJSON))
}

func (a *App) CompleteGoogleMessagesLogin(instanceID string) error {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return err
	}
	pairer, ok := provider.(googleMessagesAccountPairer)
	if !ok {
		return fmt.Errorf("provider %s does not support Google account pairing", instanceID)
	}
	if err := pairer.CompleteGoogleAccountPairing(); err != nil {
		return err
	}
	if a.ctx != nil {
		a.startEventListenerForProvider(a.ctx, instanceID, provider)
	}
	return nil
}

type teamsBrowserLogin interface {
	LoginWithBrowser(context.Context, string) error
}

// AutoLoginTeams opens Chrome on Microsoft's first-party device login page.
// Microsoft handles credentials, MFA and Conditional Access in the browser;
// Loom receives OAuth tokens only after the user approves the login.
func (a *App) AutoLoginTeams(instanceID, tenant string) error {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return err
	}
	login, ok := provider.(teamsBrowserLogin)
	if !ok {
		return fmt.Errorf("provider %s does not support Microsoft browser login", instanceID)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	if err := login.LoginWithBrowser(ctx, tenant); err != nil {
		return err
	}
	if err := a.SetActiveProvider(instanceID); err != nil {
		return fmt.Errorf("activate Microsoft Teams provider: %w", err)
	}
	if a.ctx != nil {
		a.startEventListenerForProvider(a.ctx, instanceID, provider)
	}
	return nil
}

type slackBrowserLogin interface {
	LoginWithBrowser(context.Context, string) error
}

// AutoLoginSlack opens Chrome on the Slack login page for the specified
// workspace (or slack.com/signin if empty). Loom extracts the session cookies
// and xoxc token once the user has logged in; the browser never exposes
// credentials to Loom. Existing conversations in the database are preserved.
func (a *App) AutoLoginSlack(instanceID, workspaceURL string) error {
	provider, err := a.providerManager.GetProvider(instanceID)
	if err != nil {
		return err
	}
	login, ok := provider.(slackBrowserLogin)
	if !ok {
		return fmt.Errorf("provider %s does not support Slack browser login", instanceID)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	if err := login.LoginWithBrowser(ctx, workspaceURL); err != nil {
		return err
	}
	if err := a.SetActiveProvider(instanceID); err != nil {
		return fmt.Errorf("activate Slack provider: %w", err)
	}
	if a.ctx != nil {
		a.startEventListenerForProvider(a.ctx, instanceID, provider)
	}
	return nil
}

// SyncProvider triggers a synchronization for a specific provider.
// If providerID is empty, syncs the active provider.
func (a *App) SyncProvider(providerID string) error {
	if providerID == "" {
		provider := a.getActiveProvider()
		if provider == nil {
			return fmt.Errorf("no active provider to sync")
		}
		for _, info := range a.providerManager.GetConfiguredProviders() {
			candidate, err := a.providerManager.GetProvider(info.InstanceID)
			if err == nil && candidate == provider {
				providerID = info.InstanceID
				break
			}
		}
		if providerID == "" {
			return fmt.Errorf("active provider is not configured")
		}
	} else {
		if _, err := a.providerManager.GetProvider(providerID); err != nil {
			return err
		}
	}

	// Return immediately to the frontend while retaining a five-minute overlap from
	// the last successful sync, so manual refreshes also recover missed messages.
	go a.syncProviderHistory(providerID, a.syncSince(providerID, 30*24*time.Hour), "manual")
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
// An active call is one with CallType "incoming_call" or "incoming_group_call" that hasn't been terminated.
// We limit to the last 3 minutes: the CallTerminate event may not update the DB record when a LID→JID
// resolution mismatch causes the lookup to fail, leaving a stale "incoming_call" row. A real ringing
// call cannot last longer than a couple of minutes.
func (a *App) GetAllActiveCalls() (map[string]bool, error) {
	if db.DB == nil {
		return map[string]bool{}, nil
	}

	cutoff := time.Now().Add(-3 * time.Minute)
	var activeCallMessages []models.Message
	err := db.DB.Where("call_type IN ? AND timestamp >= ?", []string{"incoming_call", "incoming_group_call"}, cutoff).
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
	// Providers persist timestamps with different UTC offsets. julianday normalizes
	// them before comparison; ordering the raw TEXT values would compare wall-clock
	// times and put e.g. 21:00+02:00 after 20:00+00:00 incorrectly.
	// ID makes equal timestamps deterministic.
	// Thread replies intentionally participate: activity in a thread makes its
	// conversation recent too. GORM silently ignores the rn column.
	err := db.DB.Raw(`
		SELECT * FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY protocol_conv_id
				ORDER BY julianday(timestamp) DESC, id DESC
			) AS rn
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

	rows, err := db.DB.Raw(`
		SELECT
			protocol_conv_id,
			CAST(ROUND(MAX(julianday(timestamp)) * 86400000.0 - 210866760000000.0) AS INTEGER) AS max_time_ms
		FROM messages
		WHERE deleted_at IS NULL
		GROUP BY protocol_conv_id
	`).Rows()
	if err != nil {
		fmt.Printf("[GetAllLastMessageTimestamps] Error getting message timestamps: %v\n", err)
		return map[string]int64{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var protocolConvID string
		var maxTimeMillis int64
		if err := rows.Scan(&protocolConvID, &maxTimeMillis); err != nil {
			fmt.Printf("[GetAllLastMessageTimestamps] Error scanning message row: %v\n", err)
			continue
		}
		if maxTimeMillis > 0 {
			result[protocolConvID] = maxTimeMillis
		}
	}

	// A newly added reaction is conversation activity as well. Keep the latest
	// surviving reaction date so the ordering is identical after a restart.
	reactionRows, err := db.DB.Table("reactions").
		Joins("JOIN messages ON messages.id = reactions.message_id").
		Select(`
			messages.protocol_conv_id,
			CAST(ROUND(MAX(julianday(reactions.created_at)) * 86400000.0 - 210866760000000.0) AS INTEGER) AS max_time_ms
		`).
		Where("messages.deleted_at IS NULL").
		Group("messages.protocol_conv_id").
		Rows()
	if err != nil {
		fmt.Printf("[GetAllLastMessageTimestamps] Error getting reaction timestamps: %v\n", err)
		return map[string]int64{}, err
	}
	defer reactionRows.Close()

	for reactionRows.Next() {
		var protocolConvID string
		var maxTimeMillis int64
		if err := reactionRows.Scan(&protocolConvID, &maxTimeMillis); err != nil {
			fmt.Printf("[GetAllLastMessageTimestamps] Error scanning reaction row: %v\n", err)
			continue
		}
		if maxTimeMillis > result[protocolConvID] {
			result[protocolConvID] = maxTimeMillis
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

	// 3. For missing IDs, try every connected provider. A conversation can be
	// displayed while another provider is active, notably for Teams reactions.
	if len(missingIDs) > 0 {
		for _, userID := range missingIDs {
			if _, exists := result[userID]; !exists {
				instanceIDs := a.providerManager.GetAllInstanceIDs()
				if strings.HasPrefix(strings.ToLower(userID), "8:") {
					sort.SliceStable(instanceIDs, func(i, j int) bool {
						return strings.HasPrefix(instanceIDs[i], "teams-") &&
							!strings.HasPrefix(instanceIDs[j], "teams-")
					})
				}
				for _, instanceID := range instanceIDs {
					provider, err := a.providerManager.GetProvider(instanceID)
					if err != nil || !provider.IsAuthenticated() {
						continue
					}
					name, err := provider.GetContactName(userID)
					if err == nil && name != "" && name != userID {
						result[userID] = name
						break
					}
				}
			}
		}
	}

	return result, nil
}

// GetContactProfile returns the provider-neutral metadata currently persisted for
// a participant. Missing provider fields are represented by empty values.
func (a *App) GetContactProfile(conversationID, userID string) (models.ContactProfile, error) {
	profile := models.ContactProfile{
		UserID:         userID,
		PhoneNumbers:   []string{},
		Emails:         []string{},
		ProviderFields: map[string]string{},
	}
	if db.DB == nil {
		return profile, nil
	}

	var conversation models.Conversation
	var conversationAccount models.LinkedAccount
	if err := db.DB.Where("protocol_conv_id = ?", conversationID).First(&conversation).Error; err == nil {
		_ = db.DB.First(&conversationAccount, conversation.LinkedAccountID).Error
	}

	var account models.LinkedAccount
	query := db.DB.Where("user_id = ?", userID)
	if conversationAccount.ProviderInstanceID != "" {
		query = query.Where("provider_instance_id = ?", conversationAccount.ProviderInstanceID)
	}
	if err := query.Order("updated_at DESC").First(&account).Error; err != nil {
		// Direct conversations often use the account itself as their participant.
		if conversationAccount.ID == 0 || (userID != conversationAccount.UserID && userID != "") {
			if provider := a.getProviderForConversation(conversationID); provider != nil {
				if richer, ok := provider.(interface {
					GetContactProfile(string) (models.ContactProfile, error)
				}); ok {
					if remote, remoteErr := richer.GetContactProfile(userID); remoteErr == nil {
						mergeContactProfile(&profile, remote)
					}
				}
			}
			return profile, nil
		}
		account = conversationAccount
	}

	profile.DisplayName = account.Username
	profile.AvatarURL = account.AvatarURL
	profile.Protocol = account.Protocol
	profile.ProviderInstanceID = account.ProviderInstanceID
	profile.Presence = account.Status
	profile.LastSeen = account.LastSeen

	var extra map[string]interface{}
	if account.Extra != "" && json.Unmarshal([]byte(account.Extra), &extra) == nil {
		profile.StatusText = extraString(extra, "statusText", "status_text")
		profile.StatusEmoji = extraString(extra, "statusEmoji", "status_emoji")
		profile.Address = extraString(extra, "address", "office")
		profile.Company = extraString(extra, "company")
		profile.JobTitle = extraString(extra, "jobTitle", "job_title", "title")
		profile.Department = extraString(extra, "department")
		profile.Timezone = extraString(extra, "timezone", "tz")
		profile.Emails = extraStrings(extra, "emails", "email")
		profile.PhoneNumbers = extraStrings(extra, "phoneNumbers", "phones", "phone")
		for _, key := range []string{"activity", "role", "office"} {
			if value := extraString(extra, key); value != "" {
				profile.ProviderFields[key] = value
			}
		}
	}

	// A canonical WhatsApp JID is the one provider identifier which safely
	// contains a phone number. LIDs deliberately do not.
	if account.Protocol == "whatsapp" && strings.HasSuffix(account.UserID, "@s.whatsapp.net") {
		local := strings.SplitN(account.UserID, "@", 2)[0]
		if colon := strings.Index(local, ":"); colon >= 0 {
			local = local[:colon]
		}
		if local != "" {
			profile.PhoneNumbers = appendUnique(profile.PhoneNumbers, "+"+local)
		}
	}
	if provider := a.getProviderForConversation(conversationID); provider != nil {
		if richer, ok := provider.(interface {
			GetContactProfile(string) (models.ContactProfile, error)
		}); ok {
			if remote, err := richer.GetContactProfile(userID); err == nil {
				mergeContactProfile(&profile, remote)
			}
		}
	}
	return profile, nil
}

// GetContactExchangeStats calculates complete aggregates over persisted history,
// independently of the 50-message frontend page.
func (a *App) GetContactExchangeStats(conversationID, participantID string) (models.ContactExchangeStats, error) {
	stats := models.ContactExchangeStats{}
	if db.DB == nil {
		return stats, nil
	}
	var conversation models.Conversation
	if err := db.DB.Where("protocol_conv_id = ?", conversationID).First(&conversation).Error; err == nil {
		stats.IsGroup = conversation.IsGroup
	}
	includeOwnMessages := !stats.IsGroup
	if stats.IsGroup && participantID != "" {
		var selfMessages int64
		db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ? AND is_from_me = 1 AND sender_id = ?", conversationID, participantID).
			Count(&selfMessages)
		includeOwnMessages = selfMessages > 0
	}

	type aggregateRow struct {
		TotalMessages         int64
		SentMessages          int64
		ReceivedMessages      int64
		ActiveDays            int64
		AttachmentMessages    int64
		Calls                 int64
		MissedCalls           int64
		TotalCallDurationSecs int64
		FirstExchangeMillis   *int64
		LastExchangeMillis    *int64
	}
	var aggregate aggregateRow
	err := db.DB.Raw(`
		SELECT
			COUNT(*) AS total_messages,
			COALESCE(SUM(CASE WHEN is_from_me = 1 AND ? = 1 THEN 1 ELSE 0 END), 0) AS sent_messages,
			COALESCE(SUM(CASE WHEN is_from_me = 0 AND (? = '' OR sender_id = ?) THEN 1 ELSE 0 END), 0) AS received_messages,
			COUNT(DISTINCT date(timestamp)) AS active_days,
			COALESCE(SUM(CASE WHEN attachments IS NOT NULL AND TRIM(attachments) NOT IN ('', '[]', 'null') THEN 1 ELSE 0 END), 0) AS attachment_messages,
			COALESCE(SUM(CASE WHEN call_type <> '' THEN 1 ELSE 0 END), 0) AS calls,
			COALESCE(SUM(CASE WHEN call_type LIKE 'missed_%' OR UPPER(call_outcome) = 'MISSED' THEN 1 ELSE 0 END), 0) AS missed_calls,
			COALESCE(SUM(call_duration_secs), 0) AS total_call_duration_secs,
			CAST(ROUND(julianday(MIN(timestamp)) * 86400000.0 - 210866760000000.0) AS INTEGER) AS first_exchange_millis,
			CAST(ROUND(julianday(MAX(timestamp)) * 86400000.0 - 210866760000000.0) AS INTEGER) AS last_exchange_millis
		FROM messages
		WHERE protocol_conv_id = ? AND deleted_at IS NULL AND is_deleted = 0
			AND ((is_from_me = 1 AND ? = 1) OR (is_from_me = 0 AND (? = '' OR sender_id = ?)))
	`, includeOwnMessages, participantID, participantID, conversationID, includeOwnMessages, participantID, participantID).Scan(&aggregate).Error
	if err != nil {
		return stats, err
	}
	stats.TotalMessages = aggregate.TotalMessages
	stats.SentMessages = aggregate.SentMessages
	stats.ReceivedMessages = aggregate.ReceivedMessages
	stats.ActiveDays = aggregate.ActiveDays
	stats.AttachmentMessages = aggregate.AttachmentMessages
	stats.Calls = aggregate.Calls
	stats.MissedCalls = aggregate.MissedCalls
	stats.TotalCallDurationSecs = aggregate.TotalCallDurationSecs
	if aggregate.FirstExchangeMillis != nil {
		value := time.UnixMilli(*aggregate.FirstExchangeMillis)
		stats.FirstExchange = &value
	}
	if aggregate.LastExchangeMillis != nil {
		value := time.UnixMilli(*aggregate.LastExchangeMillis)
		stats.LastExchange = &value
	}

	_ = db.DB.Raw(`
		SELECT COUNT(*) FROM reactions r
		JOIN messages m ON m.id = r.message_id
		WHERE m.protocol_conv_id = ? AND m.deleted_at IS NULL AND r.user_id = ?
	`, conversationID, participantID).Scan(&stats.ReactionsGiven).Error
	_ = db.DB.Raw(`
		SELECT COUNT(*) FROM reactions r
		JOIN messages m ON m.id = r.message_id
		WHERE m.protocol_conv_id = ? AND m.deleted_at IS NULL
			AND m.is_from_me = 0 AND (? = '' OR m.sender_id = ?)
			AND r.user_id <> ?
	`, conversationID, participantID, participantID, participantID).Scan(&stats.ReactionsReceived).Error

	type messageTurn struct {
		IsFromMe  bool
		Timestamp time.Time
	}
	var turns []messageTurn
	if err := db.DB.Raw(`
		SELECT is_from_me, timestamp FROM messages
		WHERE protocol_conv_id = ? AND deleted_at IS NULL AND is_deleted = 0
			AND ((is_from_me = 1 AND ? = 1) OR (is_from_me = 0 AND (? = '' OR sender_id = ?)))
		ORDER BY timestamp ASC, id ASC
	`, conversationID, includeOwnMessages, participantID, participantID).Scan(&turns).Error; err != nil {
		return stats, err
	}
	var contactResponses, myResponses []int64
	for i := 1; i < len(turns); i++ {
		if turns[i].IsFromMe == turns[i-1].IsFromMe {
			continue
		}
		seconds := int64(turns[i].Timestamp.Sub(turns[i-1].Timestamp).Seconds())
		if seconds < 0 {
			continue
		}
		if turns[i].IsFromMe {
			myResponses = append(myResponses, seconds)
		} else {
			contactResponses = append(contactResponses, seconds)
		}
	}
	stats.MedianContactResponseSecs = medianSeconds(contactResponses)
	stats.MedianMyResponseSecs = medianSeconds(myResponses)
	return stats, nil
}

// GetCommunicationStats calculates the dashboard aggregates directly from the
// persisted history. from is inclusive and to is exclusive.
func (a *App) GetCommunicationStats(from, to time.Time) (models.CommunicationStats, error) {
	stats := models.CommunicationStats{From: from, To: to, Series: []models.CommunicationSeriesPoint{}, Instances: []models.InstanceCommunicationStats{}, Contacts: []models.ContactCommunicationStats{}}
	if db.DB == nil || !to.After(from) {
		return stats, nil
	}
	previousFrom := from.Add(-to.Sub(from))

	type countRow struct{ Total, Sent, Received int64 }
	loadCount := func(start, end time.Time, target *models.CommunicationCount) error {
		var row countRow
		err := db.DB.Raw(`SELECT COUNT(*) total,
			COALESCE(SUM(CASE WHEN is_from_me = 1 THEN 1 ELSE 0 END), 0) sent,
			COALESCE(SUM(CASE WHEN is_from_me = 0 THEN 1 ELSE 0 END), 0) received
			FROM messages WHERE timestamp >= ? AND timestamp < ? AND deleted_at IS NULL
			AND is_deleted = 0 AND COALESCE(call_type, '') = ''`, start, end).Scan(&row).Error
		target.Total, target.Sent, target.Received = row.Total, row.Sent, row.Received
		return err
	}
	if err := loadCount(from, to, &stats.Summary); err != nil {
		return stats, err
	}
	if err := loadCount(previousFrom, from, &stats.PreviousSummary); err != nil {
		return stats, err
	}

	bucket := 24 * time.Hour
	if to.Sub(from) <= 48*time.Hour {
		bucket = time.Hour
	} else if to.Sub(from) > 62*24*time.Hour {
		bucket = 7 * 24 * time.Hour
	}
	type seriesRow struct {
		Bucket                int
		Total, Sent, Received int64
	}
	var series []seriesRow
	if err := db.DB.Raw(`SELECT CAST(((julianday(timestamp)-julianday(?))*86400)/? AS INTEGER) bucket,
		COUNT(*) total, COALESCE(SUM(CASE WHEN is_from_me=1 THEN 1 ELSE 0 END),0) sent,
		COALESCE(SUM(CASE WHEN is_from_me=0 THEN 1 ELSE 0 END),0) received
		FROM messages WHERE timestamp >= ? AND timestamp < ? AND deleted_at IS NULL AND is_deleted=0
		AND COALESCE(call_type,'')='' GROUP BY bucket ORDER BY bucket`, from, int64(bucket.Seconds()), from, to).Scan(&series).Error; err != nil {
		return stats, err
	}
	byBucket := make(map[int]seriesRow, len(series))
	for _, row := range series {
		byBucket[row.Bucket] = row
	}
	for i, at := 0, from; at.Before(to); i, at = i+1, at.Add(bucket) {
		row := byBucket[i]
		stats.Series = append(stats.Series, models.CommunicationSeriesPoint{Timestamp: at, CommunicationCount: models.CommunicationCount{Total: row.Total, Sent: row.Sent, Received: row.Received}})
	}

	type aggregateRow struct {
		MetaContactID                                                        uint
		DisplayName, AvatarURL, ProviderInstanceID, ProviderID, InstanceName string
		Total, Sent, Received                                                int64
	}
	var rows []aggregateRow
	err := db.DB.Raw(`SELECT mc.id meta_contact_id, mc.display_name, mc.avatar_url,
		la.provider_instance_id, la.protocol provider_id,
		COALESCE(NULLIF(pc.instance_name,''), la.provider_instance_id) instance_name,
		COUNT(*) total, COALESCE(SUM(CASE WHEN m.is_from_me=1 THEN 1 ELSE 0 END),0) sent,
		COALESCE(SUM(CASE WHEN m.is_from_me=0 THEN 1 ELSE 0 END),0) received
		FROM messages m JOIN conversations c ON c.id=m.conversation_id
		JOIN linked_accounts la ON la.id=c.linked_account_id JOIN meta_contacts mc ON mc.id=la.meta_contact_id
		LEFT JOIN provider_configurations pc ON pc.instance_id=la.provider_instance_id
		WHERE m.timestamp>=? AND m.timestamp<? AND m.deleted_at IS NULL AND m.is_deleted=0 AND COALESCE(m.call_type,'')=''
		GROUP BY mc.id, la.provider_instance_id ORDER BY total DESC`, from, to).Scan(&rows).Error
	if err != nil {
		return stats, err
	}
	instances := make(map[string]*models.InstanceCommunicationStats)
	contacts := make(map[string]*models.ContactCommunicationStats)
	for _, row := range rows {
		inst := &models.InstanceCommunicationStats{ProviderInstanceID: row.ProviderInstanceID, ProviderID: row.ProviderID, InstanceName: row.InstanceName, CommunicationCount: models.CommunicationCount{Total: row.Total, Sent: row.Sent, Received: row.Received}}
		if existing := instances[row.ProviderInstanceID]; existing != nil {
			existing.Total += row.Total
			existing.Sent += row.Sent
			existing.Received += row.Received
		} else {
			instances[row.ProviderInstanceID] = inst
		}
		key := fmt.Sprintf("%d:%s", row.MetaContactID, row.ProviderInstanceID)
		contacts[key] = &models.ContactCommunicationStats{MetaContactID: row.MetaContactID, DisplayName: row.DisplayName, AvatarURL: row.AvatarURL, ProviderInstanceID: row.ProviderInstanceID, ProviderID: row.ProviderID, InstanceName: row.InstanceName, CommunicationCount: inst.CommunicationCount}
	}

	type callRow struct {
		MetaContactID                                                                                  uint
		DisplayName, AvatarURL, ProviderInstanceID, ProviderID, InstanceName, ProtocolConvID, CallType string
		Timestamp                                                                                      time.Time
		Duration                                                                                       *int32
	}
	var calls []callRow
	err = db.DB.Raw(`SELECT mc.id meta_contact_id, mc.display_name, mc.avatar_url, la.provider_instance_id,
		la.protocol provider_id, COALESCE(NULLIF(pc.instance_name,''),la.provider_instance_id) instance_name,
		m.protocol_conv_id, m.call_type, m.timestamp, m.call_duration_secs duration
		FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN linked_accounts la ON la.id=c.linked_account_id
		JOIN meta_contacts mc ON mc.id=la.meta_contact_id LEFT JOIN provider_configurations pc ON pc.instance_id=la.provider_instance_id
		WHERE m.timestamp>=? AND m.timestamp<? AND m.deleted_at IS NULL AND m.is_deleted=0 AND COALESCE(m.call_type,'')<>''
		ORDER BY m.timestamp, m.id`, from.Add(-24*time.Hour), to).Scan(&calls).Error
	if err != nil {
		return stats, err
	}
	pending := map[string]callRow{}
	addCall := func(row callRow, duration int64, missing bool) {
		inst := instances[row.ProviderInstanceID]
		if inst == nil {
			inst = &models.InstanceCommunicationStats{ProviderInstanceID: row.ProviderInstanceID, ProviderID: row.ProviderID, InstanceName: row.InstanceName}
			instances[row.ProviderInstanceID] = inst
		}
		key := fmt.Sprintf("%d:%s", row.MetaContactID, row.ProviderInstanceID)
		contact := contacts[key]
		if contact == nil {
			contact = &models.ContactCommunicationStats{MetaContactID: row.MetaContactID, DisplayName: row.DisplayName, AvatarURL: row.AvatarURL, ProviderInstanceID: row.ProviderInstanceID, ProviderID: row.ProviderID, InstanceName: row.InstanceName}
			contacts[key] = contact
		}
		inst.CallCount++
		contact.CallCount++
		inst.CallDurationSecs += duration
		contact.CallDurationSecs += duration
		if missing {
			inst.CallsWithoutDuration++
			contact.CallsWithoutDuration++
		}
	}
	for _, row := range calls {
		t := strings.ToLower(row.CallType)
		isStart := t == "scheduled_start" || t == "incoming_call" || t == "incoming_group_call"
		if isStart && row.Duration == nil {
			pending[row.ProtocolConvID] = row
			continue
		}
		duration := int64(0)
		if row.Duration != nil && *row.Duration > 0 {
			duration = int64(*row.Duration)
		}
		if duration == 0 {
			if start, ok := pending[row.ProtocolConvID]; ok && row.Timestamp.After(start.Timestamp) && row.Timestamp.Sub(start.Timestamp) <= 24*time.Hour {
				duration = int64(row.Timestamp.Sub(start.Timestamp).Seconds())
				delete(pending, row.ProtocolConvID)
			}
		}
		if !row.Timestamp.Before(from) {
			addCall(row, duration, duration == 0)
		}
	}
	for _, row := range pending {
		if !row.Timestamp.Before(from) {
			addCall(row, 0, true)
		}
	}

	for _, value := range instances {
		stats.Instances = append(stats.Instances, *value)
	}
	for _, value := range contacts {
		stats.Contacts = append(stats.Contacts, *value)
	}
	sort.Slice(stats.Instances, func(i, j int) bool { return stats.Instances[i].Total > stats.Instances[j].Total })
	sort.Slice(stats.Contacts, func(i, j int) bool { return stats.Contacts[i].Total > stats.Contacts[j].Total })
	return stats, nil
}

func extraString(extra map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := extra[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extraStrings(extra map[string]interface{}, keys ...string) []string {
	result := []string{}
	for _, key := range keys {
		switch value := extra[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				result = appendUnique(result, strings.TrimSpace(value))
			}
		case []interface{}:
			for _, item := range value {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					result = appendUnique(result, strings.TrimSpace(text))
				}
			}
		}
	}
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeContactProfile(target *models.ContactProfile, source models.ContactProfile) {
	if source.DisplayName != "" {
		target.DisplayName = source.DisplayName
	}
	if source.AvatarURL != "" {
		target.AvatarURL = source.AvatarURL
	}
	if source.Protocol != "" {
		target.Protocol = source.Protocol
	}
	if source.ProviderInstanceID != "" {
		target.ProviderInstanceID = source.ProviderInstanceID
	}
	for _, email := range source.Emails {
		target.Emails = appendUnique(target.Emails, email)
	}
	for _, phone := range source.PhoneNumbers {
		target.PhoneNumbers = appendUnique(target.PhoneNumbers, phone)
	}
	if source.Address != "" {
		target.Address = source.Address
	}
	if source.Company != "" {
		target.Company = source.Company
	}
	if source.JobTitle != "" {
		target.JobTitle = source.JobTitle
	}
	if source.Department != "" {
		target.Department = source.Department
	}
}

func medianSeconds(values []int64) *int64 {
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	value := values[middle]
	if len(values)%2 == 0 {
		value = (values[middle-1] + values[middle]) / 2
	}
	return &value
}

// GetAttachmentData reads local file or downloads remote URL and returns base64 data URL
func (a *App) GetAttachmentData(path string) (string, error) {
	if strings.HasPrefix(path, "data:") {
		return path, nil
	}

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

	// Teams and SharePoint file URLs require the authenticated Teams client.
	type teamsFileDataProvider interface {
		GetTeamsFileData(string) (string, error)
	}
	var teamsErr error
	for _, instanceID := range a.providerManager.GetAllInstanceIDs() {
		p, err := a.providerManager.GetProvider(instanceID)
		if err != nil {
			continue
		}
		teamsProvider, ok := p.(teamsFileDataProvider)
		if !ok {
			continue
		}
		data, err := teamsProvider.GetTeamsFileData(path)
		if err == nil && data != "" {
			return data, nil
		}
		teamsErr = err
	}
	if teamsErr != nil && (strings.Contains(path, "teams.microsoft.com") ||
		strings.Contains(path, ".skype.com") ||
		strings.Contains(path, ".sharepoint.com") ||
		strings.Contains(path, ".sharepoint-df.com")) {
		return "", fmt.Errorf("teams file unavailable: %w", teamsErr)
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
// Returns the saved file path on success, or an empty string if the user cancelled.
func (a *App) SaveAttachmentToFile(url string, fileName string) (string, error) {
	dataURL, err := a.GetAttachmentData(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch attachment: %w", err)
	}

	// Strip the "data:<mime>;base64," prefix
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return "", fmt.Errorf("invalid data URL format")
	}
	encoded := dataURL[comma+1:]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode attachment data: %w", err)
	}

	// Ask the user where to save
	savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: fileName,
		Title:           "Enregistrer le fichier",
	})
	if err != nil {
		return "", fmt.Errorf("save dialog error: %w", err)
	}
	if savePath == "" {
		return "", nil // user cancelled
	}

	// Ensure the extension is preserved when the OS strips it
	if filepath.Ext(savePath) == "" && filepath.Ext(fileName) != "" {
		savePath += filepath.Ext(fileName)
	}

	if err := os.WriteFile(savePath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	return savePath, nil
}

// OpenFile opens a file with the default system application.
func (a *App) OpenFile(path string) error {
	return openFile(path)
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

// metaAttrs extracts property, name, and content from a <meta> node's attributes.
func metaAttrs(n *html.Node) (property, name, content string) {
	for _, a := range n.Attr {
		switch a.Key {
		case "property":
			property = a.Val
		case "name":
			name = a.Val
		case "content":
			content = a.Val
		}
	}
	return
}

// applyMetaToPreview updates preview fields from a single <meta> node.
func applyMetaToPreview(n *html.Node, p *LinkPreview) {
	property, name, content := metaAttrs(n)
	switch property {
	case "og:title":
		p.Title = content
	case "og:description":
		p.Description = content
	case "og:image":
		p.ImageURL = content
	case "og:url":
		if content != "" {
			p.URL = content
		}
	}
	if p.Description == "" && (name == "description" || name == "twitter:description") {
		p.Description = content
	}
	if p.Title == "" && name == "twitter:title" {
		p.Title = content
	}
	if p.ImageURL == "" && name == "twitter:image" {
		p.ImageURL = content
	}
}

// walkHTMLForPreview traverses the HTML tree and extracts OG/meta/title data into p.
func walkHTMLForPreview(n *html.Node, p *LinkPreview, title *string) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "title":
			if n.FirstChild != nil && *title == "" {
				*title = strings.TrimSpace(n.FirstChild.Data)
			}
		case "meta":
			applyMetaToPreview(n, p)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTMLForPreview(c, p, title)
	}
}

// jsonLDString returns the first useful string representation of a JSON-LD
// value. Schema.org allows image and URL fields to be strings, objects, or
// arrays of either.
func jsonLDString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, item := range value {
			if result := jsonLDString(item); result != "" {
				return result
			}
		}
	case map[string]any:
		for _, key := range []string{"url", "contentUrl", "@id"} {
			if result := jsonLDString(value[key]); result != "" {
				return result
			}
		}
	}
	return ""
}

// applyJSONLDToPreview walks a JSON-LD document and fills metadata missing
// from the standard Open Graph tags. Nested @graph entries are common.
func applyJSONLDToPreview(value any, p *LinkPreview) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			applyJSONLDToPreview(item, p)
		}
	case map[string]any:
		if p.Title == "" {
			for _, key := range []string{"name", "headline"} {
				if p.Title = jsonLDString(value[key]); p.Title != "" {
					break
				}
			}
		}
		if p.Description == "" {
			p.Description = jsonLDString(value["description"])
		}
		if p.ImageURL == "" {
			p.ImageURL = jsonLDString(value["image"])
		}
		if nested, ok := value["@graph"]; ok {
			applyJSONLDToPreview(nested, p)
		}
	}
}

func walkHTMLForJSONLD(n *html.Node, p *LinkPreview) {
	if n.Type == html.ElementNode && n.Data == "script" {
		var scriptType string
		for _, attr := range n.Attr {
			if attr.Key == "type" {
				scriptType = strings.ToLower(strings.TrimSpace(strings.Split(attr.Val, ";")[0]))
				break
			}
		}
		if scriptType == "application/ld+json" && n.FirstChild != nil {
			var value any
			if json.Unmarshal([]byte(n.FirstChild.Data), &value) == nil {
				applyJSONLDToPreview(value, p)
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkHTMLForJSONLD(child, p)
	}
}

// FetchLinkPreview fetches and parses Open Graph metadata for a given URL.
// Results are cached for one hour to avoid repeated network requests.
func (a *App) FetchLinkPreview(url string) (LinkPreview, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return LinkPreview{}, fmt.Errorf("invalid URL: %s", url)
	}

	// Serve from cache when available.
	a.linkPreviewCacheMu.RLock()
	if a.linkPreviewCache != nil {
		if entry, ok := a.linkPreviewCache[url]; ok && time.Now().Before(entry.expiresAt) {
			a.linkPreviewCacheMu.RUnlock()
			return entry.preview, nil
		}
	}
	a.linkPreviewCacheMu.RUnlock()

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return LinkPreview{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Loom/1.0; +https://github.com/loom)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return LinkPreview{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return LinkPreview{}, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	// Limit to 512 KB to avoid reading huge pages.
	body := io.LimitReader(resp.Body, 512*1024)
	doc, err := html.Parse(body)
	if err != nil {
		return LinkPreview{}, fmt.Errorf("HTML parse error: %w", err)
	}

	preview := LinkPreview{URL: url}
	var title string
	walkHTMLForPreview(doc, &preview, &title)
	walkHTMLForJSONLD(doc, &preview)

	if preview.Title == "" {
		preview.Title = title
	}

	a.linkPreviewCacheMu.Lock()
	if a.linkPreviewCache == nil {
		a.linkPreviewCache = make(map[string]linkPreviewEntry)
	}
	a.linkPreviewCache[url] = linkPreviewEntry{preview: preview, expiresAt: time.Now().Add(time.Hour)}
	a.linkPreviewCacheMu.Unlock()

	return preview, nil
}
