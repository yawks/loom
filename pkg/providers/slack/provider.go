// Package slack provides the Slack provider implementation.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/logging"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// SlackProvider implements the core.Provider interface for Slack.
type SlackProvider struct {
	config        core.ProviderConfig
	client        *slack.Client
	mu            sync.RWMutex
	logger        *logging.ProviderLogger
	userCache     map[string]*slack.User // Cache for user info to avoid repeated API calls
	userCacheMu   sync.RWMutex
	emojiCache    map[string]string // Cache for emoji names to URLs (e.g., "calendar" -> "https://...")
	emojiCacheMu  sync.RWMutex
	eventChan     chan core.ProviderEvent // Channel for emitting events
	stopChan      chan struct{}           // Channel to signal polling goroutine to stop
	statusCache   map[string]userStatus   // Cache of last known status for each user
	statusCacheMu sync.RWMutex            // Mutex for status cache
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
	fmt.Printf("cookieTransport.RoundTrip: Adding cookie header: %s\n", cookiePreview)
	req.Header.Add("Cookie", t.Cookie)
	// Some xoxc endpoints also check for d-s cookie, but usually d is the main auth one.
	return t.Transport.RoundTrip(req)
}

// Ensure interface compliance
var _ core.Provider = (*SlackProvider)(nil)

// NewSlackProvider creates a new instance of the SlackProvider.
func NewSlackProvider() *SlackProvider {
	return &SlackProvider{
		userCache:   make(map[string]*slack.User),
		emojiCache:  make(map[string]string),
		eventChan:   make(chan core.ProviderEvent, 100), // Buffered channel to avoid blocking
		stopChan:    make(chan struct{}),
		statusCache: make(map[string]userStatus),
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
		opts := []slack.Option{}

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

// GetQRCode returns the latest QR code string for authentication.
func (p *SlackProvider) GetQRCode() (string, error) {
	return "", nil
}

// IsAuthenticated returns true if the provider is already authenticated.
func (p *SlackProvider) IsAuthenticated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.client != nil
}

// Connect establishes the connection with the remote service.
func (p *SlackProvider) Connect() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

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

	// Load emojis after successful connection
	p.loadEmojis()

	// Initialize status cache with current statuses
	p.initializeStatusCache()

	// Start polling goroutine for status updates
	go p.pollStatusUpdates()

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

// Disconnect closes the connection and stops all background operations.
func (p *SlackProvider) Disconnect() error {
	// Signal polling goroutine to stop
	select {
	case p.stopChan <- struct{}{}:
	default:
	}
	close(p.stopChan)

	// Clear status cache
	p.statusCacheMu.Lock()
	p.statusCache = make(map[string]userStatus)
	p.statusCacheMu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.log("Slack: Disconnecting...\n")

	// Close logger
	if p.logger != nil {
		p.logger.Close()
		p.logger = nil
	}

	p.log("Slack: Disconnected\n")
	return nil
}
