package whatsapp

import (
	"Loom/pkg/core"
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
	avatarLoading        map[string]bool                 // Track which avatars are currently being loaded to avoid duplicates
	avatarLoadingMu      sync.Mutex                      // Mutex for avatarLoading map
	avatarFailures       map[string]bool                 // Track avatars that failed to load (401 errors) to avoid retrying
	avatarFailuresMu     sync.RWMutex                    // Mutex for avatarFailures map
	lastSyncTimestamp    *time.Time                      // Timestamp of last successful sync (loaded from DB)
	groupsCacheTimestamp *time.Time                      // Timestamp when groups were last fetched (to avoid repeated API calls)
	groupsCache          []models.LinkedAccount          // Cached groups from GetJoinedGroups
	lidToJIDMap          map[string]string               // Map of LID to standard JID for conversation resolution
	lidToJIDMu           sync.RWMutex                    // Mutex for LID to JID map
}

func (w *WhatsAppProvider) emitSyncStatus(status core.SyncStatusType, message string, progress int) {
	// Use recover to prevent panic if channel is closed
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("WhatsApp: PANIC in emitSyncStatus (channel may be closed): %v, status=%s, message=%s\n", r, status, message)
		}
	}()

	if w.eventChan == nil {
		fmt.Printf("WhatsApp: Warning - eventChan is nil, cannot emit sync status: %s\n", message)
		return
	}

	// Log the event being emitted for debugging
	fmt.Printf("WhatsApp: Emitting sync status: status=%s, message=%s, progress=%d\n", status, message, progress)

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
		Status:   status,
		Message:  message,
		Progress: progress,
	}:
		// Event sent successfully
		fmt.Printf("WhatsApp: Sync status event sent successfully: %s\n", message)
	case <-ctx.Done():
		// Timeout - log but don't block
		fmt.Printf("WhatsApp: ERROR - sync status event not sent (channel may be full or timeout): status=%s, message=%s\n", status, message)
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
	fmt.Printf("WhatsAppProvider.Init: called with config: %v\n", config != nil)
	w.mu.Lock()
	defer w.mu.Unlock()

	if config != nil {
		w.config = config
	} else {
		w.config = make(core.ProviderConfig)
	}
	fmt.Printf("WhatsAppProvider.Init: config set, proceeding with initialization\n")

	// Automatically determine database path (never ask user for this)
	fmt.Printf("WhatsAppProvider.Init: Getting config directory...\n")
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("WhatsAppProvider.Init: ERROR - failed to get config directory: %v\n", err)
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	fmt.Printf("WhatsAppProvider.Init: Config directory: %s\n", configDir)

	// Use instanceID from config to create isolated storage for each instance
	var dbPath string
	instanceID, _ := w.config["_instance_id"].(string)
	if instanceID != "" {
		// Use instanceID in path: configDir/Loom/whatsapp-1/whatsapp.db
		dbPath = filepath.Join(configDir, "Loom", instanceID, "whatsapp.db")
		fmt.Printf("WhatsAppProvider.Init: Database path (with instanceID): %s\n", dbPath)
	} else {
		// Fallback to old path for backward compatibility
		dbPath = filepath.Join(configDir, "Loom", "whatsapp", "whatsapp.db")
		fmt.Printf("WhatsAppProvider.Init: Database path (legacy): %s\n", dbPath)
	}

	// Ensure directory exists
	fmt.Printf("WhatsAppProvider.Init: Creating directory...\n")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		fmt.Printf("WhatsAppProvider.Init: ERROR - failed to create directory: %v\n", err)
		return fmt.Errorf("failed to create directory: %w", err)
	}
	fmt.Printf("WhatsAppProvider.Init: Directory created successfully\n")

	// Create database connection string
	dbConnStr := fmt.Sprintf("file:%s?_foreign_keys=on", dbPath)
	fmt.Printf("WhatsAppProvider.Init: Database connection string created\n")

	// Initialize database logger
	dbLog := waLog.Stdout("Database", "DEBUG", false)
	fmt.Printf("WhatsAppProvider.Init: Database logger initialized\n")

	// Create container
	fmt.Printf("WhatsAppProvider.Init: Creating store container...\n")
	container, err := sqlstore.New(w.ctx, "sqlite3", dbConnStr, dbLog)
	if err != nil {
		fmt.Printf("WhatsAppProvider.Init: ERROR - failed to create store container: %v\n", err)
		return fmt.Errorf("failed to create store container: %w", err)
	}
	w.container = container
	fmt.Printf("WhatsAppProvider.Init: Store container created successfully\n")

	// Get device store
	fmt.Printf("WhatsAppProvider.Init: Getting device store...\n")
	deviceStore, err := container.GetFirstDevice(w.ctx)
	if err != nil {
		fmt.Printf("WhatsAppProvider.Init: ERROR - failed to get device store: %v\n", err)
		return fmt.Errorf("failed to get device store: %w", err)
	}
	w.deviceStore = deviceStore
	fmt.Printf("WhatsAppProvider.Init: Device store retrieved successfully\n")

	// Initialize client logger
	clientLog := waLog.Stdout("Client", "DEBUG", false)
	fmt.Printf("WhatsAppProvider.Init: Client logger initialized\n")

	// Set custom OS info for WhatsApp registration
	// This changes the connector name from "whatsmeow" to "Loom"
	store.SetOSInfo("Loom", [3]uint32{1, 0, 0})
	fmt.Printf("WhatsAppProvider.Init: Custom OS info set to 'Loom'\n")

	// Enable call log history in DeviceProps
	// This must be done before creating the client
	fmt.Printf("WhatsAppProvider.Init: Enabling call log history support...\n")
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
						fmt.Printf("WhatsAppProvider.Init: Call log history support enabled successfully\n")
					} else {
						fmt.Printf("WhatsAppProvider.Init: SupportCallLogHistory field not found or unsettable\n")
					}
				}
			} else {
				fmt.Printf("WhatsAppProvider.Init: HistorySyncConfig field not found\n")
			}
		} else {
			fmt.Printf("WhatsAppProvider.Init: DeviceProps is nil\n")
		}
	} else {
		// Log but don't error out - maybe field is missing or unexported
		fmt.Printf("WhatsAppProvider.Init: DeviceProps field not found in deviceStore\n")
	}

	// Create client
	fmt.Printf("WhatsAppProvider.Init: Creating WhatsApp client...\n")
	w.client = whatsmeow.NewClient(deviceStore, clientLog)
	fmt.Printf("WhatsAppProvider.Init: WhatsApp client created successfully\n")

	// Load cached messages from database on startup
	// Note: w.mu is already locked, so we call the internal version that doesn't lock
	fmt.Printf("WhatsAppProvider.Init: Loading messages from database...\n")
	w.loadMessagesFromDatabaseLocked()
	fmt.Printf("WhatsAppProvider.Init: Messages loaded from database\n")

	// Load avatar failures cache
	fmt.Printf("WhatsAppProvider.Init: Loading avatar failures cache...\n")
	w.loadAvatarFailures()
	fmt.Printf("WhatsAppProvider.Init: Avatar failures cache loaded\n")

	// Load last sync timestamp from database
	// Note: w.mu is already locked, so we call the internal version that doesn't lock
	fmt.Printf("WhatsAppProvider.Init: Loading last sync timestamp...\n")
	w.loadLastSyncTimestampLocked()
	fmt.Printf("WhatsAppProvider.Init: Last sync timestamp loaded\n")

	// Add event handler
	fmt.Printf("WhatsAppProvider.Init: Adding event handler...\n")
	w.client.AddEventHandler(w.eventHandler)
	fmt.Printf("WhatsAppProvider.Init: Event handler added successfully\n")
	fmt.Printf("WhatsAppProvider.Init: Initialization completed successfully\n")

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
	fmt.Printf("WhatsApp.GetQRCode: Returning QR code (length: %d, empty: %v)\n", len(w.latestQRCode), w.latestQRCode == "")
	if w.latestQRCode == "" {
		fmt.Printf("WhatsApp.GetQRCode: WARNING - QR code is empty. IsAuthenticated=%v, client.Store.ID=%v\n",
			w.IsAuthenticated(), w.client != nil && w.client.Store != nil && w.client.Store.ID != nil)
	}
	return w.latestQRCode, nil
}

func (w *WhatsAppProvider) IsAuthenticated() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check if we have a device store - if it exists, we can authenticate
	// The device store existing means we have credentials stored
	if w.deviceStore != nil {
		// Try to get the device store's ID via reflection or direct check
		// The device store is of type *store.Device
		// First check via the client if available and connected
		if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
			return true
		}
		// If client is not yet initialized or not connected, check the container
		if w.container != nil {
			// Try to get the first device from the container
			ctx := context.Background()
			deviceStore, err := w.container.GetFirstDevice(ctx)
			if err == nil && deviceStore != nil {
				// Check if device has an ID (was previously authenticated)
				// Use reflection to check ID field
				deviceValue := reflect.ValueOf(deviceStore).Elem()
				idField := deviceValue.FieldByName("ID")
				if idField.IsValid() && !idField.IsNil() {
					return true
				}
			}
		}
		// If we have a deviceStore but can't check ID directly, assume authenticated
		// This handles the case where deviceStore exists but client isn't connected yet
		return true
	}

	// Fallback: check if client and store are initialized and have an ID
	if w.client != nil && w.client.Store != nil {
		// If Store.ID is set, we're authenticated
		return w.client.Store.ID != nil
	}

	return false
}

func (w *WhatsAppProvider) Connect() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.client == nil {
		return fmt.Errorf("client not initialized, call Init first")
	}

	// Check if client is already connected
	// If connected but not authenticated (no Store.ID), disconnect first to allow QR code flow
	if w.client.IsConnected() {
		if w.client.Store.ID == nil {
			// Connected but not authenticated - disconnect to allow QR code flow
			fmt.Println("WhatsApp: Client is connected but not authenticated, disconnecting to allow QR code flow...")
			w.client.Disconnect()
			// Reset QR channel state
			w.qrChanSet = false
			w.qrChan = nil
		} else {
			// Already connected and authenticated
			fmt.Printf("WhatsApp: Already connected and logged in as %s\n", w.client.Store.ID)
			return nil
		}
	}

	// If not logged in, get QR code channel BEFORE connecting
	// According to whatsmeow docs, GetQRChannel MUST be called before Connect()
	if w.client.Store.ID == nil {
		fmt.Printf("WhatsApp.Connect: Client not authenticated (Store.ID is nil), will get QR channel\n")
		// Always get a fresh QR channel if not already set
		if !w.qrChanSet {
			fmt.Printf("WhatsApp.Connect: Getting QR channel...\n")
			qrChan, err := w.client.GetQRChannel(w.ctx)
			if err != nil {
				fmt.Printf("WhatsApp.Connect: ERROR - Failed to get QR channel: %v\n", err)
				return fmt.Errorf("failed to get QR channel: %w", err)
			}
			w.qrChan = qrChan
			w.qrChanSet = true
			fmt.Println("WhatsApp: QR channel obtained successfully")
		} else {
			fmt.Printf("WhatsApp.Connect: QR channel already set, reusing existing channel\n")
		}

		fmt.Println("WhatsApp: Starting to listen for QR events...")

		// Start goroutine to handle QR code updates
		go func() {
			qrCodeCount := 0
			for {
				select {
				case <-w.ctx.Done():
					// Provider was disconnected, exit goroutine
					fmt.Printf("WhatsApp: QR code handler goroutine exiting - context cancelled\n")
					return
				case evt, ok := <-w.qrChan:
					if !ok {
						// Channel closed, exit goroutine
						fmt.Printf("WhatsApp: QR code channel closed, exiting handler goroutine\n")
						return
					}

					if evt.Event == "code" {
						w.qrMu.Lock()
						// Only log if this is a new QR code (different from previous)
						isNewQR := w.latestQRCode != evt.Code
						w.latestQRCode = evt.Code
						w.qrMu.Unlock()

						if isNewQR {
							qrCodeCount++
							// Only log the first QR code and every 10th update to reduce log spam
							if qrCodeCount == 1 || qrCodeCount%10 == 0 {
								fmt.Printf("WhatsApp: QR code updated (update #%d, expires in ~30 seconds)\n", qrCodeCount)
							}
						}
					} else if evt.Event == "success" {
						fmt.Println("WhatsApp: ✅ QR code scanned successfully! Login in progress...")
						w.qrMu.Lock()
						w.latestQRCode = ""
						w.qrMu.Unlock()
						// Don't return here, wait for the connection to complete
						// The Connected event will be received via eventHandler
					} else if evt.Event == "timeout" {
						fmt.Println("WhatsApp: ⏱️ QR code expired. Please reconnect to get a new one.")
						w.qrMu.Lock()
						w.latestQRCode = ""
						w.qrMu.Unlock()
						qrCodeCount = 0 // Reset counter for new QR code session
						// Reset QR channel state to allow reconnection
						w.mu.Lock()
						w.qrChanSet = false
						w.qrChan = nil
						w.mu.Unlock()
					} else {
						// Only log unknown events, not every code update
						fmt.Printf("WhatsApp: QR channel event: %s\n", evt.Event)
					}
				}
			}
		}()
	} else {
		fmt.Printf("WhatsApp: Already logged in as %s, no QR code needed\n", w.client.Store.ID)
		fmt.Printf("WhatsApp.Connect: WARNING - Client is authenticated, QR code will not be generated\n")
	}

	// Connect (this must be called after getting the QR channel)
	// Note: GetQRChannel must be called before Connect() according to whatsmeow docs
	fmt.Println("WhatsApp: Attempting to connect client...")
	if err := w.client.Connect(); err != nil {
		// Check if error is because already connected
		if err.Error() == "websocket is already connected" {
			fmt.Println("WhatsApp: Client is already connected, skipping Connect()")
			return nil
		}
		return fmt.Errorf("failed to connect: %w", err)
	}

	fmt.Println("WhatsApp: Client connected, waiting for QR scan...")
	fmt.Println("WhatsApp: IMPORTANT - Make sure to scan the QR code using WhatsApp > Settings > Linked Devices on your phone")

	return nil
}

func (w *WhatsAppProvider) Disconnect() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.disconnected {
		// Already disconnected, skip
		return nil
	}

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
	return nil
}

func (w *WhatsAppProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	return w.eventChan, nil
}
