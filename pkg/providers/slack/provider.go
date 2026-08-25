// Package slack provides the Slack provider implementation.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/logging"
	"Loom/pkg/models"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// slackSession holds credentials obtained via the browser login flow.
type slackSession struct {
	Token   string `json:"token"`
	DCookie string `json:"d_cookie"`
}

// SlackProvider implements the core.Provider interface for Slack.
type SlackProvider struct {
	config             core.ProviderConfig
	session            *slackSession // set by browser auth; overrides config token/d_cookie
	client             *slack.Client
	apiHTTPClient      *http.Client // HTTP client matching Slack API cookie/auth configuration
	authToken          string       // Token used for raw endpoints not modeled by slack-go v0.17
	apiBaseURL         string       // Slack Web API base URL (overridden by tests)
	apiClientMu        sync.RWMutex // Protects apiHTTPClient and authToken independently of p.mu
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
	connectionCancel   context.CancelFunc      // Stops Socket Mode across reconnects
	dmChannelCache     map[string]string       // Cache: DM channel ID (D...) -> User ID (U...)
	dmChannelCacheMu   sync.RWMutex            // Mutex for DM channel cache
	selfUserID         string                  // Cached authenticated user ID (from AuthTest)
	selfDMChannelID    string                  // Cached channel ID for Slack's DM with yourself
	teamID             string                  // Cached workspace ID used to build Slack web conversation links
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
	response, err := t.Transport.RoundTrip(req)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	body = normalizeSlackResponse(body)
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

// normalizeSlackResponse accepts the response variant returned by some Slack
// user-token endpoints, where the optional top-level "errors" value is an
// object. slack-go declares that field as a slice and otherwise fails before it
// can inspect the authoritative "ok" and "error" fields.
func normalizeSlackResponse(body []byte) []byte {
	var response map[string]json.RawMessage
	if json.Unmarshal(body, &response) != nil {
		return body
	}
	errors, ok := response["errors"]
	trimmedErrors := bytes.TrimSpace(errors)
	if !ok || len(trimmedErrors) == 0 || trimmedErrors[0] != '{' {
		return body
	}
	delete(response, "errors")
	normalized, err := json.Marshal(response)
	if err != nil {
		return body
	}
	return normalized
}

// Ensure interface compliance
var _ core.Provider = (*SlackProvider)(nil)

// NewSlackProvider creates a new instance of the SlackProvider.
func NewSlackProvider() *SlackProvider {
	return &SlackProvider{
		userCache:          make(map[string]*slack.User),
		emojiCache:         make(map[string]string),
		eventChan:          make(chan core.ProviderEvent, 500), // Increased buffer to handle high-volume sync
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

	// Load persisted browser-auth session (may not exist for manual-token users).
	p.mu.Lock()
	_ = p.loadSessionLocked()
	p.mu.Unlock()

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

	// A manual token in the config takes priority over the browser-auth session.
	// This lets the user switch from xoxc (browser) to xoxp/xoxb without being
	// silently overridden by the stale session file.
	if token != "" {
		if p.session != nil {
			p.session = nil
			_ = os.Remove(p.sessionPath())
		}
	} else if p.session != nil {
		// No manual token provided → fall back to browser session.
		token = p.session.Token
		dCookie = p.session.DCookie
		p.config["token"] = token
		p.config["d_cookie"] = dCookie
	}

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

		apiHTTPClient := http.DefaultClient
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
			apiHTTPClient = client
			opts = append(opts, slack.OptionHTTPClient(client))
		}

		p.client = slack.New(token, opts...)
		p.apiClientMu.Lock()
		p.apiHTTPClient = apiHTTPClient
		p.authToken = token
		p.apiBaseURL = "https://slack.com/api"
		p.apiClientMu.Unlock()
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
	if p.session != nil {
		return true
	}
	token, ok := p.config.GetString("token")
	return ok && token != ""
}

func (p *SlackProvider) sessionPath() string {
	instanceID, _ := p.config.GetString("_instance_id")
	if instanceID == "" {
		instanceID = "slack-1"
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Loom", instanceID, "slack-session.json")
}

func (p *SlackProvider) loadSessionLocked() error {
	path := p.sessionPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("slack: read session: %w", err)
	}
	var stored slackSession
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("slack: decode session: %w", err)
	}
	p.session = &stored
	return nil
}

func (p *SlackProvider) saveSessionLocked() error {
	if p.session == nil {
		return fmt.Errorf("slack: no session to save")
	}
	path := p.sessionPath()
	if path == "" {
		return fmt.Errorf("slack: user configuration directory is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(p.session)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}

// incrementalSyncExistingConversations syncs new messages for conversations that already have message history
// incrementalSyncExistingConversations fetches new messages for conversations already in the DB.
// contactLatestTS maps convID -> Slack's latest message timestamp (from GetConversations API).
// contactLastRead maps convID -> Slack's last_read timestamp.
// Both maps are used to skip channels with no new messages and avoid redundant API calls.
func (p *SlackProvider) incrementalSyncExistingConversations(contactLatestTS, contactLastRead map[string]string) {
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
			AND (protocol_conv_id LIKE 'C%' OR protocol_conv_id LIKE 'D%' OR protocol_conv_id LIKE 'G%' OR protocol_conv_id LIKE 'U%'
				OR protocol_conv_id LIKE '%::C%' OR protocol_conv_id LIKE '%::D%' OR protocol_conv_id LIKE '%::G%' OR protocol_conv_id LIKE '%::U%')
		GROUP BY protocol_conv_id
		ORDER BY MAX(timestamp) DESC
		LIMIT 50
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

	// Batch orphan check: one query for all conversations instead of N queries.
	orphanSet := make(map[string]bool)
	if db.DB != nil {
		var orphanIDs []string
		db.DB.Model(&models.Message{}).
			Where("conversation_id = 0").
			Distinct("protocol_conv_id").
			Pluck("protocol_conv_id", &orphanIDs)
		for _, id := range orphanIDs {
			orphanSet[id] = true
		}
	}

	successCount := 0
	totalNewMessages := 0
	newConversationsCreated := 0

	// Sync each conversation
	for i, conv := range conversations {
		// Limit synchronization to recent rooms (e.g., active in last 30 days) to save CPU
		// if we have hundreds of rooms.
		if len(conversations) > 50 && time.Since(conv.LastTimestamp) > 30*24*time.Hour {
			continue
		}

		// Skip conversations where Slack confirms no new messages since our last stored one.
		// contactLatestTS holds the timestamp of the most recent message in the channel
		// as returned by GetConversations — no API call needed to know there's nothing new.
		if slackLatestStr, ok := contactLatestTS[core.StripConvID(conv.ProtocolConvID)]; ok && slackLatestStr != "" {
			slackLatestF, err := strconv.ParseFloat(slackLatestStr, 64)
			dbLatestF := float64(conv.LastTimestamp.UnixNano()) / 1e9
			if err == nil && slackLatestF <= dbLatestF {
				p.log("SlackProvider.incrementalSyncExistingConversations: Skipping %s (slack_latest=%s <= db_latest=%f)\n",
					conv.ProtocolConvID, slackLatestStr, dbLatestF)
				continue
			}
		}

		progress := int((float64(i+1) / float64(len(conversations))) * 100)
		p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing %s... (%d/%d)", conv.ProtocolConvID, i+1, len(conversations)), progress)

		// Fix orphaned messages saved by pollGlobalUpdates (ConversationID=0).
		// Uses the pre-fetched orphanSet to avoid per-conversation SQL queries.
		if orphanSet[conv.ProtocolConvID] && db.DB != nil {
			normalizedConvID := p.normalizeDMConversationID(core.StripConvID(conv.ProtocolConvID))
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

		sinceTimestamp := conv.LastTimestamp

		p.log("SlackProvider.incrementalSyncExistingConversations: Syncing %s (last message: %s, looking for messages after)\n",
			conv.ProtocolConvID, conv.LastTimestamp.Format(time.RFC3339))

		// Emit last_read BEFORE messages so the frontend knows the read state when messages arrive.
		if lastRead, ok := contactLastRead[conv.ProtocolConvID]; ok && lastRead != "" {
			select {
			case p.eventChan <- core.ConversationReadStatusEvent{InstanceID: p.getInstanceId(),
				ConversationID: conv.ProtocolConvID,
				LastReadTS:     lastRead,
			}:
				p.log("SlackProvider.incrementalSyncExistingConversations: Emitted last_read=%s for %s\n",
					lastRead, conv.ProtocolConvID)
			default:
				p.log("SlackProvider.incrementalSyncExistingConversations: Event channel full, skipping read status event\n")
			}
		}

		newMessages, err := p.GetConversationHistory(conv.ProtocolConvID, 1000, nil, &sinceTimestamp)
		if err != nil {
			p.log("SlackProvider.incrementalSyncExistingConversations: Failed to sync %s: %v\n", conv.ProtocolConvID, err)
			continue
		}

		if len(newMessages) > 0 {
			p.log("SlackProvider.incrementalSyncExistingConversations: Found %d new messages for %s\n", len(newMessages), conv.ProtocolConvID)
			successCount++
			totalNewMessages += len(newMessages)

			// Emit one batch event per conversation instead of one MessageEvent per message.
			// This reduces React re-renders and macOS badge API calls from N to 1 per conversation.
			select {
			case p.eventChan <- core.MessageBatchEvent{
				InstanceID:     p.getInstanceId(),
				ConversationID: conv.ProtocolConvID,
				Messages:       newMessages,
			}:
			default:
				p.log("SlackProvider.incrementalSyncExistingConversations: Event channel full, skipping batch event\n")
			}
		} else {
			p.log("SlackProvider.incrementalSyncExistingConversations: No new main messages for %s — checking thread replies\n",
				conv.ProtocolConvID)
			// GetConversationHistory only returns main messages, not thread replies.
			// When Slack says a conversation has newer activity but no new main messages exist,
			// the gap must be new thread replies. Sync them so ordering stays accurate.
			newReplies := p.refreshThreadReplies(conv.ProtocolConvID)
			if newReplies > 0 {
				totalNewMessages += newReplies
				successCount++
			} else {
				// Lookback: only when forward sync AND thread refresh found nothing.
				// Detects messages in the 24h window before the last known message that were
				// read on another client before this sync ran (so forward sync skips them).
				const lookbackWindow = 24 * time.Hour
				if time.Since(conv.LastTimestamp) < 48*time.Hour {
					lookbackBefore := conv.LastTimestamp
					lookbackSince := conv.LastTimestamp.Add(-lookbackWindow)
					missed, err := p.lookbackSyncConversation(conv.ProtocolConvID, lookbackBefore, lookbackSince)
					if err != nil {
						p.log("SlackProvider.incrementalSyncExistingConversations: Lookback failed for %s: %v\n", conv.ProtocolConvID, err)
					} else if len(missed) > 0 {
						p.log("SlackProvider.incrementalSyncExistingConversations: Lookback found %d missed messages for %s\n", len(missed), conv.ProtocolConvID)
						successCount++
						totalNewMessages += len(missed)
						select {
						case p.eventChan <- core.MessageBatchEvent{
							InstanceID:     p.getInstanceId(),
							ConversationID: conv.ProtocolConvID,
							Messages:       missed,
						}:
						default:
							p.log("SlackProvider.incrementalSyncExistingConversations: Event channel full, dropping lookback batch\n")
						}
					}
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
		case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(), UserID: "refresh", Status: "new_conversations_discovered"}:
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

// refreshThreadReplies fetches new replies for the most recent parent messages in a
// conversation and stores them. Called when the incremental sync found no new main
// messages but Slack reports newer channel activity (i.e. the gap is thread replies).
// Returns the number of new reply messages stored.
func (p *SlackProvider) refreshThreadReplies(convID string) int {
	if db.DB == nil {
		return 0
	}

	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return 0
	}

	// Collect the most recent parent messages (non-thread-reply) from the last 14 days.
	var parentRows []struct {
		ProtocolMsgID string
		MaxReplyTS    *time.Time
	}
	cutoff := time.Now().AddDate(0, 0, -14)
	db.DB.Raw(`
		SELECT m.protocol_msg_id,
		       MAX(r.timestamp) AS max_reply_ts
		FROM messages m
		LEFT JOIN messages r
		       ON r.protocol_conv_id = m.protocol_conv_id
		      AND r.thread_id        = m.protocol_msg_id
		      AND r.protocol_msg_id != m.protocol_msg_id
		WHERE m.protocol_conv_id = ?
		  AND (m.thread_id IS NULL OR m.thread_id = '' OR m.thread_id = m.protocol_msg_id)
		  AND m.timestamp >= ?
		GROUP BY m.protocol_msg_id
		ORDER BY m.timestamp DESC
		LIMIT 10
	`, convID, cutoff).Scan(&parentRows)

	if len(parentRows) == 0 {
		return 0
	}

	// Resolve the actual Slack channel ID (U-prefix DMs need the D-prefix channel).
	actualChannelID := convID
	if len(convID) > 0 && convID[0] == 'U' {
		ch, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{convID},
			ReturnIM: true,
		})
		if err != nil || ch == nil || ch.ID == "" {
			p.log("SlackProvider.refreshThreadReplies: failed to open DM for %s: %v\n", convID, err)
			return 0
		}
		actualChannelID = ch.ID
	}

	var allReplies []models.Message
	for _, row := range parentRows {
		var oldest time.Time
		if row.MaxReplyTS != nil {
			oldest = *row.MaxReplyTS
		}
		replies, err := p.getThreadReplies(actualChannelID, row.ProtocolMsgID, oldest)
		if err != nil {
			p.log("SlackProvider.refreshThreadReplies: failed to get replies for thread %s: %v\n", row.ProtocolMsgID, err)
			continue
		}
		allReplies = append(allReplies, replies...)
	}

	if len(allReplies) == 0 {
		return 0
	}

	// A thread without a locally stored reply has no MaxReplyTS cursor, so Slack
	// may return older replies alongside the new one. Only publish IDs that are
	// genuinely absent from SQLite; otherwise a reconnect would resurrect old
	// replies as unread notifications.
	messageIDs := make([]string, 0, len(allReplies))
	for _, reply := range allReplies {
		if reply.ProtocolMsgID != "" {
			messageIDs = append(messageIDs, reply.ProtocolMsgID)
		}
	}
	var existingIDs []string
	if len(messageIDs) > 0 {
		db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ? AND protocol_msg_id IN ?", convID, messageIDs).
			Pluck("protocol_msg_id", &existingIDs)
	}
	existing := make(map[string]struct{}, len(existingIDs))
	for _, id := range existingIDs {
		existing[id] = struct{}{}
	}
	newReplies := make([]models.Message, 0, len(allReplies))
	for _, reply := range allReplies {
		if _, found := existing[reply.ProtocolMsgID]; !found {
			newReplies = append(newReplies, reply)
		}
	}
	if len(newReplies) == 0 {
		return 0
	}

	p.storeMessagesForConversation(convID, newReplies)
	p.log("SlackProvider.refreshThreadReplies: stored %d new thread replies for %s\n", len(newReplies), convID)
	select {
	case p.eventChan <- core.MessageBatchEvent{
		InstanceID:     p.getInstanceId(),
		ConversationID: convID,
		Messages:       newReplies,
	}:
	default:
		p.log("SlackProvider.refreshThreadReplies: event channel full, dropping batch event\n")
	}
	return len(newReplies)
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
	if p.selfUserID != authInfo.UserID {
		p.selfDMChannelID = ""
	}
	p.selfUserID = authInfo.UserID
	p.teamID = authInfo.TeamID
	if p.connectionCancel != nil {
		p.connectionCancel()
	}
	connectionCtx, connectionCancel := context.WithCancel(context.Background())
	p.connectionCancel = connectionCancel

	// Determine connection mode based on token type
	token, _ := p.config.GetString("token")

	if strings.HasPrefix(token, "xoxc") || strings.HasPrefix(token, "xoxp") {
		// Web-client (xoxc) and OAuth user (xoxp) tokens both use RTM.
		// xoxc requires the d cookie in the WebSocket dialer; xoxp authenticates
		// via the token alone and does not need a cookie.
		p.log("SlackProvider.Connect: Detected user token (%s...), initializing RTM client\n", token[:5])

		rtmOptions := []slack.RTMOption{}

		if strings.HasPrefix(token, "xoxc") {
			dCookie, _ := p.config.GetString("d_cookie")
			if dCookie != "" {
				p.log("SlackProvider.Connect: injecting d cookie into RTM dialer\n")

				jar, _ := cookiejar.New(nil)
				urlObj, _ := url.Parse("https://slack.com")
				cookies := []*http.Cookie{{Name: "d", Value: dCookie}}
				if strings.HasPrefix(dCookie, "d=") {
					cookies[0].Value = dCookie[2:]
				}
				jar.SetCookies(urlObj, cookies)
				urlObj2, _ := url.Parse("https://wss-primary.slack.com")
				jar.SetCookies(urlObj2, cookies)

				dialer := *websocket.DefaultDialer
				dialer.Jar = jar
				rtmOptions = append(rtmOptions, slack.RTMOptionDialer(&dialer))
				p.log("SlackProvider.Connect: Set cookie jar on RTM dialer\n")
			}
		}

		p.rtmClient = p.client.NewRTM(rtmOptions...)
		go p.startRTM(connectionCtx)
	} else {
		// Bot Token (xoxb) -> Use Socket Mode (Modern)
		p.log("SlackProvider.Connect: Detected Bot Token (xoxb), initializing Socket Mode client\n")
		p.socketClient = socketmode.New(
			p.client,
			socketmode.OptionDebug(false),
			socketmode.OptionLog(p.logger),
		)
		go p.startSocketMode(connectionCtx, p.socketClient)
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
		go p.pollStatusUpdates(connectionCtx)

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
		} else if bytes.HasPrefix(data, []byte("<!")) || bytes.HasPrefix(data, []byte("<html")) {
			// Stale HTML error page from a previous failed download — evict and re-download.
			_ = os.Remove(cachePath)
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

	// File not in cache, download it with the already configured Slack client.
	// This matters for browser sessions (xoxc + d cookie): the client's custom
	// transport injects the cookie on every request, including requests created
	// while following redirects from files.slack.com. A Cookie header attached
	// only to the initial request can be stripped by net/http on a cross-host
	// redirect, which makes Slack answer with its HTML login page.
	downloadURL := normalizeSlackDownloadURL(fileURL)
	var downloaded bytes.Buffer
	if err := client.GetFile(downloadURL, &downloaded); err != nil {
		return "", fmt.Errorf("failed to download Slack file: %w", err)
	}
	data := downloaded.Bytes()
	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "text/html") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("<!DOCTYPE html")) {
		return "", fmt.Errorf("slack returned HTML instead of file data (browser session is not authorized for files.slack.com): %s", fileURL)
	}

	// Save to cache
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		// Log but don't fail - cache write failure shouldn't break the feature
		p.log("SlackProvider.GetFileData: Failed to cache file: %v\n", err)
	}

	// Determine MIME type from Content-Type header or file extension
	mimeType := contentType
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

// normalizeSlackDownloadURL upgrades url_private links to the binary
// url_private_download form. Slack may answer the former with an HTTP 200 login
// page even when the API token is otherwise valid.
func normalizeSlackDownloadURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "files.slack.com") {
		return rawURL
	}
	if !strings.HasPrefix(u.Path, "/files-pri/") || strings.Contains(u.Path, "/download/") {
		return rawURL
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) < 4 {
		return rawURL
	}
	parts = append(parts[:3], append([]string{"download"}, parts[3:]...)...)
	u.Path = strings.Join(parts, "/")
	return u.String()
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

	users, err := client.GetUsers(slack.GetUsersOptionPresence(true))
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

// determineStatus determines the status string based on presence, statusText, and statusEmoji.
func (p *SlackProvider) determineStatus(presence, statusText, statusEmoji string) string {
	switch presence {
	case "active":
		return "online"
	case "away":
		return "away"
	default:
		return "offline"
	}
}

func (p *SlackProvider) RefreshContactStatuses(userIDs []string) map[string]string {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	result := make(map[string]string, len(userIDs))
	if client == nil {
		return result
	}
	type presenceResult struct{ userID, status string }
	results := make(chan presenceResult, len(userIDs))
	semaphore := make(chan struct{}, 6)
	var wg sync.WaitGroup
	seen := make(map[string]bool, len(userIDs))
	for _, userID := range userIDs {
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			presence, err := client.GetUserPresence(id)
			if err != nil {
				return
			}
			status := p.determineStatus(presence.Presence, "", "")
			results <- presenceResult{userID: id, status: status}
		}(userID)
	}
	wg.Wait()
	close(results)
	for item := range results {
		result[item.userID] = item.status
		p.statusCacheMu.Lock()
		cached := p.statusCache[item.userID]
		cached.status = item.status
		p.statusCache[item.userID] = cached
		p.statusCacheMu.Unlock()
		if account, ok := db.ContactStore.FindByProviderUser(p.getInstanceId(), item.userID); ok {
			account.Status = item.status
			db.ContactStore.UpsertLinkedAccount(account)
			if db.DB != nil {
				db.DB.Model(&account).Update("status", item.status)
			}
		}
	}
	return result
}

// pollStatusUpdates periodically checks for status changes and emits events
func (p *SlackProvider) pollStatusUpdates(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Poll every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.checkStatusChanges()
		case <-ctx.Done():
			p.log("SlackProvider.pollStatusUpdates: stopping polling goroutine\n")
			return
		}
	}
}

// checkStatusChanges polls real-time presence for all known DM contacts and persists changes.
// Uses users.getPresence (not users.list presence=true which is deprecated/unreliable).
func (p *SlackProvider) checkStatusChanges() {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return
	}

	instanceID := p.getInstanceId()
	contacts := db.ContactStore.FindByProvider(instanceID)

	p.statusCacheMu.Lock()
	defer p.statusCacheMu.Unlock()

	changed := false
	for _, contact := range contacts {
		if contact.IsGroup || contact.UserID == "" {
			continue
		}

		userPresence, err := client.GetUserPresence(contact.UserID)
		if err != nil {
			continue
		}

		cached := p.statusCache[contact.UserID]
		newStatus := p.determineStatus(userPresence.Presence, cached.statusText, cached.statusEmoji)

		if cached.status == newStatus {
			continue
		}

		if la, ok := db.ContactStore.FindByProviderUser(instanceID, contact.UserID); ok {
			la.Status = newStatus
			db.ContactStore.UpsertLinkedAccount(la)
			if db.DB != nil {
				db.DB.Model(&la).Update("status", newStatus)
			}
		}
		select {
		case p.eventChan <- core.ContactStatusEvent{
			InstanceID:  instanceID,
			UserID:      contact.UserID,
			Status:      newStatus,
			StatusEmoji: cached.statusEmoji,
			StatusText:  cached.statusText,
		}:
		default:
		}
		p.statusCache[contact.UserID] = userStatus{status: newStatus, statusEmoji: cached.statusEmoji, statusText: cached.statusText}
		changed = true
	}

	if changed {
		select {
		case p.eventChan <- core.ContactStatusEvent{InstanceID: instanceID, UserID: "refresh", Status: "sync_complete"}:
		default:
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
	if p.eventStreamCancel != nil {
		p.eventStreamCancel()
		p.eventStreamCancel = nil
		p.eventStreamCtx = nil
		p.eventStreamStarted = false
	}
	if p.connectionCancel != nil {
		p.connectionCancel()
		p.connectionCancel = nil
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

	// Re-create stopChan for next connection
	p.stopChan = make(chan struct{})

	// Clear status cache
	p.statusCacheMu.Lock()
	p.statusCache = make(map[string]userStatus)
	p.statusCacheMu.Unlock()

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

	// Remove browser-auth session file.
	p.mu.Lock()
	sessionFile := p.sessionPath()
	p.session = nil
	p.mu.Unlock()
	if sessionFile != "" {
		_ = os.Remove(sessionFile)
	}

	if p.logger != nil {
		p.logger.Close()
		p.logger = nil
	}

	return nil
}

func (p *SlackProvider) GetCapabilities() core.Capabilities {
	// Positive allowlist: only officially-supported token types can use scheduling APIs.
	//   xoxb-* (bot/workspace-app token) → chat.scheduleMessage ✓, scheduledMessages.list ✗
	//   xoxp-* (OAuth user token)        → both ✓
	//   xoxc-* (web-client token)        → REST API unreliable; RTM only → both ✗
	//   anything else (legacy, unknown)  → both ✗
	token, _ := p.config.GetString("token")

	var canSchedule, canListScheduled bool
	switch {
	case strings.HasPrefix(token, "xoxb"):
		canSchedule = true
		canListScheduled = false
	case strings.HasPrefix(token, "xoxp"):
		canSchedule = true
		canListScheduled = true
	}

	return core.Capabilities{
		SupportsThreads:               true,
		SupportsReactions:             true,
		SupportsCustomEmojis:          true,
		SupportsTypingIndicator:       true,
		SupportsGroupManagement:       true,
		SupportsAddGroupMembers:       true,
		SupportsRemoveGroupMembers:    true,
		SupportsRenameGroup:           true,
		SupportsGroupDescription:      true,
		SupportsLeaveGroup:            true,
		SupportsDeleteMessage:         true,
		SupportsEditMessage:           true,
		SupportsReadReceipts:          false,
		SupportsPinConversation:       false,
		SupportsPinMessage:            true,
		SupportsListMessagePins:       true,
		SupportsScheduledMessages:     canSchedule,
		SupportsListScheduledMessages: canListScheduled,
		MessagePinScope:               string(models.MessagePinScopeShared),
		SupportsMuteConversation:      false,
		SupportsQRCodeAuth:            false,
		SupportsContactDirectory:      true,
		SupportsDirectConversation:    true,
		SupportsGroupConversation:     true,
		SupportsGroupTitle:            true,
		RequiresGroupTitle:            true,
		GroupConversationTypes:        "group_message,private_channel,public_channel",
	}
}

func (p *SlackProvider) GetCustomEmojis() (map[string]string, error) {
	p.emojiCacheMu.RLock()
	defer p.emojiCacheMu.RUnlock()

	// Return a copy of the emoji cache
	emojis := make(map[string]string, len(p.emojiCache))
	for k, v := range p.emojiCache {
		emojis[k] = v
	}
	return emojis, nil
}

func (p *SlackProvider) GetAuthQRCode() (string, error) {
	return "", fmt.Errorf("Slack does not support QR code authentication")
}

func (p *SlackProvider) RefreshContact(contactID string) error {
	// For Slack, we can just resolve the user name again which updates the cache
	p.ResolveUserNames([]string{core.StripConvID(contactID)})
	return nil
}

func (p *SlackProvider) getInstanceId() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	id, _ := p.config.GetString("_instance_id")
	return id
}
