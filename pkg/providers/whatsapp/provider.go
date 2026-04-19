package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/logging"
	"Loom/pkg/models"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

const maxMessagesPerConversation = 200

type WhatsAppProvider struct {
	client               *whatsmeow.Client
	container            *sqlstore.Container
	deviceStore          interface{} // Store the device store for later use (type is *store.Device)
	eventChan            chan core.ProviderEvent
	stopChan             chan struct{}
	config               core.ProviderConfig
	mu                   sync.RWMutex
	qrMu                 sync.RWMutex
	latestQRCode         string
	ctx                  context.Context
	cancel               context.CancelFunc
	knownGroups          map[string]string               // Map of group JID to group name (tracked from messages)
	groupParticipants    map[string]map[types.JID]string // Map of group JID to map of participant JID to phone number
	conversations        map[string]models.LinkedAccount // Cached conversations from history sync
	conversationMessages map[string][]models.Message     // Cached messages per conversation
	disconnected         bool                            // Track if already disconnected
	qrChan               <-chan whatsmeow.QRChannelItem  // QR code channel (must be obtained before Connect)
	qrChanSet            bool                            // Track if QR channel has been set
	qrListenerRunning    bool                            // Track if QR listener goroutine is running
	qrListenerMu         sync.Mutex                      // Mutex for qrListenerRunning flag
	avatarLoading        map[string]bool                 // Track which avatars are currently being loaded to avoid duplicates
	avatarLoadingMu      sync.Mutex                      // Mutex for avatarLoading map
	avatarFailures       map[string]bool                 // Track avatars that failed to load (401 errors) to avoid retrying
	avatarFailuresMu     sync.RWMutex                    // Mutex for avatarFailures map
	lastSyncTimestamp    *time.Time                      // Timestamp of last successful sync (loaded from DB)
	groupsCacheTimestamp *time.Time                      // Timestamp when groups were last fetched (to avoid repeated API calls)
	groupsCache          []models.LinkedAccount          // Cached groups from GetJoinedGroups
	lidToJIDMap          map[string]string               // Map of LID to standard JID for conversation resolution
	lidToJIDMu           sync.RWMutex                    // Mutex for LID to JID map
	logger               *logging.ProviderLogger         // Logger for this provider instance
}

func (w *WhatsAppProvider) log(format string, args ...interface{}) {
	if w.logger != nil {
		w.logger.Logf(format, args...)
	} else {
		// Fallback to fmt.Printf if logger not initialized
		fmt.Printf(format, args...)
	}
}

func (w *WhatsAppProvider) emitSyncStatus(status core.SyncStatusType, message string, progress int) {
	// Use recover to prevent panic if channel is closed
	defer func() {
		if r := recover(); r != nil {
			w.log("WhatsApp: PANIC in emitSyncStatus (channel may be closed): %v, status=%s, message=%s\n", r, status, message)
		}
	}()

	if w.eventChan == nil {
		w.log("WhatsApp: Warning - eventChan is nil, cannot emit sync status: %s\n", message)
		return
	}

	// Log the event being emitted for debugging
	w.log("WhatsApp: Emitting sync status: status=%s, message=%s, progress=%d\n", status, message, progress)

	// Use a timeout to ensure important events (like "completed") are not lost
	// For "completed" and "error" status, we use a longer timeout to ensure delivery
	timeout := 100 * time.Millisecond
	if status == core.SyncStatusCompleted || status == core.SyncStatusError {
		timeout = 1 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case w.eventChan <- core.SyncStatusEvent{
		InstanceID: w.getInstanceId(),
		Status:     status,
		Message:    message,
		Progress:   progress,
	}:
		// Event sent successfully
		w.log("WhatsApp: Sync status event sent successfully: %s\n", message)
	case <-ctx.Done():
		// Timeout - log but don't block
		w.log("WhatsApp: ERROR - sync status event not sent (channel may be full or timeout): status=%s, message=%s\n", status, message)
	}
}

func NewWhatsAppProvider() *WhatsAppProvider {
	ctx, cancel := context.WithCancel(context.Background())
	return &WhatsAppProvider{
		eventChan:            make(chan core.ProviderEvent, 200), // Increased buffer to prevent event loss
		stopChan:             make(chan struct{}),
		config:               make(core.ProviderConfig),
		ctx:                  ctx,
		cancel:               cancel,
		knownGroups:          make(map[string]string),
		groupParticipants:    make(map[string]map[types.JID]string),
		conversations:        make(map[string]models.LinkedAccount),
		conversationMessages: make(map[string][]models.Message),
		avatarLoading:        make(map[string]bool),
		avatarFailures:       make(map[string]bool),
		lidToJIDMap:          make(map[string]string),
	}
}

func (w *WhatsAppProvider) Init(config core.ProviderConfig) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if config != nil {
		w.config = config
	} else {
		w.config = make(core.ProviderConfig)
	}

	// Get instanceID for logger initialization
	instanceID, _ := w.config["_instance_id"].(string)
	if instanceID == "" {
		instanceID = "whatsapp-1" // Default instance ID
	}

	// Initialize logger
	logger, err := logging.GetLogger("whatsapp", instanceID)
	if err != nil {
		// Log error but continue - fallback to fmt.Printf
		fmt.Printf("WhatsAppProvider.Init: WARNING - failed to initialize logger: %v\n", err)
	} else {
		w.logger = logger
	}

	w.log("WhatsAppProvider.Init: called with config: %v\n", config != nil)
	w.log("WhatsAppProvider.Init: config set, proceeding with initialization\n")

	// Automatically determine database path (never ask user for this)
	w.log("WhatsAppProvider.Init: Getting config directory...\n")
	configDir, err := os.UserConfigDir()
	if err != nil {
		w.log("WhatsAppProvider.Init: ERROR - failed to get config directory: %v\n", err)
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	w.log("WhatsAppProvider.Init: Config directory: %s\n", configDir)

	// Use instanceID from config to create isolated storage for each instance
	var dbPath string
	if instanceID != "" {
		// Use instanceID in path: configDir/Loom/whatsapp-1/whatsapp.db
		dbPath = filepath.Join(configDir, "Loom", instanceID, "whatsapp.db")
		w.log("WhatsAppProvider.Init: Database path (with instanceID): %s\n", dbPath)
	} else {
		// Fallback to old path for backward compatibility
		dbPath = filepath.Join(configDir, "Loom", "whatsapp", "whatsapp.db")
		w.log("WhatsAppProvider.Init: Database path (legacy): %s\n", dbPath)
	}

	// Ensure directory exists
	w.log("WhatsAppProvider.Init: Creating directory...\n")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		w.log("WhatsAppProvider.Init: ERROR - failed to create directory: %v\n", err)
		return fmt.Errorf("failed to create directory: %w", err)
	}
	w.log("WhatsAppProvider.Init: Directory created successfully\n")

	// Create database connection string
	dbConnStr := fmt.Sprintf("file:%s?_foreign_keys=on", dbPath)
	w.log("WhatsAppProvider.Init: Database connection string created\n")

	// Initialize database logger
	dbLog := waLog.Stdout("Database", "DEBUG", false)
	w.log("WhatsAppProvider.Init: Database logger initialized\n")

	// Create container
	w.log("WhatsAppProvider.Init: Creating store container...\n")
	container, err := sqlstore.New(w.ctx, "sqlite3", dbConnStr, dbLog)
	if err != nil {
		w.log("WhatsAppProvider.Init: ERROR - failed to create store container: %v\n", err)
		return fmt.Errorf("failed to create store container: %w", err)
	}
	w.container = container
	w.log("WhatsAppProvider.Init: Store container created successfully\n")

	// Get device store
	w.log("WhatsAppProvider.Init: Getting device store...\n")
	deviceStore, err := container.GetFirstDevice(w.ctx)
	if err != nil {
		w.log("WhatsAppProvider.Init: ERROR - failed to get device store: %v\n", err)
		return fmt.Errorf("failed to get device store: %w", err)
	}
	w.deviceStore = deviceStore
	w.log("WhatsAppProvider.Init: Device store retrieved successfully\n")

	// Initialize client logger
	clientLog := waLog.Stdout("Client", "DEBUG", false)
	w.log("WhatsAppProvider.Init: Client logger initialized\n")

	// Set custom OS info for WhatsApp registration
	// Using macOS with a recent version is safer to avoid err-client-outdated issues
	store.SetOSInfo("macOS", [3]uint32{15, 0, 0})
	w.log("WhatsAppProvider.Init: OS info set to macOS 15.0\n")

	// Enable call log history in DeviceProps
	// This must be done before creating the client
	w.log("WhatsAppProvider.Init: Enabling call log history support...\n")
	// Enable call log history support
	// Enable call log history support via reflection
	// We use reflection because DeviceProps might be unexported or we want to be safe
	deviceStoreValue := reflect.ValueOf(deviceStore)
	if deviceStoreValue.Kind() == reflect.Ptr {
		deviceStoreValue = deviceStoreValue.Elem()
	}

	devicePropsField := deviceStoreValue.FieldByName("DeviceProps")
	if devicePropsField.IsValid() {
		// Get DeviceProps value
		devicePropsPtr := devicePropsField.Interface()
		if devicePropsPtr != nil {
			devicePropsValue := reflect.ValueOf(devicePropsPtr).Elem()
			historySyncConfigField := devicePropsValue.FieldByName("HistorySyncConfig")

			if historySyncConfigField.IsValid() {
				// Initialize if nil
				if historySyncConfigField.IsNil() && historySyncConfigField.CanSet() {
					newConfig := reflect.New(historySyncConfigField.Type().Elem())
					historySyncConfigField.Set(newConfig)
				}

				if !historySyncConfigField.IsNil() {
					configValue := historySyncConfigField.Elem()
					supportCallLogHistoryField := configValue.FieldByName("SupportCallLogHistory")

					if supportCallLogHistoryField.IsValid() && supportCallLogHistoryField.CanSet() {
						supportCallLogHistoryField.Set(reflect.ValueOf(proto.Bool(true)))
						w.log("WhatsAppProvider.Init: Call log history support enabled successfully\n")
					} else {
						w.log("WhatsAppProvider.Init: SupportCallLogHistory field not found or unsettable\n")
					}
				}
			} else {
				w.log("WhatsAppProvider.Init: HistorySyncConfig field not found\n")
			}
		} else {
			w.log("WhatsAppProvider.Init: DeviceProps is nil\n")
		}
	} else {
		// Log but don't error out - maybe field is missing or unexported
		w.log("WhatsAppProvider.Init: DeviceProps field not found in deviceStore\n")
	}

	// Create client
	w.log("WhatsAppProvider.Init: Creating WhatsApp client...\n")
	w.client = whatsmeow.NewClient(deviceStore, clientLog)
	w.log("WhatsAppProvider.Init: WhatsApp client created successfully\n")

	// Load cached messages from database on startup
	// Note: w.mu is already locked, so we call the internal version that doesn't lock
	w.log("WhatsAppProvider.Init: Loading messages from database...\n")
	w.loadMessagesFromDatabaseLocked()
	w.log("WhatsAppProvider.Init: Messages loaded from database\n")

	// Load avatar failures cache
	w.log("WhatsAppProvider.Init: Loading avatar failures cache...\n")
	w.loadAvatarFailures()
	w.log("WhatsAppProvider.Init: Avatar failures cache loaded\n")

	// Load last sync timestamp from database
	// Note: w.mu is already locked, so we call the internal version that doesn't lock
	w.log("WhatsAppProvider.Init: Loading last sync timestamp...\n")
	w.loadLastSyncTimestampLocked()
	w.log("WhatsAppProvider.Init: Last sync timestamp loaded\n")

	// Add event handler
	w.log("WhatsAppProvider.Init: Adding event handler...\n")
	w.client.AddEventHandler(w.eventHandler)
	w.log("WhatsAppProvider.Init: Event handler added successfully\n")
	w.log("WhatsAppProvider.Init: Initialization completed successfully\n")

	return nil
}

func (w *WhatsAppProvider) GetConfig() core.ProviderConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.config
}

func (w *WhatsAppProvider) SetConfig(config core.ProviderConfig) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config = config
	return nil
}

func (w *WhatsAppProvider) GetQRCode() (string, error) {
	w.qrMu.RLock()
	defer w.qrMu.RUnlock()
	w.log("WhatsApp.GetQRCode: Returning QR code (length: %d, empty: %v)\n", len(w.latestQRCode), w.latestQRCode == "")
	if w.latestQRCode == "" {
		w.log("WhatsApp.GetQRCode: WARNING - QR code is empty. IsAuthenticated=%v, client.Store.ID=%v\n",
			w.IsAuthenticated(), w.client != nil && w.client.Store != nil && w.client.Store.ID != nil)
	}
	return w.latestQRCode, nil
}

func (w *WhatsAppProvider) IsAuthenticated() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check if client and store are initialized and have an ID
	// This is the most reliable way to check if we are currently authenticated
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
		return true
	}

	// Check the device store from the container if client is not connected
	if w.container != nil {
		// Try to get the first device from the container
		// Use a short timeout to avoid blocking
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		deviceStore, err := w.container.GetFirstDevice(ctx)
		if err == nil && deviceStore != nil {
			// Check if device has an ID (was previously authenticated)
			if deviceStore.ID != nil {
				return true
			}
		}
	}

	return false
}

func (w *WhatsAppProvider) Connect() error {
	w.mu.Lock()

	if w.client == nil {
		w.mu.Unlock()
		return fmt.Errorf("client not initialized, call Init first")
	}

	// Reset disconnected flag and recreate context if needed
	if w.disconnected {
		w.log("WhatsApp.Connect: Re-initializing context after disconnect\n")
		w.disconnected = false
		w.ctx, w.cancel = context.WithCancel(context.Background())
	}

	// Check if client is already connected
	// If connected but not authenticated (no Store.ID), disconnect first to allow QR code flow
	if w.client.IsConnected() {
		if w.client.Store.ID == nil {
			// Connected but not authenticated - disconnect to allow QR code flow
			w.log("WhatsApp: Client is connected but not authenticated, disconnecting to allow QR code flow...\n")
			w.client.Disconnect()
			// Reset QR channel state
			w.qrChanSet = false
			w.qrChan = nil
			w.mu.Unlock()
			time.Sleep(1 * time.Second) // Give it more time to disconnect
			w.mu.Lock()
		} else {
			// Already connected and authenticated
			w.log("WhatsApp: Already connected and logged in as %s\n", w.client.Store.ID)
			w.mu.Unlock()
			return nil
		}
	}

	// If not logged in, get QR code channel BEFORE connecting
	// According to whatsmeow docs, GetQRChannel MUST be called before Connect()
	if w.client.Store.ID == nil {
		w.log("WhatsApp.Connect: Client not authenticated (Store.ID is nil), will get QR channel\n")

		// Clear latest QR code to avoid showing stale one
		w.qrMu.Lock()
		w.latestQRCode = ""
		w.qrMu.Unlock()

		// Ensure QR channel is obtained synchronously before Connect
		if !w.qrChanSet {
			w.log("WhatsApp.Connect: Obtaining initial QR channel...\n")
			qrChan, err := w.client.GetQRChannel(w.ctx)
			if err != nil {
				w.log("WhatsApp.Connect: ERROR - Failed to get QR channel: %v\n", err)
				w.mu.Unlock()
				return fmt.Errorf("failed to get QR channel: %w", err)
			}
			w.qrChan = qrChan
			w.qrChanSet = true
			w.log("WhatsApp.Connect: Initial QR channel obtained successfully\n")
		}

		// Start goroutine to handle QR code updates if not already running
		w.qrListenerMu.Lock()
		if !w.qrListenerRunning {
			w.qrListenerRunning = true
			go func() {
				defer func() {
					w.qrListenerMu.Lock()
					w.qrListenerRunning = false
					w.qrListenerMu.Unlock()

					w.mu.Lock()
					w.qrChanSet = false
					w.qrChan = nil
					w.mu.Unlock()
				}()

				qrCodeCount := 0
				for {
					w.mu.Lock()
					// Exit if authenticated or client is nil
					if w.client == nil || (w.client.Store != nil && w.client.Store.ID != nil) {
						w.mu.Unlock()
						return
					}

					if !w.qrChanSet {
						w.log("WhatsApp: Refreshing QR channel...\n")
						// Ensure we are disconnected before getting a new channel to restart the flow
						if w.client.IsConnected() {
							w.log("WhatsApp: Disconnecting before refreshing QR channel\n")
							w.client.Disconnect()
							w.mu.Unlock()
							time.Sleep(1 * time.Second)
							w.mu.Lock()
						}

						qrChan, err := w.client.GetQRChannel(w.ctx)
						if err != nil {
							w.log("WhatsApp: ERROR - Failed to get QR channel: %v\n", err)
							w.mu.Unlock()
							time.Sleep(5 * time.Second)
							continue
						}
						w.qrChan = qrChan
						w.qrChanSet = true
						w.log("WhatsApp: QR channel refreshed successfully, re-connecting...\n")

						// Re-connect to start receiving codes on the new channel
						if err := w.client.Connect(); err != nil {
							w.log("WhatsApp: WARNING - Failed to re-connect in listener: %v\n", err)
						}
					}
					qrChan := w.qrChan
					w.mu.Unlock()

					select {
					case <-w.ctx.Done():
						// Provider was disconnected, exit goroutine
						w.log("WhatsApp: QR code handler goroutine exiting - context cancelled\n")
						return
					case evt, ok := <-qrChan:
						if !ok {
							// Channel closed, reset to get a new one in next iteration
							w.log("WhatsApp: QR code channel closed, will retry...\n")
							w.mu.Lock()
							w.qrChanSet = false
							w.qrChan = nil
							w.mu.Unlock()
							continue
						}

						w.log("WhatsApp: Received QR channel event: %s\n", evt.Event)

						if evt.Event == "code" {
							w.qrMu.Lock()
							// Only log if this is a new QR code (different from previous)
							isNewQR := w.latestQRCode != evt.Code
							w.latestQRCode = evt.Code
							w.qrMu.Unlock()

							if isNewQR {
								qrCodeCount++
								w.log("WhatsApp: QR code updated (update #%d, code length: %d)\n", qrCodeCount, len(evt.Code))
							}
						} else if evt.Event == "success" {
							w.log("WhatsApp: ✅ QR code scanned successfully! Login in progress...\n")
							w.qrMu.Lock()
							w.latestQRCode = ""
							w.qrMu.Unlock()
							// Emit fetching_contacts status immediately after QR scan to close the modal
							w.emitSyncStatus(core.SyncStatusFetchingContacts, "QR code scanned, connecting...", -1)
							return // Exit goroutine on success
						} else if evt.Event == "timeout" {
							w.log("WhatsApp: ⏱️ QR code expired. Refreshing for a new one...\n")
							w.qrMu.Lock()
							w.latestQRCode = ""
							w.qrMu.Unlock()
							qrCodeCount = 0 // Reset counter for new QR code session
							// Reset QR channel state to allow getting a new one in next iteration
							w.mu.Lock()
							w.qrChanSet = false
							w.qrChan = nil
							w.mu.Unlock()
						}
					}
				}
			}()
		}
		w.qrListenerMu.Unlock()
	} else {
		w.log("WhatsApp: Already logged in as %s, no QR code needed\n", w.client.Store.ID)
		w.log("WhatsApp.Connect: WARNING - Client is authenticated, QR code will not be generated\n")
	}

	// Connect (this must be called after getting the QR channel or starting the listener)
	// We MUST NOT hold the lock during Connect() because it might trigger events
	// that need the lock (deadlock risk).
	client := w.client
	w.mu.Unlock()

	w.log("WhatsApp: Attempting to connect client...\n")
	err := client.Connect()
	if err != nil {
		if err.Error() == "websocket is already connected" {
			w.log("WhatsApp: Client is already connected, proceeding\n")
		} else {
			return fmt.Errorf("failed to connect: %w", err)
		}
	}

	w.log("WhatsApp: Client connected/connecting, waiting for QR codes...\n")
	w.log("WhatsApp: IMPORTANT - Make sure to scan the QR code using WhatsApp > Settings > Linked Devices on your phone\n")

	return nil
}

func (w *WhatsAppProvider) Disconnect() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.disconnected {
		// Already disconnected, skip
		return nil
	}

	w.log("WhatsApp: Disconnecting...\n")

	if w.client != nil {
		w.client.Disconnect()
	}

	if w.cancel != nil {
		w.cancel()
	}

	// Close channels safely
	select {
	case <-w.stopChan:
		// Already closed
	default:
		close(w.stopChan)
	}

	select {
	case <-w.eventChan:
		// Already closed
	default:
		close(w.eventChan)
	}

	w.disconnected = true
	w.qrChanSet = false
	w.qrChan = nil

	// Close logger
	if w.logger != nil {
		w.logger.Close()
		w.logger = nil
	}

	w.log("WhatsApp: Disconnected\n")
	return nil
}

func (w *WhatsAppProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	return w.eventChan, nil
}

func (w *WhatsAppProvider) Cleanup() error {
	w.log("WhatsApp: Cleaning up provider data...\n")

	// 1. Disconnect first to stop everything
	_ = w.Disconnect()

	// 2. Close database connection if open
	w.mu.Lock()
	if w.container != nil {
		if err := w.container.Close(); err != nil {
			w.log("WhatsApp: Warning - failed to close database container: %v\n", err)
		}
		w.container = nil
	}

	// 3. Determine data directory
	instanceID := ""
	if w.config != nil {
		if id, ok := w.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	w.mu.Unlock()

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	var dataDir string
	if instanceID != "" {
		dataDir = filepath.Join(configDir, "Loom", instanceID)
	} else {
		dataDir = filepath.Join(configDir, "Loom", "whatsapp")
	}

	// 4. Remove directory
	w.log("WhatsApp: Removing data directory: %s\n", dataDir)
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("failed to remove data directory: %w", err)
	}

	w.log("WhatsApp: Cleanup completed successfully\n")
	return nil
}

func (w *WhatsAppProvider) GetCapabilities() core.Capabilities {
	return core.Capabilities{
		SupportsThreads:          false,
		SupportsReactions:        true,
		SupportsCustomEmojis:     false,
		SupportsTypingIndicator:  true,
		SupportsGroupManagement:  true,
		SupportsDeleteMessage:    true,
		SupportsEditMessage:      true,
		SupportsReadReceipts:     true,
		SupportsPinConversation:  true,
		SupportsMuteConversation: true,
		SupportsQRCodeAuth:       true,
	}
}

func (w *WhatsAppProvider) GetCustomEmojis() (map[string]string, error) {
	return nil, nil // WhatsApp doesn't support custom emojis
}

func (w *WhatsAppProvider) GetAuthQRCode() (string, error) {
	return w.GetQRCode()
}

func (w *WhatsAppProvider) RefreshContact(contactID string) error {
	// Implementation in avatars.go
	return w.refreshContactMetadata(contactID)
}

func (w *WhatsAppProvider) getInstanceId() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	id, _ := w.config["_instance_id"].(string)
	return id
}
