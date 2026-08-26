package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"Loom/pkg/core"
)

const clientPrefix = "/_matrix/client/v3"

type Provider struct {
	mu             sync.RWMutex
	config         core.ProviderConfig
	homeserver     string
	accessToken    string
	userID         string
	deviceID       string
	instanceID     string
	client         *http.Client
	events         chan core.ProviderEvent
	cancel         context.CancelFunc
	nextBatch      string
	eventRooms     map[string]string
	reactionEvents map[string]string
}

var _ core.Provider = (*Provider)(nil)

func NewProvider() *Provider {
	return &Provider{client: &http.Client{Timeout: 35 * time.Second}, events: make(chan core.ProviderEvent, 500), eventRooms: make(map[string]string), reactionEvents: make(map[string]string)}
}

func (p *Provider) Init(config core.ProviderConfig) error { return p.SetConfig(config) }
func (p *Provider) GetConfig() core.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

func (p *Provider) SetConfig(config core.ProviderConfig) error {
	homeserver, _ := config.GetString("homeserver")
	token, _ := config.GetString("access_token")
	userID, _ := config.GetString("user_id")
	password, _ := config.GetString("password")
	deviceID, _ := config.GetString("device_id")
	instanceID, _ := config.GetString("_instance_id")
	homeserver = strings.TrimRight(strings.TrimSpace(homeserver), "/")
	if homeserver != "" {
		u, err := url.Parse(homeserver)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("matrix: invalid homeserver URL")
		}
	}
	p.mu.Lock()
	p.config, p.homeserver, p.accessToken, p.userID, p.deviceID, p.instanceID = config, homeserver, strings.TrimSpace(token), strings.TrimSpace(userID), strings.TrimSpace(deviceID), instanceID
	p.mu.Unlock()
	if strings.TrimSpace(token) == "" && (strings.TrimSpace(userID) == "" || password == "") {
		return fmt.Errorf("matrix: user ID and password are required")
	}
	return nil
}

func (p *Provider) IsAuthenticated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	password, _ := p.config.GetString("password")
	return (p.homeserver != "" && p.accessToken != "") || (p.userID != "" && password != "")
}

func (p *Provider) Connect() error {
	if !p.IsAuthenticated() {
		return fmt.Errorf("matrix: user ID and password are required")
	}
	p.mu.RLock()
	hasToken := p.homeserver != "" && p.accessToken != ""
	p.mu.RUnlock()
	if !hasToken {
		if err := p.loginWithPassword(context.Background()); err != nil {
			return err
		}
	}
	var who struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}
	if err := p.do(context.Background(), http.MethodGet, "/account/whoami", nil, nil, &who); err != nil {
		return fmt.Errorf("matrix: authenticate: %w", err)
	}
	p.mu.Lock()
	if p.userID == "" {
		p.userID = who.UserID
	}
	if p.deviceID == "" {
		p.deviceID = who.DeviceID
	}
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.mu.Unlock()
	go p.syncLoop(ctx)
	return nil
}

func (p *Provider) loginWithPassword(ctx context.Context) error {
	p.mu.RLock()
	userID := p.userID
	password, _ := p.config.GetString("password")
	homeserver := p.homeserver
	p.mu.RUnlock()
	if homeserver == "" {
		var err error
		homeserver, err = p.discoverHomeserver(ctx, userID)
		if err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.homeserver = homeserver
	p.mu.Unlock()
	payload := map[string]any{"type": "m.login.password", "identifier": map[string]string{"type": "m.id.user", "user": userID}, "password": password, "initial_device_display_name": "Loom"}
	var response struct {
		UserID      string `json:"user_id"`
		AccessToken string `json:"access_token"`
		DeviceID    string `json:"device_id"`
	}
	if err := p.doUnauthenticated(ctx, http.MethodPost, homeserver+clientPrefix+"/login", payload, &response); err != nil {
		return fmt.Errorf("matrix: login: %w", err)
	}
	if response.AccessToken == "" {
		return fmt.Errorf("matrix: login returned no access token")
	}
	p.mu.Lock()
	p.accessToken, p.userID, p.deviceID = response.AccessToken, response.UserID, response.DeviceID
	p.config["homeserver"], p.config["access_token"], p.config["user_id"], p.config["device_id"] = homeserver, response.AccessToken, response.UserID, response.DeviceID
	delete(p.config, "password")
	p.mu.Unlock()
	return nil
}

func (p *Provider) discoverHomeserver(ctx context.Context, userID string) (string, error) {
	separator := strings.LastIndex(userID, ":")
	if !strings.HasPrefix(userID, "@") || separator < 2 || separator == len(userID)-1 {
		return "", fmt.Errorf("matrix: user ID must look like @user:server")
	}
	serverName := userID[separator+1:]
	var discovery struct {
		Homeserver struct {
			BaseURL string `json:"base_url"`
		} `json:"m.homeserver"`
	}
	err := p.doUnauthenticated(ctx, http.MethodGet, "https://"+serverName+"/.well-known/matrix/client", nil, &discovery)
	if err == nil && discovery.Homeserver.BaseURL != "" {
		base := strings.TrimRight(discovery.Homeserver.BaseURL, "/")
		parsed, parseErr := url.Parse(base)
		if parseErr == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" {
			return base, nil
		}
	}
	return "https://" + serverName, nil
}

func (p *Provider) doUnauthenticated(ctx context.Context, method, endpoint string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (p *Provider) Disconnect() error {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.mu.Unlock()
	return nil
}
func (p *Provider) Cleanup() error                                   { return p.Disconnect() }
func (p *Provider) StreamEvents() (<-chan core.ProviderEvent, error) { return p.events, nil }
func (p *Provider) CurrentUserID() string                            { p.mu.RLock(); defer p.mu.RUnlock(); return p.userID }

func (p *Provider) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	p.mu.RLock()
	base, token := p.homeserver, p.accessToken
	p.mu.RUnlock()
	u := base + clientPrefix + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (p *Provider) txnID() string { return fmt.Sprintf("loom-%d", time.Now().UnixNano()) }
func (p *Provider) roomPath(roomID string) string {
	return "/rooms/" + url.PathEscape(core.StripConvID(roomID))
}

func (p *Provider) GetCapabilities() core.Capabilities {
	return core.Capabilities{
		SupportsThreads: true, SupportsReactions: true, SupportsTypingIndicator: true,
		SupportsGroupManagement: true, SupportsAddGroupMembers: true, SupportsRemoveGroupMembers: true,
		SupportsRenameGroup: true, SupportsGroupDescription: true,
		SupportsLeaveGroup: true, SupportsDeleteMessage: true, SupportsEditMessage: true,
		SupportsReadReceipts: true, SupportsMuteConversation: true, NativeEmojiReactions: true,
		SupportsContactDirectory: true, SupportsDirectConversation: true, SupportsGroupConversation: true,
		SupportsGroupTitle: true, GroupConversationTypes: "group",
	}
}

func (p *Provider) emit(event core.ProviderEvent) {
	select {
	case p.events <- event:
	default:
	}
}
func (p *Provider) namespacedRoom(roomID string) string {
	p.mu.RLock()
	id := p.instanceID
	p.mu.RUnlock()
	return core.BuildConvID(id, roomID)
}
func (p *Provider) getInstanceID() string { p.mu.RLock(); defer p.mu.RUnlock(); return p.instanceID }

func (p *Provider) GetCustomEmojis() (map[string]string, error) { return nil, nil }
func (p *Provider) GetAuthQRCode() (string, error) {
	return "", fmt.Errorf("matrix: QR authentication is not supported")
}
