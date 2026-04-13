// Package slack provides the Slack provider implementation.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/logging"
	"Loom/pkg/models"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// SlackProvider implements the core.Provider interface for Slack.
type SlackProvider struct {
	config             core.ProviderConfig
	client             *slack.Client
	socketClient       *socketmode.Client
	rtmClient          *slack.RTM
	mu                 sync.RWMutex
	logger             *logging.ProviderLogger
	userCache          map[string]*slack.User // Cache for user info to avoid repeated API calls
	userCacheMu        sync.RWMutex
	emojiCache         map[string]string // Cache for emoji names to URLs (e.g., "calendar" -> "https://...")
	emojiCacheMu       sync.RWMutex
	eventChan          chan core.ProviderEvent // Channel for emitting events
	stopChan           chan struct{}           // Channel to signal polling goroutine to stop
	statusCache        map[string]userStatus   // Cache of last known status for each user
	statusCacheMu      sync.RWMutex            // Mutex for status cache
	mpimProcessingChan chan struct{}           // Channel to track MPIM processing completion
	mpimCount          int                     // Number of MPIMs being processed
	mpimCountMu        sync.RWMutex            // Mutex for MPIM count
	eventStreamCtx     context.Context         // Context for event stream
	eventStreamCancel  context.CancelFunc      // Cancel function for event stream
	eventStreamStarted bool                    // Whether event stream has been started
	dmChannelCache     map[string]string        // Cache: DM channel ID (D...) -> User ID (U...)
	dmChannelCacheMu   sync.RWMutex             // Mutex for DM channel cache
	selfUserID         string                   // Cached authenticated user ID (from AuthTest)
}

// userStatus represents the cached status information for a user
type userStatus struct {
	status      string
	statusEmoji string
	statusText  string
}

// cookieTransport injects the d cookie into requests
type cookieTransport struct {
	Transport http.RoundTripper
	Cookie    string
}

func (t *cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Log the cookie being sent (first 20 chars only for security)
	cookiePreview := t.Cookie
	if len(cookiePreview) > 20 {
		cookiePreview = cookiePreview[:20] + "..."
	}
	req.Header.Add("Cookie", t.Cookie)
	// Some xoxc endpoints also check for d-s cookie, but usually d is the main auth one.
	return t.Transport.RoundTrip(req)
}

// Ensure interface compliance
var _ core.Provider = (*SlackProvider)(nil)

// NewSlackProvider creates a new instance of the SlackProvider.
func NewSlackProvider() *SlackProvider {
	return &SlackProvider{
		userCache:          make(map[string]*slack.User),
		emojiCache:         make(map[string]string),
		eventChan:          make(chan core.ProviderEvent, 100), // Buffered channel to avoid blocking
		stopChan:           make(chan struct{}),
		statusCache:        make(map[string]userStatus),
		mpimProcessingChan: make(chan struct{}, 1), // Buffered channel for MPIM processing completion
		dmChannelCache:     make(map[string]string),
	}
}

// Init initializes the provider with its configuration.
func (p *SlackProvider) Init(config core.ProviderConfig) error {
	fmt.Printf("SlackProvider.Init: START\n")

	// Get instanceID for logger initialization
	instanceID, _ := config["_instance_id"].(string)
	if instanceID == "" {
		instanceID = "slack-1" // Default instance ID
	}
	fmt.Printf("SlackProvider.Init: instanceID=%s\n", instanceID)

	// Initialize logger
	logger, err := logging.GetLogger("slack", instanceID)
	if err != nil {
		// Log error but continue - fallback to fmt.Printf
		fmt.Printf("SlackProvider.Init: WARNING - failed to initialize logger: %v\n", err)
	} else {
		p.logger = logger
		fmt.Printf("SlackProvider.Init: logger initialized successfully\n")
	}

	p.log("SlackProvider.Init: initializing with instanceID=%s\n", instanceID)
	fmt.Printf("SlackProvider.Init: calling SetConfig\n")
	err = p.SetConfig(config)
	if err != nil {
		fmt.Printf("SlackProvider.Init: ERROR - SetConfig failed: %v\n", err)
		return err
	}
	fmt.Printf("SlackProvider.Init: SetConfig completed successfully\n")
	return nil
}

func (p *SlackProvider) log(format string, args ...interface{}) {
	if p.logger != nil {
		p.logger.Logf(format, args...)
	} else {
		// Fallback to fmt.Printf if logger not initialized
		fmt.Printf(format, args...)
	}
}

// GetConfig returns the current configuration of the provider.
func (p *SlackProvider) GetConfig() core.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

// SetConfig updates the configuration of the provider.
func (p *SlackProvider) SetConfig(config core.ProviderConfig) error {
	fmt.Printf("SlackProvider.SetConfig: START\n")
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Printf("SlackProvider.SetConfig: lock acquired\n")

	p.config = config
	fmt.Printf("SlackProvider.SetConfig: config assigned\n")

	p.log("SlackProvider.SetConfig: applying config\n")
	fmt.Printf("SlackProvider.SetConfig: getting token and d_cookie\n")

	token, _ := config.GetString("token")
	dCookie, _ := config.GetString("d_cookie")
	fmt.Printf("SlackProvider.SetConfig: token present=%v, dCookie present=%v\n", token != "", dCookie != "")
	if token != "" {
		tokenPreview := token
		if len(tokenPreview) > 10 {
			tokenPreview = tokenPreview[:10]
		}
		fmt.Printf("SlackProvider.SetConfig: token starts with=%s\n", tokenPreview)
	}
	if dCookie != "" {
		cookiePreview := dCookie
		if len(cookiePreview) > 10 {
			cookiePreview = cookiePreview[:10]
		}
		fmt.Printf("SlackProvider.SetConfig: dCookie starts with=%s\n", cookiePreview)
	}

	if token != "" {
		fmt.Printf("SlackProvider.SetConfig: creating Slack client\n")
		opts := []slack.Option{
			slack.OptionDebug(false),
			slack.OptionLog(p.logger),
		}

		if dCookie != "" {
			fmt.Printf("SlackProvider.SetConfig: setting up cookie transport with d cookie\n")
			// Create custom HTTP client that sends the d cookie
			// Format: "d=xoxd-..." (the user should provide just the value, we add "d=")
			cookieValue := dCookie
			// If the user provided "d=xoxd-...", extract just the value
			if strings.HasPrefix(cookieValue, "d=") {
				cookieValue = cookieValue[2:]
			}
			cookieHeader := fmt.Sprintf("d=%s", cookieValue)
			fmt.Printf("SlackProvider.SetConfig: cookie header format: d=... (length=%d)\n", len(cookieValue))

			client := &http.Client{
				Transport: &cookieTransport{
					Transport: http.DefaultTransport,
					Cookie:    cookieHeader,
				},
			}
			opts = append(opts, slack.OptionHTTPClient(client))
		}

		p.client = slack.New(token, opts...)
		fmt.Printf("SlackProvider.SetConfig: Slack client created\n")
	} else {
		fmt.Printf("SlackProvider.SetConfig: WARNING - no token provided\n")
	}
	fmt.Printf("SlackProvider.SetConfig: END (success)\n")
	return nil
}

// IsAuthenticated returns true if the provider is already authenticated.
func (p *SlackProvider) IsAuthenticated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.client != nil
}

// incrementalSyncExistingConversations syncs new messages for conversations that already have message history
func (p *SlackProvider) incrementalSyncExistingConversations() {
	if db.DB == nil {
		p.log("SlackProvider.incrementalSyncExistingConversations: DB not initialized\n")
		return
	}

	p.log("SlackProvider.incrementalSyncExistingConversations: Starting incremental sync\n")

	// Get all unique protocol_conv_id values that have messages
	var results []struct {
		ProtocolConvID string
		LastTimestamp  string // SQLite returns timestamp as string
		MessageCount   int64
	}

	// Get MAX timestamp for each conversation, but only for main messages (not thread replies)
	// Thread replies have thread_id != protocol_msg_id, so we exclude them
	// IMPORTANT: Filter by protocol to only sync Slack conversations (not WhatsApp or other providers)
	// Slack conversation IDs start with C (channels), D (DMs), G (groups), U (users), etc. and don't contain "@"
	// WhatsApp IDs contain "@s.whatsapp.net" or "@g.us" or "@lid"
	err := db.DB.Raw(`
		SELECT 
			protocol_conv_id as protocol_conv_id,
			MAX(timestamp) as last_timestamp,
			COUNT(*) as message_count
		FROM messages
		WHERE protocol_conv_id IS NOT NULL 
			AND protocol_conv_id != ''
			AND (thread_id IS NULL OR thread_id = '' OR thread_id = protocol_msg_id)
			AND protocol_conv_id NOT LIKE '%@%'
			AND (protocol_conv_id LIKE 'C%' OR protocol_conv_id LIKE 'D%' OR protocol_conv_id LIKE 'G%' OR protocol_conv_id LIKE 'U%')
		GROUP BY protocol_conv_id
		ORDER BY MAX(timestamp) DESC
	`).Scan(&results).Error

	if err != nil {
		p.log("SlackProvider.incrementalSyncExistingConversations: Failed to query conversations: %v\n", err)
		return
	}

	// Convert string timestamps to time.Time
	// SQLite returns timestamps in format "2006-01-02 15:04:05.999999999+07:00" (space instead of T)
	type ConversationInfo struct {
		ProtocolConvID string
		LastTimestamp  time.Time
		MessageCount   int64
	}
	conversations := make([]ConversationInfo, 0, len(results))
	for _, r := range results {
		var lastTS time.Time
		var err error

		// Try RFC3339Nano first (standard format with T)
		lastTS, err = time.Parse(time.RFC3339Nano, r.LastTimestamp)
		if err != nil {
			// Try SQLite format (space instead of T): "2006-01-02 15:04:05.999999999+07:00"
			// Replace space with T to convert to RFC3339Nano format
			sqliteFormat := strings.Replace(r.LastTimestamp, " ", "T", 1)
			lastTS, err = time.Parse(time.RFC3339Nano, sqliteFormat)
			if err != nil {
				// Try RFC3339 (without nanoseconds)
				lastTS, err = time.Parse(time.RFC3339, r.LastTimestamp)
				if err != nil {
					// Try SQLite format without nanoseconds
					sqliteFormatSimple := strings.Replace(r.LastTimestamp, " ", "T", 1)
					lastTS, err = time.Parse(time.RFC3339, sqliteFormatSimple)
					if err != nil {
						p.log("SlackProvider.incrementalSyncExistingConversations: Failed to parse timestamp %s for %s: %v\n", r.LastTimestamp, r.ProtocolConvID, err)
						continue
					}
				}
			}
		}
		conversations = append(conversations, ConversationInfo{
			ProtocolConvID: r.ProtocolConvID,
			LastTimestamp:  lastTS,
			MessageCount:   r.MessageCount,
		})
	}

	p.log("SlackProvider.incrementalSyncExistingConversations: Found %d conversations with messages\n", len(conversations))

	if len(conversations) == 0 {
		// No conversations with messages yet (e.g. fresh setup). Emit completed so the footer closes.
		p.emitSyncStatus(core.SyncStatusCompleted, "All conversations are up to date", 100)
		return
	}

	// Emit sync status
	p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Checking %d conversations for new messages...", len(conversations)), 0)

	successCount := 0
	totalNewMessages := 0
	newConversationsCreated := 0

	// Sync each conversation
	for i, conv := range conversations {
		progress := int((float64(i+1) / float64(len(conversations))) * 100)
		p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing %s... (%d/%d)", conv.ProtocolConvID, i+1, len(conversations)), progress)

		// Fix orphaned messages saved by pollGlobalUpdates (ConversationID=0).
		// Only call ensureConversation when such messages exist to avoid side-effects on
		// well-configured conversations. Normalize DM channel IDs (D→U) first so we
		// reuse the existing Conversation record instead of creating a duplicate.
		if db.DB != nil {
			var orphanCount int64
			db.DB.Model(&models.Message{}).
				Where("protocol_conv_id = ? AND conversation_id = 0", conv.ProtocolConvID).
				Count(&orphanCount)
			if orphanCount > 0 {
				normalizedConvID := p.normalizeDMConversationID(conv.ProtocolConvID)
				convDBID, ensureErr := p.ensureConversation(normalizedConvID)
				if ensureErr != nil {
					p.log("SlackProvider.incrementalSyncExistingConversations: Failed to ensure conversation for %s (normalized: %s): %v\n",
						conv.ProtocolConvID, normalizedConvID, ensureErr)
				} else if convDBID > 0 {
					result := db.DB.Model(&models.Message{}).
						Where("protocol_conv_id = ? AND conversation_id = 0", conv.ProtocolConvID).
						Update("conversation_id", convDBID)
					if result.RowsAffected > 0 {
						p.log("SlackProvider.incrementalSyncExistingConversations: Fixed %d orphaned messages for %s → conv %d\n",
							result.RowsAffected, conv.ProtocolConvID, convDBID)
						newConversationsCreated++
					}
				}
			}
		}

		// Fetch new messages since last timestamp
		// Use the exact last timestamp - GetConversationHistory will handle exclusion
		// We want messages strictly after the last one we have
		sinceTimestamp := conv.LastTimestamp

		p.log("SlackProvider.incrementalSyncExistingConversations: Syncing %s (last message: %s, looking for messages after)\n",
			conv.ProtocolConvID, conv.LastTimestamp.Format(time.RFC3339))

		// Use a large limit to get all new messages
		newMessages, err := p.GetConversationHistory(conv.ProtocolConvID, 1000, nil, &sinceTimestamp)
		if err != nil {
			p.log("SlackProvider.incrementalSyncExistingConversations: Failed to sync %s: %v\n", conv.ProtocolConvID, err)
			continue
		}

		if len(newMessages) > 0 {
			p.log("SlackProvider.incrementalSyncExistingConversations: Found %d new messages for %s\n", len(newMessages), conv.ProtocolConvID)
			successCount++
			totalNewMessages += len(newMessages)

			// Log first and last message timestamps for debugging
			if len(newMessages) > 0 {
				firstMsg := newMessages[0]
				lastMsg := newMessages[len(newMessages)-1]
				p.log("SlackProvider.incrementalSyncExistingConversations: New messages range from %s to %s\n",
					firstMsg.Timestamp.Format(time.RFC3339), lastMsg.Timestamp.Format(time.RFC3339))
			}

			// Emit new message events for each message so the UI updates
			for _, msg := range newMessages {
				select {
				case p.eventChan <- core.MessageEvent{
					Message: msg,
				}:
				default:
					p.log("SlackProvider.incrementalSyncExistingConversations: Event channel full, skipping event\n")
				}
			}
		} else {
			p.log("SlackProvider.incrementalSyncExistingConversations: No new messages found for %s (last timestamp: %s)\n",
				conv.ProtocolConvID, conv.LastTimestamp.Format(time.RFC3339))
		}

		// Fetch and emit last_read timestamp from Slack API to mark messages as read
		// This ensures messages that were read in another client are marked as read here too
		p.mu.RLock()
		client := p.client
		p.mu.RUnlock()

		if client != nil {
			// Determine actual channel ID (handle DM normalization)
			actualChannelID := conv.ProtocolConvID
			if len(conv.ProtocolConvID) > 0 && conv.ProtocolConvID[0] == 'U' {
				// User ID, need to get channel ID
				channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
					Users: []string{conv.ProtocolConvID},
				})
				if err == nil && channel != nil && channel.ID != "" {
					actualChannelID = channel.ID
				}
			}

			// Get conversation info to retrieve LastRead timestamp
			convInfo, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
				ChannelID: actualChannelID,
			})
			if err == nil && convInfo != nil && convInfo.LastRead != "" {
				// Emit read status event so frontend marks messages as read
				select {
				case p.eventChan <- core.ConversationReadStatusEvent{
					ConversationID: conv.ProtocolConvID,
					LastReadTS:     convInfo.LastRead,
				}:
					p.log("SlackProvider.incrementalSyncExistingConversations: Emitted last_read=%s for %s\n",
						convInfo.LastRead, conv.ProtocolConvID)
				default:
					p.log("SlackProvider.incrementalSyncExistingConversations: Event channel full, skipping read status event\n")
				}
			}
		}

		// Small delay to avoid overwhelming the API
		time.Sleep(100 * time.Millisecond)
	}

	p.log("SlackProvider.incrementalSyncExistingConversations: Completed - synced %d conversations, found %d new messages, fixed %d newly discovered conversations\n",
		successCount, totalNewMessages, newConversationsCreated)

	// If orphaned messages were fixed (new conversations discovered), trigger a contact list refresh
	// so the UI shows those channels in Recent/Unread tabs
	if newConversationsCreated > 0 {
		select {
		case p.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "new_conversations_discovered"}:
			p.log("SlackProvider.incrementalSyncExistingConversations: Triggered contact refresh for %d new conversations\n", newConversationsCreated)
		default:
			p.log("SlackProvider.incrementalSyncExistingConversations: Event channel full, skipping contact refresh\n")
		}
	}

	// Emit completion status
	if totalNewMessages > 0 {
		p.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Found %d new messages in %d conversations", totalNewMessages, successCount), 100)
	} else {
		p.emitSyncStatus(core.SyncStatusCompleted, "All conversations are up to date", 100)
	}

	// Keep status visible for 2 seconds
	time.Sleep(2 * time.Second)

	// Clear status
	p.emitSyncStatus(core.SyncStatusCompleted, "", -1)
}

// Connect establishes the connection with the remote service.
func (p *SlackProvider) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		p.log("SlackProvider.Connect: ERROR - client not initialized\n")
		return fmt.Errorf("slack client not initialized")
	}

	p.log("SlackProvider.Connect: performing auth test\n")
	authInfo, err := p.client.AuthTest()
	if err != nil {
		p.log("SlackProvider.Connect: ERROR - auth test failed: %v\n", err)
		return err
	}
	p.log("SlackProvider.Connect: auth test successful, user=%s, team=%s\n", authInfo.User, authInfo.Team)
	p.selfUserID = authInfo.UserID

	// Determine connection mode based on token type
	token, _ := p.config.GetString("token")

	if strings.HasPrefix(token, "xoxc") {
		// User Token -> Use RTM (Legacy)
		p.log("SlackProvider.Connect: Detected User Token (xoxc), initializing RTM client\n")

		// For RTM with xoxc tokens, we likely need to pass the d cookie in the websocket handshake too
		// slack-go/slack NewRTM allows passing options, including a custom dialer.

		rtmOptions := []slack.RTMOption{
			// slack.RTMOptionUseStart(true), // rtm.start is blocked (not_allowed_token_type), let's stick to default rtm.connect
		}

		dCookie, _ := p.config.GetString("d_cookie")
		if dCookie != "" {
			p.log("SlackProvider.Connect: injecting d cookie into RTM dialer\n")

			jar, _ := cookiejar.New(nil)
			urlObj, _ := url.Parse("https://slack.com") // Cookie needs to be set for the domain
			cookies := []*http.Cookie{
				{Name: "d", Value: dCookie},
			}
			// Handle "d=xoxd..." format if present
			if strings.HasPrefix(dCookie, "d=") {
				cookies[0].Value = dCookie[2:]
			}
			jar.SetCookies(urlObj, cookies)

			// Also set for .slack.com
			urlObj2, _ := url.Parse("https://wss-primary.slack.com")
			jar.SetCookies(urlObj2, cookies)

			// Copy DefaultDialer to avoid modifying global state
			dialer := *websocket.DefaultDialer
			dialer.Jar = jar

			rtmOptions = append(rtmOptions, slack.RTMOptionDialer(&dialer))
			p.log("SlackProvider.Connect: Set cookie jar on RTM dialer\n")
		}

		p.rtmClient = p.client.NewRTM(rtmOptions...)
		go p.startRTM()
	} else {
		// Bot Token -> Use Socket Mode (Modern)
		p.log("SlackProvider.Connect: Detected Bot Token (xoxb), initializing Socket Mode client\n")
		p.socketClient = socketmode.New(
			p.client,
			socketmode.OptionDebug(false), // Set to true if detailed logs are needed
			socketmode.OptionLog(p.logger),
		)
		go p.startSocketMode()
	}

	// Perform initialization tasks in background to avoid blocking Connect return
	// This ensures the UI doesn't hang waiting for emojis/history
	go func() {
		p.log("SlackProvider.Connect: background initialization started\n")

		// Load emojis
		p.log("SlackProvider.Connect: calling loadEmojis\n")
		p.loadEmojis()
		p.log("SlackProvider.Connect: loadEmojis returned\n")

		// Initialize status cache
		p.log("SlackProvider.Connect: calling initializeStatusCache\n")
		p.initializeStatusCache()
		p.log("SlackProvider.Connect: initializeStatusCache returned\n")

		// Start status polling
		go p.pollStatusUpdates()

		// Note: SyncHistory and incrementalSyncExistingConversations are NOT called here.
		// On startup, app.go triggers SyncHistory from domReady() once the frontend is ready
		// to receive sync-status events. For new provider setup (ConfigureProvider), the caller
		// is responsible for triggering sync after Connect().
		p.log("SlackProvider.Connect: background initialization completed\n")
	}()

	p.log("SlackProvider.Connect: END (returning nil)\n")
	return nil
}

// loadEmojis fetches and caches Slack emojis (both standard and custom)
func (p *SlackProvider) loadEmojis() {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return
	}

	p.log("SlackProvider.loadEmojis: fetching emojis from Slack API\n")
	emojis, err := client.GetEmoji()
	if err != nil {
		p.log("SlackProvider.loadEmojis: WARNING - failed to get emojis: %v\n", err)
		return
	}

	p.log("SlackProvider.loadEmojis: received %d emojis from API\n", len(emojis))

	p.emojiCacheMu.Lock()
	defer p.emojiCacheMu.Unlock()

	// Clear existing cache
	p.emojiCache = make(map[string]string)

	// First pass: populate cache with direct emoji URLs
	directCount := 0
	aliasCount := 0
	for name, url := range emojis {
		// Skip aliases for now, we'll handle them in the second pass
		if !strings.HasPrefix(url, "alias:") {
			p.emojiCache[name] = url
			directCount++
		} else {
			aliasCount++
		}
	}
	p.log("SlackProvider.loadEmojis: first pass - %d direct emojis, %d aliases\n", directCount, aliasCount)

	// Second pass: resolve aliases to their target emoji URLs
	// Handle chains of aliases (alias -> alias -> emoji)
	maxIterations := 10 // Prevent infinite loops
	for iteration := 0; iteration < maxIterations; iteration++ {
		resolved := false
		for name, url := range emojis {
			// Skip if already resolved to a direct URL
			if cachedURL, exists := p.emojiCache[name]; exists && !strings.HasPrefix(cachedURL, "alias:") {
				continue
			}

			if strings.HasPrefix(url, "alias:") {
				// Extract the target emoji name
				targetName := strings.TrimPrefix(url, "alias:")
				// Look up the target emoji's URL (might be another alias or direct URL)
				if targetURL, exists := p.emojiCache[targetName]; exists {
					if strings.HasPrefix(targetURL, "alias:") {
						// Target is also an alias, will be resolved in next iteration
						p.emojiCache[name] = url
					} else {
						// Target is a direct URL, resolve the alias
						p.emojiCache[name] = targetURL
						p.log("SlackProvider.loadEmojis: resolved alias '%s' -> '%s' (%s)\n", name, targetName, targetURL)
						resolved = true
					}
				} else {
					// Target not found yet, might be resolved in next iteration
					p.emojiCache[name] = url
				}
			}
		}
		// If no more aliases were resolved, we're done
		if !resolved {
			break
		}
	}

	// Log any remaining unresolved aliases
	unresolvedCount := 0
	for name, url := range p.emojiCache {
		if strings.HasPrefix(url, "alias:") {
			targetName := strings.TrimPrefix(url, "alias:")
			p.log("SlackProvider.loadEmojis: WARNING - unresolved alias '%s' -> '%s' (target not found)\n", name, targetName)
			unresolvedCount++
		}
	}

	p.log("SlackProvider.loadEmojis: loaded %d emojis (%d direct, %d aliases resolved, %d unresolved)\n",
		len(p.emojiCache), len(emojis)-unresolvedCount, len(p.emojiCache)-len(emojis)+unresolvedCount, unresolvedCount)
}

// GetFileData downloads a Slack file and returns it as base64 data URL
// Files are cached locally in Application Support/Loom/slack/<instance_name>/
func (p *SlackProvider) GetFileData(fileURL string) (string, error) {
	p.mu.RLock()
	client := p.client
	config := p.config
	p.mu.RUnlock()

	if client == nil {
		return "", fmt.Errorf("slack client not initialized")
	}

	// Get instance ID and name from config
	instanceID, _ := config.GetString("_instance_id")
	instanceName, _ := config.GetString("_instance_name")
	if instanceName == "" {
		instanceName = instanceID // Fallback to instanceID if name not available
	}
	if instanceName == "" {
		instanceName = "default" // Final fallback
	}

	// Create cache directory path: Application Support/Loom/slack/<instance_name>/
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	cacheDir := filepath.Join(configDir, "Loom", "slack", instanceName)
	if err := os.MkdirAll(cacheDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Generate cache file path from URL (use hash of URL to avoid path issues)
	urlHash := fmt.Sprintf("%x", []byte(fileURL))
	// Try to determine file extension from URL or Content-Type
	ext := filepath.Ext(filepath.Base(fileURL))
	if ext == "" {
		// Default extension based on common patterns
		if strings.Contains(fileURL, "image") {
			ext = ".jpg"
		} else if strings.Contains(fileURL, "video") {
			ext = ".mp4"
		} else if strings.Contains(fileURL, "audio") {
			ext = ".mp3"
		} else {
			ext = ".bin"
		}
	}
	cachePath := filepath.Join(cacheDir, urlHash+ext)

	// Check if file is already cached
	if _, err := os.Stat(cachePath); err == nil {
		// File exists in cache, read and return it
		data, err := os.ReadFile(cachePath)
		if err != nil {
			// Cache file exists but can't be read, fall through to download
		} else {
			// Determine MIME type from file extension
			mimeType := "application/octet-stream"
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
			case ".ogg":
				mimeType = "audio/ogg"
			}
			base64Data := base64.StdEncoding.EncodeToString(data)
			return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
		}
	}

	// File not in cache, download it
	// Get token from config for authentication
	token, _ := config.GetString("token")
	if token == "" {
		return "", fmt.Errorf("slack token not found")
	}

	// Create HTTP request with authentication
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add authorization header
	req.Header.Set("Authorization", "Bearer "+token)

	// Create HTTP client (use the same transport as Slack client if available)
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// If we have a d cookie, add it to the request
	dCookie, _ := config.GetString("d_cookie")
	if dCookie != "" {
		cookieValue := dCookie
		if strings.HasPrefix(cookieValue, "d=") {
			cookieValue = cookieValue[2:]
		}
		req.Header.Set("Cookie", fmt.Sprintf("d=%s", cookieValue))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file: status %d", resp.StatusCode)
	}

	// Read file content
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Save to cache
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		// Log but don't fail - cache write failure shouldn't break the feature
		p.log("SlackProvider.GetFileData: Failed to cache file: %v\n", err)
	}

	// Determine MIME type from Content-Type header or file extension
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		// Fallback to extension-based detection
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
		case ".ogg":
			mimeType = "audio/ogg"
		default:
			mimeType = "application/octet-stream"
		}
	}

	// Encode to base64
	base64Data := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

// GetEmojiURL returns the URL for a Slack emoji, or empty string if not found
// emojiName should be without colons (e.g., "calendar" not ":calendar:")
// Handles aliases by resolving them to their target emoji URLs
func (p *SlackProvider) GetEmojiURL(emojiName string) string {
	// Remove colons if present
	name := strings.TrimPrefix(strings.TrimSuffix(emojiName, ":"), ":")

	p.emojiCacheMu.RLock()
	defer p.emojiCacheMu.RUnlock()

	url := p.emojiCache[name]

	// If found but it's still an alias reference (shouldn't happen after loadEmojis, but handle it anyway)
	if url != "" && strings.HasPrefix(url, "alias:") {
		targetName := strings.TrimPrefix(url, "alias:")
		url = p.emojiCache[targetName]
		if url != "" {
			p.log("SlackProvider.GetEmojiURL: resolved alias '%s' -> '%s' -> %s\n", name, targetName, url)
		}
	}

	if url == "" {
		// Log some sample emoji names from cache for debugging
		sampleCount := 0
		sampleNames := make([]string, 0, 5)
		for cachedName := range p.emojiCache {
			if sampleCount >= 5 {
				break
			}
			sampleNames = append(sampleNames, cachedName)
			sampleCount++
		}
		p.log("SlackProvider.GetEmojiURL: emoji '%s' not found in cache (cache size: %d, samples: %v)\n",
			name, len(p.emojiCache), sampleNames)
	} else if strings.HasPrefix(url, "alias:") {
		// This shouldn't happen after loadEmojis, but handle it anyway
		targetName := strings.TrimPrefix(url, "alias:")
		p.log("SlackProvider.GetEmojiURL: WARNING - emoji '%s' still has unresolved alias '%s'\n", name, targetName)
	} else {
		p.log("SlackProvider.GetEmojiURL: found emoji '%s' -> %s\n", name, url)
	}
	return url
}

// initializeStatusCache loads current user statuses into the cache
func (p *SlackProvider) initializeStatusCache() {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return
	}

	users, err := client.GetUsers()
	if err != nil {
		p.log("SlackProvider.initializeStatusCache: WARNING - failed to get users: %v\n", err)
		return
	}

	p.statusCacheMu.Lock()
	defer p.statusCacheMu.Unlock()

	for _, user := range users {
		if user.Deleted || user.IsBot {
			continue
		}

		status := p.determineStatus(user.Presence, user.Profile.StatusText, user.Profile.StatusEmoji)
		p.statusCache[user.ID] = userStatus{
			status:      status,
			statusEmoji: user.Profile.StatusEmoji,
			statusText:  user.Profile.StatusText,
		}
	}

	p.log("SlackProvider.initializeStatusCache: initialized cache for %d users\n", len(p.statusCache))
}

// determineStatus determines the status string based on presence, statusText, and statusEmoji
func (p *SlackProvider) determineStatus(presence, statusText, statusEmoji string) string {
	status := "offline"

	if presence == "active" {
		statusLower := ""
		if statusText != "" {
			statusLower = strings.ToLower(statusText)
		}

		isMeeting := strings.Contains(statusEmoji, "calendar") ||
			strings.Contains(statusLower, "meeting") ||
			strings.Contains(statusLower, "réunion") ||
			strings.Contains(statusLower, "en réunion")

		if isMeeting {
			status = "meeting"
		} else {
			status = "online"
		}
	} else if presence == "away" {
		statusLower := ""
		if statusText != "" {
			statusLower = strings.ToLower(statusText)
		}

		if strings.Contains(statusLower, "holiday") || strings.Contains(statusLower, "vacation") || strings.Contains(statusLower, "vacances") {
			status = "holiday"
		} else if strings.Contains(statusLower, "busy") || strings.Contains(statusLower, "dnd") || strings.Contains(statusLower, "do not disturb") {
			status = "busy"
		} else if strings.Contains(statusLower, "meeting") || strings.Contains(statusLower, "réunion") || strings.Contains(statusEmoji, "calendar") {
			status = "meeting"
		} else {
			status = "away"
		}
	}

	return status
}

// pollStatusUpdates periodically checks for status changes and emits events
func (p *SlackProvider) pollStatusUpdates() {
	ticker := time.NewTicker(30 * time.Second) // Poll every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.checkStatusChanges()
		case <-p.stopChan:
			p.log("SlackProvider.pollStatusUpdates: stopping polling goroutine\n")
			return
		}
	}
}

// checkStatusChanges checks for status changes and emits ContactStatusEvent if changed
func (p *SlackProvider) checkStatusChanges() {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return
	}

	users, err := client.GetUsers()
	if err != nil {
		p.log("SlackProvider.checkStatusChanges: WARNING - failed to get users: %v\n", err)
		return
	}

	p.statusCacheMu.Lock()
	defer p.statusCacheMu.Unlock()

	for _, user := range users {
		if user.Deleted || user.IsBot {
			continue
		}

		newStatus := p.determineStatus(user.Presence, user.Profile.StatusText, user.Profile.StatusEmoji)
		newStatusEmoji := user.Profile.StatusEmoji
		newStatusText := user.Profile.StatusText

		// Check if status has changed
		cached, exists := p.statusCache[user.ID]
		if !exists || cached.status != newStatus || cached.statusEmoji != newStatusEmoji || cached.statusText != newStatusText {
			// Status changed, emit event
			select {
			case p.eventChan <- core.ContactStatusEvent{
				UserID:      user.ID,
				Status:      newStatus,
				StatusEmoji: newStatusEmoji,
				StatusText:  newStatusText,
			}:
				p.log("SlackProvider.checkStatusChanges: emitted status change for user %s: %s -> %s (emoji: %s, text: %s)\n",
					user.ID, cached.status, newStatus, newStatusEmoji, newStatusText)
			default:
				p.log("SlackProvider.checkStatusChanges: WARNING - event channel full, dropping status change event\n")
			}

			// Update cache
			p.statusCache[user.ID] = userStatus{
				status:      newStatus,
				statusEmoji: newStatusEmoji,
				statusText:  newStatusText,
			}
		}
	}
}

// Disconnect disconnects from the Slack API.
func (p *SlackProvider) Disconnect() error {
	p.log("Slack: Disconnecting...\n")
	p.mu.Lock()
	defer p.mu.Unlock()

	// Close stopChan to signal goroutines to stop
	select {
	case <-p.stopChan:
		// Already closed
	default:
		close(p.stopChan)
	}

	// Disconnect Socket Mode if active
	if p.socketClient != nil {
		// socketmode.Client doesn't have a direct Close/Stop method exposed easily
		// but closing the context (if we used RunContext) or relying on stopChan logic helper
		// mostly we just stop reading events.
		p.socketClient = nil
	}

	// Disconnect RTM if active
	if p.rtmClient != nil {
		p.rtmClient.Disconnect()
		p.rtmClient = nil
	}

	p.client = nil

	// Re-create stopChan for next connection
	p.stopChan = make(chan struct{})

	// Clear status cache
	p.statusCacheMu.Lock()
	p.statusCache = make(map[string]userStatus)
	p.statusCacheMu.Unlock()

	// Close logger
	if p.logger != nil {
		p.logger.Close()
		p.logger = nil
	}

	p.log("Slack: Disconnected\n")
	return nil
}

func (p *SlackProvider) Cleanup() error {
	p.log("SlackProvider.Cleanup: Cleaning up provider data...\n")

	// 1. Disconnect
	_ = p.Disconnect()

	// 2. Remove cache directory
	p.mu.RLock()
	config := p.config
	p.mu.RUnlock()

	instanceID, _ := config.GetString("_instance_id")
	instanceName, _ := config.GetString("_instance_name")
	if instanceName == "" {
		instanceName = instanceID
	}
	if instanceName == "" {
		instanceName = "default"
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	// Slack uses instanceName for cache dir
	cacheDir := filepath.Join(configDir, "Loom", "slack", instanceName)
	p.log("SlackProvider.Cleanup: Removing cache directory: %s\n", cacheDir)
	if err := os.RemoveAll(cacheDir); err != nil {
		p.log("SlackProvider.Cleanup: WARNING - failed to remove cache directory: %v\n", err)
	}

	return nil
}
