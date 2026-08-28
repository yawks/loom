// Package googlechat provides the Google Chat provider using the official REST API + OAuth2.
package googlechat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"Loom/pkg/core"
	"Loom/pkg/logging"
	"Loom/pkg/models"
)

var chatScopes = []string{
	"https://www.googleapis.com/auth/chat.messages",
	"https://www.googleapis.com/auth/chat.messages.reactions",
	"https://www.googleapis.com/auth/chat.spaces",
	"https://www.googleapis.com/auth/chat.memberships",
	"https://www.googleapis.com/auth/directory.readonly",
	"https://www.googleapis.com/auth/contacts.readonly",
	"openid",
	"email",
	"profile",
}

type cachedUser struct {
	Name      string
	AvatarURL string
	Email     string
}

// GoogleChatProvider implements core.Provider using the Google Chat REST API.
type GoogleChatProvider struct {
	config core.ProviderConfig
	mu     sync.RWMutex
	// syncMu serializes contact discovery and history catch-up. At startup both
	// Connect and the app-level startup sync can request discovery; letting them
	// overlap can make the history query observe a partially refreshed contact
	// graph and skip conversations until the next manual sync.
	syncMu sync.Mutex
	logger *logging.ProviderLogger

	oauthConf *oauth2.Config
	apiClient *http.Client
	selfID    string
	selfName  string

	userCache map[string]cachedUser
	userMu    sync.RWMutex

	// threadNameByMsgID maps a message's ProtocolMsgID to its Google Chat thread name.
	// Populated during GetConversationHistory; used by SendReply to avoid extra API calls.
	threadNameByMsgID   map[string]string
	threadNameByMsgIDMu sync.RWMutex

	// spaceWebIDCache maps REST space name ("spaces/XXX") to the web client space ID
	// extracted from SpaceUri (e.g. "_q55RHW5d6o" from "https://...chat/r/spaces/_q55RHW5d6o").
	// Required to build correct create_unsent_message payloads.
	spaceWebIDCache map[string]string
	spaceIsDMCache  map[string]bool
	spaceWebIDMu    sync.RWMutex

	webClient *googleChatWebClient

	lastSeen   map[string]time.Time
	lastSeenMu sync.Mutex

	eventChan chan core.ProviderEvent
	ctx       context.Context
	cancel    context.CancelFunc
}

var _ core.Provider = (*GoogleChatProvider)(nil)
var _ core.ScheduledMessageProvider = (*GoogleChatProvider)(nil)

func NewGoogleChatProvider() *GoogleChatProvider {
	p := &GoogleChatProvider{
		eventChan:         make(chan core.ProviderEvent, 500),
		userCache:         make(map[string]cachedUser),
		threadNameByMsgID: make(map[string]string),
		lastSeen:          make(map[string]time.Time),
		spaceWebIDCache:   make(map[string]string),
		spaceIsDMCache:    make(map[string]bool),
	}
	p.webClient = newGoogleChatWebClient(p.getInstanceID(), nil, p.handleWebAuthExpired)
	return p
}

// --- Lifecycle ---

func (p *GoogleChatProvider) Init(config core.ProviderConfig) error {
	instanceID := p.instanceIDFrom(config)
	if logger, err := logging.GetLogger("googlechat", instanceID); err == nil {
		p.logger = logger
	}
	return p.SetConfig(config)
}

func (p *GoogleChatProvider) GetConfig() core.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

func (p *GoogleChatProvider) SetConfig(config core.ProviderConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config

	clientID, _ := config.GetString("client_id")
	clientSecret, _ := config.GetString("client_secret")
	if clientID == "" || clientSecret == "" {
		return nil
	}
	p.oauthConf = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       chatScopes,
		Endpoint:     google.Endpoint,
	}
	return nil
}

func (p *GoogleChatProvider) IsAuthenticated() bool {
	_, err := p.loadToken()
	return err == nil
}

func (p *GoogleChatProvider) Connect() error {
	p.log("GoogleChatProvider.Connect: connecting...\n")
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.ctx = ctx
	p.cancel = cancel
	oauthConf := p.oauthConf
	p.mu.Unlock()

	if oauthConf == nil {
		return fmt.Errorf("googlechat: not configured (missing client_id/client_secret)")
	}

	token, err := p.loadToken()
	if err != nil {
		p.log("GoogleChatProvider.Connect: no saved token, starting OAuth flow\n")
		go p.runOAuthFlow(ctx, oauthConf)
		return nil
	}

	return p.connectWithToken(ctx, oauthConf, token)
}

func (p *GoogleChatProvider) connectWithToken(ctx context.Context, oauthConf *oauth2.Config, token *oauth2.Token) error {
	httpClient := oauthConf.Client(ctx, token)

	p.mu.Lock()
	p.apiClient = httpClient
	p.mu.Unlock()

	// Load saved web cookies if available for scheduled messages
	if cookies, err := p.loadWebCookies(); err == nil && len(cookies) > 0 {
		p.webClient.SetCookies(cookies)
	}

	if err := p.fetchSelf(); err != nil {
		p.log("GoogleChatProvider.Connect: fetchSelf failed: %v\n", err)
	} else {
		p.log("GoogleChatProvider.Connect: connected as %s\n", p.selfName)
	}

	go p.pollLoop(ctx)

	// Initial contact + conversation sync
	go func() {
		p.syncMu.Lock()
		contacts, err := p.GetContacts()
		p.syncMu.Unlock()
		if err != nil {
			p.log("GoogleChatProvider: initial GetContacts error: %v\n", err)
		} else {
			p.log("GoogleChatProvider: initial GetContacts OK: %d contacts\n", len(contacts))
		}
		refresh := core.ContactStatusEvent{
			InstanceID: p.getInstanceID(),
			UserID:     "refresh",
			Status:     "new_conversations_discovered",
		}
		p.emit(refresh)
		// Re-emit after a delay: the first event can be missed if the backend
		// event loop isn't ready yet (e.g. right after the OAuth flow completes).
		time.Sleep(3 * time.Second)
		p.emit(refresh)
	}()

	return nil
}

func (p *GoogleChatProvider) Disconnect() error {
	p.log("GoogleChatProvider.Disconnect: disconnecting\n")
	p.mu.Lock()
	cancel := p.cancel
	p.apiClient = nil
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (p *GoogleChatProvider) Cleanup() error {
	_ = os.Remove(p.tokenFilePath())
	_ = os.Remove(p.webCookiesFilePath())
	if p.logger != nil {
		p.logger.Close()
		p.logger = nil
	}
	return nil
}

func (p *GoogleChatProvider) SyncHistory(since time.Time) error {
	if p.getHTTPClient() == nil {
		return fmt.Errorf("googlechat: not connected")
	}
	// Do not report success to the app (which advances LastSyncAt) until the
	// actual catch-up has completed. This also waits for Connect's initial
	// contact discovery instead of racing it.
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	if _, err := p.GetContacts(); err != nil {
		return fmt.Errorf("googlechat: refresh contacts: %w", err)
	}
	p.emit(core.ContactStatusEvent{
		InstanceID: p.getInstanceID(),
		UserID:     "refresh",
		Status:     "new_conversations_discovered",
	})
	// Per-conversation forward sync + 24h lookback to catch messages that were
	// missed because they were read on another client before this sync ran.
	p.incrementalSync()
	return nil
}

func (p *GoogleChatProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	return p.eventChan, nil
}

func (p *GoogleChatProvider) GetCapabilities() core.Capabilities {
	return core.Capabilities{
		ReadCursorAuthoritativeForNewMessages: true,
		SupportsThreads:                       true,
		SupportsReactions:                     true,
		SupportsLeaveGroup:                    true,
		SupportsDeleteMessage:                 true,
		SupportsEditMessage:                   true,
		SupportsPinMessage:                    true,
		SupportsListMessagePins:               true,
		SupportsScheduledMessages:             true,
		SupportsListScheduledMessages:         true,
		MessagePinScope:                       string(models.MessagePinScopeShared),
		SupportsQRCodeAuth:                    false,
		NativeEmojiReactions:                  true,
		SupportsRenameGroup:                   true,
		SupportsAddGroupMembers:               true,
		SupportsRemoveGroupMembers:            true,
		SupportsGroupAdminRoles:               true,
		SupportsContactDirectory:              true,
		SupportsGroupConversation:             true,
		SupportsGroupTitle:                    true,
		RequiresGroupTitle:                    true,
		GroupConversationTypes:                "group",
	}
}

func (p *GoogleChatProvider) LoginWithGaiaBrowser(ctx context.Context) (map[string]string, error) {
	cookies, err := FetchGoogleChatCookiesViaLogin(ctx)
	if err != nil {
		return nil, fmt.Errorf("googlechat: gaia login failed: %w", err)
	}

	if err := p.saveWebCookies(cookies); err != nil {
		p.log("GoogleChatProvider: save web cookies failed: %v\n", err)
	}

	p.webClient.SetCookies(cookies)
	p.emit(core.ContactStatusEvent{
		InstanceID: p.getInstanceID(),
		UserID:     "system",
		Status:     "connected",
	})
	return cookies, nil
}

func (p *GoogleChatProvider) GetWebCookies() (map[string]string, error) {
	return p.loadWebCookies()
}

func (p *GoogleChatProvider) GetCustomEmojis() (map[string]string, error) {
	return nil, nil
}

func (p *GoogleChatProvider) GetAuthQRCode() (string, error) {
	return "", fmt.Errorf("googlechat: use OAuth2 flow — no QR code")
}

// --- Token persistence ---

func (p *GoogleChatProvider) tokenFilePath() string {
	configDir, _ := os.UserConfigDir()
	return filepath.Join(configDir, "Loom", "googlechat-"+p.getInstanceID()+"-tokens.json")
}

func (p *GoogleChatProvider) loadToken() (*oauth2.Token, error) {
	data, err := os.ReadFile(p.tokenFilePath())
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token in saved credentials")
	}
	return &token, nil
}

func (p *GoogleChatProvider) saveToken(token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p.tokenFilePath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(p.tokenFilePath(), data, 0600)
}

func (p *GoogleChatProvider) webCookiesFilePath() string {
	configDir, _ := os.UserConfigDir()
	return filepath.Join(configDir, "Loom", "googlechat-"+p.getInstanceID()+"-cookies.json")
}

func (p *GoogleChatProvider) loadWebCookies() (map[string]string, error) {
	data, err := os.ReadFile(p.webCookiesFilePath())
	if err != nil {
		return nil, err
	}
	var cookies map[string]string
	if err := json.Unmarshal(data, &cookies); err != nil {
		return nil, err
	}
	return cookies, nil
}

func (p *GoogleChatProvider) saveWebCookies(cookies map[string]string) error {
	data, err := json.Marshal(cookies)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p.webCookiesFilePath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(p.webCookiesFilePath(), data, 0600)
}

func (p *GoogleChatProvider) handleWebAuthExpired() {
	p.log("GoogleChatProvider: web session cookies expired — emitting auth_expired status event\n")
	p.emit(core.ContactStatusEvent{
		InstanceID: p.getInstanceID(),
		UserID:     "system",
		Status:     "auth_expired",
	})
}

// --- Helpers ---

func (p *GoogleChatProvider) log(format string, args ...interface{}) {
	if p.logger != nil {
		p.logger.Logf(format, args...)
	}
}

func (p *GoogleChatProvider) getInstanceID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.instanceIDFrom(p.config)
}

func (p *GoogleChatProvider) instanceIDFrom(config core.ProviderConfig) string {
	if id, _ := config["_instance_id"].(string); id != "" {
		return id
	}
	return "googlechat-1"
}

func (p *GoogleChatProvider) emit(evt core.ProviderEvent) {
	select {
	case p.eventChan <- evt:
	default:
	}
}

func (p *GoogleChatProvider) fetchSelf() error {
	info, err := p.getUserInfo()
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.selfID = info.Sub
	p.selfName = info.Name
	p.mu.Unlock()
	return nil
}

func (p *GoogleChatProvider) getSelfID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.selfID
}
