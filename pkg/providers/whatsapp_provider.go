// Package providers contains implementations of the Provider interface.
package providers

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "github.com/mattn/go-sqlite3" // SQLite driver for whatsmeow store
)

const maxMessagesPerConversation = 200

// WhatsAppProvider implements the Provider interface for WhatsApp using whatsmeow.
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
}

func (w *WhatsAppProvider) emitSyncStatus(status core.SyncStatusType, message string, progress int) {
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

func (w *WhatsAppProvider) lookupDisplayName(jid types.JID, fallback string) string {
	if fallback != "" && fallback != jid.String() {
		return fallback
	}

	if jid.Server == types.GroupServer {
		w.mu.RLock()
		if name, ok := w.knownGroups[jid.String()]; ok && name != "" {
			w.mu.RUnlock()
			return name
		}
		w.mu.RUnlock()
	}

	if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil && !jid.IsEmpty() {
		if contact, err := w.client.Store.Contacts.GetContact(w.ctx, jid); err == nil && contact.Found {
			switch {
			case contact.FullName != "":
				return contact.FullName
			case contact.FirstName != "":
				return contact.FirstName
			case contact.PushName != "":
				return contact.PushName
			case contact.BusinessName != "":
				return contact.BusinessName
			}
		}
	}

	if fallback != "" {
		return fallback
	}

	if !jid.IsEmpty() {
		return jid.String()
	}

	return ""
}

func (w *WhatsAppProvider) lookupSenderName(jid types.JID) string {
	return w.lookupDisplayName(jid, "")
}

// lookupSenderNameInGroup looks up the sender name in a group context.
// For LID participants, it tries to get the phone number from group info and then looks up the contact.
func (w *WhatsAppProvider) lookupSenderNameInGroup(senderJID types.JID, groupJID types.JID) string {
	// Check if it's a LID (Linked Device ID) - LIDs have server "lid"
	isLID := senderJID.Server == "lid"

	// If it's not a LID, use normal lookup
	if !isLID {
		return w.lookupSenderName(senderJID)
	}

	// For LID participants, try to get phone number from group info
	w.mu.RLock()
	groupParticipants, hasGroup := w.groupParticipants[groupJID.String()]
	w.mu.RUnlock()

	if hasGroup {
		// Check if we have a phone number mapping for this LID
		if phoneNumber, ok := groupParticipants[senderJID]; ok {
			// Parse the phone number JID and lookup contact
			if phoneJID, err := types.ParseJID(phoneNumber); err == nil {
				return w.lookupSenderName(phoneJID)
			}
		}
	}

	// Fallback to normal lookup
	return w.lookupSenderName(senderJID)
}

// cacheGroupParticipants caches the participants of a group with their phone numbers.
// This allows us to map LID participants to their phone numbers for contact lookup.
// This function should NOT be called while holding w.mu lock to avoid deadlocks.
func (w *WhatsAppProvider) cacheGroupParticipants(groupJID types.JID) {
	if w.client == nil {
		return
	}

	// Get group info to obtain participants with phone numbers
	// This is a potentially blocking call, so we don't hold any locks
	groupInfo, err := w.client.GetGroupInfo(w.ctx, groupJID)
	if err != nil || groupInfo == nil {
		return
	}

	// Create mapping of participant JID to phone number
	participants := make(map[types.JID]string)
	for _, participant := range groupInfo.Participants {
		// Check if participant has a LID and a phone number
		if participant.JID.Server == "lid" && !participant.PhoneNumber.IsEmpty() {
			// Store mapping: participant LID -> phone number string
			participants[participant.JID] = participant.PhoneNumber.String()
		}
	}

	// Only take lock for the final write operation
	w.mu.Lock()
	if w.groupParticipants == nil {
		w.groupParticipants = make(map[string]map[types.JID]string)
	}
	w.groupParticipants[groupJID.String()] = participants
	w.mu.Unlock()
}

// NewWhatsAppProvider creates a new instance of the WhatsAppProvider.
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
	}
}

// Init initializes the WhatsApp provider with its configuration.
func (w *WhatsAppProvider) Init(config core.ProviderConfig) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if config != nil {
		w.config = config
	} else {
		w.config = make(core.ProviderConfig)
	}

	// Automatically determine database path (never ask user for this)
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	dbPath := filepath.Join(configDir, "Loom", "whatsapp", "whatsapp.db")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create database connection string
	dbConnStr := fmt.Sprintf("file:%s?_foreign_keys=on", dbPath)

	// Initialize database logger
	dbLog := waLog.Stdout("Database", "DEBUG", false)

	// Create container
	container, err := sqlstore.New(w.ctx, "sqlite3", dbConnStr, dbLog)
	if err != nil {
		return fmt.Errorf("failed to create store container: %w", err)
	}
	w.container = container

	// Get device store
	deviceStore, err := container.GetFirstDevice(w.ctx)
	if err != nil {
		return fmt.Errorf("failed to get device store: %w", err)
	}
	w.deviceStore = deviceStore

	// Initialize client logger
	clientLog := waLog.Stdout("Client", "DEBUG", false)

	// Create client
	client := whatsmeow.NewClient(deviceStore, clientLog)
	w.client = client

	// Add event handler
	client.AddEventHandler(w.eventHandler)

	return nil
}

// GetConfig returns the current configuration of the WhatsApp provider.
func (w *WhatsAppProvider) GetConfig() core.ProviderConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.config
}

// SetConfig updates the configuration of the WhatsApp provider.
func (w *WhatsAppProvider) SetConfig(config core.ProviderConfig) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config = config
	return nil
}

// GetQRCode returns the latest QR code string for authentication.
func (w *WhatsAppProvider) GetQRCode() (string, error) {
	w.qrMu.RLock()
	defer w.qrMu.RUnlock()
	return w.latestQRCode, nil
}

// IsAuthenticated returns true if the WhatsApp provider is already authenticated.
func (w *WhatsAppProvider) IsAuthenticated() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil || w.client.Store == nil {
		return false
	}

	// If Store.ID is set, we're authenticated
	return w.client.Store.ID != nil
}

// Connect establishes the connection with WhatsApp.
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
		// Always get a fresh QR channel if not already set
		if !w.qrChanSet {
			qrChan, err := w.client.GetQRChannel(w.ctx)
			if err != nil {
				return fmt.Errorf("failed to get QR channel: %w", err)
			}
			w.qrChan = qrChan
			w.qrChanSet = true
			fmt.Println("WhatsApp: QR channel obtained")
		}

		fmt.Println("WhatsApp: Starting to listen for QR events...")

		// Start goroutine to handle QR code updates
		go func() {
			qrCodeCount := 0
			for evt := range w.qrChan {
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
			fmt.Println("WhatsApp: QR channel closed")
			// Reset QR channel state when channel closes
			w.mu.Lock()
			w.qrChanSet = false
			w.qrChan = nil
			w.mu.Unlock()
		}()
	} else {
		fmt.Printf("WhatsApp: Already logged in as %s, no QR code needed\n", w.client.Store.ID)
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

// Disconnect closes the connection and stops all background operations.
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

// eventHandler handles WhatsApp events and converts them to ProviderEvent.
func (w *WhatsAppProvider) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		// Convert WhatsApp message to our Message model
		msg := w.convertWhatsAppMessage(v)
		if msg != nil {
			w.appendMessageToConversation(msg)
			select {
			case w.eventChan <- core.MessageEvent{Message: *msg}:
			default:
			}
		}
	case *events.Presence:
		// Handle typing indicators (Presence events include typing status)
		// TODO: Fix typing indicator handling once we understand the exact structure
		// For now, we'll skip this to get the basic functionality working
	case *events.QR:
		// QR code event - this is handled by the QR channel
		// No need to log here as it's already handled by the QR channel goroutine
	case *events.Connected:
		fmt.Println("WhatsApp: Connected event received - client is now fully connected")
		// Check if we're logged in now
		if w.client != nil && w.client.Store.ID != nil {
			fmt.Printf("WhatsApp: Successfully logged in as %s\n", w.client.Store.ID)
			// Note: Device name is set during initial pairing via QR code
			// To change the name to "loom", you need to unpair and re-pair the device
			// The name will appear as "loom" after re-pairing
			fmt.Println("WhatsApp: Ready to receive messages and conversations")
			// Emit sync status event - connection successful, starting sync
			// Don't emit completed here - wait for OfflineSyncCompleted which is the final event
			w.emitSyncStatus(core.SyncStatusFetchingContacts, "Connected, synchronizing...", -1)

			// Fallback: If OfflineSyncCompleted is not received within 30 seconds, emit completed anyway
			go func() {
				time.Sleep(30 * time.Second)
				// Check if we're still connected and have conversations
				if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
					contacts, err := w.GetContacts()
					if err == nil && len(contacts) > 0 {
						fmt.Printf("WhatsApp: Fallback - emitting completed sync status after 30s timeout with %d conversations\n", len(contacts))
						w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
					}
				}
			}()
		}
	case *events.Disconnected:
		fmt.Printf("WhatsApp: Disconnected event received\n")
	case *events.LoggedOut:
		fmt.Println("WhatsApp: Logged out event received")
	case *events.StreamError:
		fmt.Printf("WhatsApp: Stream error: %v\n", v)
	case *events.HistorySync:
		// History sync contains conversations and messages
		fmt.Println("WhatsApp: History sync received - conversations are being synced")

		// Cache conversations from the history sync data so we can display them immediately
		if v != nil && v.Data != nil {
			w.cacheConversationsFromHistory(v.Data)
			w.cacheMessagesFromHistory(v.Data)
		}

		// Emit sync status event - history sync in progress
		// Don't emit completed here - wait for OfflineSyncCompleted which is the final event
		w.emitSyncStatus(core.SyncStatusFetchingHistory, "Syncing message history...", -1)
		// Trigger a contact refresh after history sync
		// Use a goroutine to delay the refresh slightly to allow whatsmeow to process the sync
		go func() {
			time.Sleep(2 * time.Second) // Wait a bit for sync to complete
			select {
			case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
			default:
			}
		}()
	case *events.AppStateSyncComplete:
		// App state sync completed - contacts and conversations are now available
		fmt.Printf("WhatsApp: App state sync completed for collection: %s\n", v.Name)
		// After app state sync, conversations should be available
		// Emit sync status event - app state sync completed, now fetching contacts
		// Don't emit completed here - wait for OfflineSyncCompleted which is the final event
		w.emitSyncStatus(core.SyncStatusFetchingContacts, "Fetching conversations...", -1)
		go func() {
			select {
			case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
			default:
			}
		}()
	case *events.OfflineSyncCompleted:
		// Offline sync completed - all data is now synced
		// This is the FINAL sync event - emit completed status here
		fmt.Println("WhatsApp: Offline sync completed - conversations should now be available")
		// Wait a bit for the store to be fully populated, then fetch contacts and emit final completed
		go func() {
			time.Sleep(2 * time.Second) // Wait for store to be fully populated
			contacts, err := w.GetContacts()
			if err != nil {
				fmt.Printf("WhatsApp: Failed to fetch contacts after offline sync: %v\n", err)
				fmt.Printf("WhatsApp: Emitting error sync status event\n")
				w.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to refresh conversations: %v", err), -1)
			} else {
				fmt.Printf("WhatsApp: Fetched %d conversations after offline sync\n", len(contacts))
				// This is the final completed event - sync is fully done
				fmt.Printf("WhatsApp: Emitting completed sync status event with %d conversations\n", len(contacts))
				w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
				fmt.Printf("WhatsApp: Completed sync status event emitted\n")
			}
			// Emit a contact refresh event
			select {
			case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
			default:
			}
		}()
	default:
		// Log other events for debugging
		fmt.Printf("WhatsApp: Unhandled event type: %T\n", evt)
	}
}

// convertWhatsAppMessage converts a WhatsApp message to our Message model.
func (w *WhatsAppProvider) convertWhatsAppMessage(evt *events.Message) *models.Message {
	msg := evt.Message
	if msg == nil {
		return nil
	}

	// Get conversation ID
	convID := evt.Info.Chat.String()
	chatJID := evt.Info.Chat

	// Track groups from messages
	if chatJID.Server == types.GroupServer {
		// Try to get group info from client to get the name
		// Do this without holding the lock to avoid deadlocks
		if w.client != nil {
			groupInfo, err := w.client.GetGroupInfo(w.ctx, chatJID)
			if err == nil && groupInfo != nil {
				w.mu.Lock()
				w.knownGroups[convID] = groupInfo.Name
				w.mu.Unlock()
				// Cache group participants asynchronously to avoid blocking
				// This avoids deadlocks since cacheGroupParticipants also takes locks
				go w.cacheGroupParticipants(chatJID)
			} else {
				w.mu.RLock()
				_, exists := w.knownGroups[convID]
				w.mu.RUnlock()
				if !exists {
					w.mu.Lock()
					w.knownGroups[convID] = convID
					w.mu.Unlock()
				}
			}
		}
	}

	// Get message ID
	msgID := evt.Info.ID

	// Get sender ID
	senderID := evt.Info.Sender.String()

	// Check if message is from me
	isFromMe := evt.Info.IsFromMe
	var senderName string
	if isFromMe {
		senderName = "You"
	} else {
		// For group messages, use group-aware lookup to handle LID participants
		if chatJID.Server == types.GroupServer {
			// Try push name first (most reliable for groups)
			if evt.Info.PushName != "" {
				senderName = evt.Info.PushName
			} else {
				// Use group-aware lookup to handle LID participants
				senderName = w.lookupSenderNameInGroup(evt.Info.Sender, chatJID)
			}
		} else {
			// For individual chats, use normal lookup
			senderName = w.lookupSenderName(evt.Info.Sender)
		}
		// If still empty, use push name as fallback
		if senderName == "" && evt.Info.PushName != "" {
			senderName = evt.Info.PushName
		}
		// If still empty, use sender ID as last resort
		if senderName == "" {
			senderName = senderID
		}
	}

	// Get message text
	body := ""
	if msg.GetConversation() != "" {
		body = msg.GetConversation()
	} else if msg.GetExtendedTextMessage() != nil {
		body = msg.GetExtendedTextMessage().GetText()
	}

	// Get timestamp
	timestamp := evt.Info.Timestamp

	return &models.Message{
		ProtocolConvID: convID,
		ProtocolMsgID:  msgID,
		SenderID:       senderID,
		SenderName:     senderName,
		Body:           body,
		Timestamp:      timestamp,
		IsFromMe:       isFromMe,
	}
}

func (w *WhatsAppProvider) storeMessagesForConversation(convID string, messages []models.Message) int {
	if convID == "" || len(messages) == 0 {
		return 0
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conversationMessages == nil {
		w.conversationMessages = make(map[string][]models.Message)
	}

	existing := append([]models.Message{}, w.conversationMessages[convID]...)
	combined := append(existing, messages...)

	sort.SliceStable(combined, func(i, j int) bool {
		return combined[i].Timestamp.Before(combined[j].Timestamp)
	})

	seens := make(map[string]struct{}, len(combined))
	dedup := make([]models.Message, 0, len(combined))
	for _, msg := range combined {
		key := msg.ProtocolMsgID
		if key != "" {
			if _, exists := seens[key]; exists {
				continue
			}
			seens[key] = struct{}{}
		}
		dedup = append(dedup, msg)
	}

	if len(dedup) > maxMessagesPerConversation {
		dedup = dedup[len(dedup)-maxMessagesPerConversation:]
	}

	w.conversationMessages[convID] = dedup
	return len(dedup)
}

func (w *WhatsAppProvider) appendMessageToConversation(msg *models.Message) {
	if msg == nil {
		return
	}
	w.storeMessagesForConversation(msg.ProtocolConvID, []models.Message{*msg})
}

func (w *WhatsAppProvider) hasConversationHistory(convID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.conversationMessages[convID]) > 0
}

func (w *WhatsAppProvider) cacheMessagesFromHistory(history *waHistorySync.HistorySync) {
	if history == nil || w.client == nil {
		return
	}

	conversations := history.GetConversations()
	for _, conv := range conversations {
		if conv == nil {
			continue
		}
		convID := conv.GetID()
		if convID == "" {
			continue
		}
		chatJID, err := types.ParseJID(convID)
		if err != nil {
			continue
		}
		historyMsgs := conv.GetMessages()
		if len(historyMsgs) == 0 {
			continue
		}

		converted := make([]models.Message, 0, len(historyMsgs))
		for _, hMsg := range historyMsgs {
			if hMsg == nil || hMsg.GetMessage() == nil {
				continue
			}
			evt, err := w.client.ParseWebMessage(chatJID, hMsg.GetMessage())
			if err != nil {
				fmt.Printf("WhatsApp: Failed to parse history message for %s: %v\n", convID, err)
				continue
			}
			if msg := w.convertWhatsAppMessage(evt); msg != nil {
				converted = append(converted, *msg)
			}
		}

		if len(converted) > 0 {
			total := w.storeMessagesForConversation(convID, converted)
			fmt.Printf("WhatsApp: Cached %d messages from history for %s (total stored: %d)\n", len(converted), convID, total)
		}
	}
}

// StreamEvents returns a channel for receiving real-time events.
func (w *WhatsAppProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	return w.eventChan, nil
}

// GetContacts returns the list of conversations (chats) for WhatsApp.
// This returns conversations, not just contacts, as they represent the actual chats.
func (w *WhatsAppProvider) GetContacts() ([]models.LinkedAccount, error) {
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Check if client is connected (Store.ID is set after successful login)
	if w.client.Store == nil || w.client.Store.ID == nil {
		return []models.LinkedAccount{}, nil
	}

	// Start with cached conversations discovered via history sync
	w.mu.RLock()
	cachedCount := len(w.conversations)
	linkedAccounts := make([]models.LinkedAccount, 0, cachedCount+32)
	seen := make(map[string]struct{}, cachedCount)
	for _, conv := range w.conversations {
		linkedAccounts = append(linkedAccounts, conv)
		seen[conv.UserID] = struct{}{}
	}
	w.mu.RUnlock()

	// Fall back to the contact store and joined groups
	// Note: getContactsFallback may need to write to w.knownGroups, so we don't hold the lock
	fallbackAccounts, err := w.getContactsFallback()
	if err != nil {
		return nil, err
	}

	for _, acc := range fallbackAccounts {
		if _, exists := seen[acc.UserID]; exists {
			continue
		}
		linkedAccounts = append(linkedAccounts, acc)
		seen[acc.UserID] = struct{}{}
	}

	filtered := w.filterAccountsWithHistory(linkedAccounts)
	if len(filtered) > 0 {
		linkedAccounts = filtered
	}

	fmt.Printf("WhatsApp: GetContacts returning %d conversations (%d cached + %d fallback)\n", len(linkedAccounts), cachedCount, len(fallbackAccounts))
	return linkedAccounts, nil
}

func (w *WhatsAppProvider) filterAccountsWithHistory(accounts []models.LinkedAccount) []models.LinkedAccount {
	w.mu.RLock()
	historyCount := len(w.conversationMessages)
	w.mu.RUnlock()

	if historyCount == 0 {
		return nil
	}

	filtered := make([]models.LinkedAccount, 0, len(accounts))
	for _, acc := range accounts {
		if w.hasConversationHistory(acc.UserID) {
			filtered = append(filtered, acc)
		}
	}
	return filtered
}

// getContactsFallback returns contacts and groups as conversations.
func (w *WhatsAppProvider) getContactsFallback() ([]models.LinkedAccount, error) {
	// Check if store is available
	if w.client == nil || w.client.Store == nil || w.client.Store.Contacts == nil {
		fmt.Printf("WhatsApp: Store not available yet (client=%v, store=%v, contacts=%v)\n",
			w.client != nil,
			w.client != nil && w.client.Store != nil,
			w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil)
		return []models.LinkedAccount{}, nil
	}

	// Get contacts - these represent people you can chat with
	// Note: GetAllContacts only returns contacts in the address book, not all conversations
	contacts, err := w.client.Store.Contacts.GetAllContacts(w.ctx)
	if err != nil {
		fmt.Printf("WhatsApp: GetAllContacts failed: %v\n", err)
		contacts = make(map[types.JID]types.ContactInfo)
	} else {
		fmt.Printf("WhatsApp: GetAllContacts returned %d contacts\n", len(contacts))
	}

	linkedAccounts := make([]models.LinkedAccount, 0, len(contacts))
	now := time.Now()

	// Add individual contacts
	for jid, contact := range contacts {
		// Skip groups (they have @g.us server)
		if jid.Server == types.GroupServer || jid.Server == types.BroadcastServer {
			continue
		}

		displayName := contact.FullName
		if displayName == "" {
			displayName = contact.FirstName
		}
		if displayName == "" {
			displayName = contact.PushName
		}
		displayName = w.lookupDisplayName(jid, displayName)

		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    jid.String(),
			Username:  displayName,
			Status:    "offline",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	// Add known groups (tracked from messages)
	w.mu.RLock()
	for groupJID, groupName := range w.knownGroups {
		displayName := groupName
		if jid, err := types.ParseJID(groupJID); err == nil {
			displayName = w.lookupDisplayName(jid, groupName)
		}
		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    groupJID,
			Username:  displayName,
			Status:    "offline",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	w.mu.RUnlock()

	// Try to get groups using GetJoinedGroups if available
	if w.client != nil && w.client.Store.ID != nil {
		fmt.Printf("WhatsApp: Attempting to fetch groups via GetJoinedGroups...\n")
		groups, err := w.client.GetJoinedGroups(w.ctx)
		if err == nil && len(groups) > 0 {
			fmt.Printf("WhatsApp: Found %d groups via GetJoinedGroups\n", len(groups))
			groupsAdded := 0
			for _, group := range groups {
				if group == nil {
					continue
				}
				groupJID := group.JID.String()
				alreadyAdded := false
				for _, acc := range linkedAccounts {
					if acc.UserID == groupJID {
						alreadyAdded = true
						fmt.Printf("WhatsApp: Group %s already in linkedAccounts, skipping\n", groupJID)
						break
					}
				}
				if alreadyAdded {
					continue
				}
				displayName := group.Name
				if displayName == "" {
					displayName = groupJID
				}
				displayName = w.lookupDisplayName(group.JID, displayName)
				linkedAccounts = append(linkedAccounts, models.LinkedAccount{
					Protocol:  "whatsapp",
					UserID:    groupJID,
					Username:  displayName,
					Status:    "offline",
					CreatedAt: now,
					UpdatedAt: now,
				})
				// Store in known groups
				w.mu.Lock()
				w.knownGroups[groupJID] = displayName
				w.mu.Unlock()
				fmt.Printf("WhatsApp: Added group %s (%s)\n", displayName, groupJID)
				groupsAdded++
			}
			fmt.Printf("WhatsApp: Added %d new groups from GetJoinedGroups (total linkedAccounts: %d)\n", groupsAdded, len(linkedAccounts))
		} else if err != nil {
			fmt.Printf("WhatsApp: Could not get groups via GetJoinedGroups: %v\n", err)
		} else {
			fmt.Printf("WhatsApp: No groups found via GetJoinedGroups\n")
		}
	} else {
		fmt.Printf("WhatsApp: Cannot fetch groups - client=%v, Store.ID=%v\n", w.client != nil, w.client != nil && w.client.Store != nil && w.client.Store.ID != nil)
	}

	fmt.Printf("WhatsApp: Retrieved %d contacts/conversations (including groups)\n", len(linkedAccounts))
	return linkedAccounts, nil
}

// cacheConversationsFromHistory stores conversations discovered during history sync.
func (w *WhatsAppProvider) cacheConversationsFromHistory(history *waHistorySync.HistorySync) {
	if history == nil {
		return
	}

	conversations := history.GetConversations()
	if len(conversations) == 0 {
		fmt.Printf("WhatsApp: History sync contained 0 conversations\n")
		return
	}
	fmt.Printf("WhatsApp: Processing %d conversations from history sync\n", len(conversations))

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conversations == nil {
		w.conversations = make(map[string]models.LinkedAccount)
	}

	added := 0
	skippedNil := 0
	skippedEmptyID := 0
	skippedInvalidJID := 0

	for i, conv := range conversations {
		if conv == nil {
			skippedNil++
			fmt.Printf("WhatsApp: Conversation[%d] is nil\n", i)
			continue
		}

		jidString := conv.GetID()
		if jidString == "" {
			skippedEmptyID++
			fmt.Printf("WhatsApp: Conversation[%d] has empty ID\n", i)
			continue
		}

		jid, err := types.ParseJID(jidString)
		if err != nil {
			skippedInvalidJID++
			fmt.Printf("WhatsApp: Conversation[%d] has invalid JID %s: %v\n", i, jidString, err)
			continue
		}

		displayName := w.lookupDisplayName(jid, conv.GetName())
		if displayName == "" {
			displayName = w.lookupDisplayName(jid, conv.GetDisplayName())
		}

		lastActivity := time.Now()
		if ts := conv.GetConversationTimestamp(); ts > 0 {
			lastActivity = time.Unix(int64(ts), 0)
		}

		linked := models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    jid.String(),
			Username:  displayName,
			Status:    "active",
			CreatedAt: lastActivity,
			UpdatedAt: lastActivity,
		}

		w.conversations[linked.UserID] = linked
		fmt.Printf("WhatsApp: Cached conversation[%d]: %s (%s)\n", i, displayName, jid.String())

		if jid.Server == types.GroupServer {
			w.knownGroups[linked.UserID] = displayName
			fmt.Printf("WhatsApp: Also cached as group: %s\n", displayName)
		}

		added++
	}

	fmt.Printf("WhatsApp: Cache summary - Added: %d, Skipped (nil): %d, Skipped (empty ID): %d, Skipped (invalid JID): %d\n",
		added, skippedNil, skippedEmptyID, skippedInvalidJID)

	fmt.Printf("WhatsApp: Cached %d conversations from history sync (total cached: %d)\n", added, len(w.conversations))
	if added == 0 {
		fmt.Printf("WhatsApp: WARNING - No conversations were cached despite %d in history sync!\n", len(conversations))
	}
}

// GetConversationHistory retrieves the message history for a specific conversation.
func (w *WhatsAppProvider) GetConversationHistory(conversationID string, limit int) ([]models.Message, error) {
	if conversationID == "" {
		return []models.Message{}, fmt.Errorf("conversation ID is required")
	}

	w.mu.RLock()
	messages, ok := w.conversationMessages[conversationID]
	w.mu.RUnlock()

	if !ok || len(messages) == 0 {
		return []models.Message{}, nil
	}

	start := 0
	if limit > 0 && len(messages) > limit {
		start = len(messages) - limit
	}

	result := make([]models.Message, len(messages)-start)
	copy(result, messages[start:])
	return result, nil
}

// SendMessage sends a text message to a given conversation.
func (w *WhatsAppProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	markUnused(file, threadID)

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Create message
	msg := &waE2E.Message{
		Conversation: &text,
	}

	// Send message
	resp, err := w.client.SendMessage(w.ctx, jid, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Convert to our Message model
	return &models.Message{
		ProtocolConvID: conversationID,
		ProtocolMsgID:  resp.ID,
		SenderID:       w.client.Store.ID.String(),
		Body:           text,
		Timestamp:      time.Now(),
		IsFromMe:       true,
	}, nil
}

// SendFile sends a file to a given conversation without text.
func (w *WhatsAppProvider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	// TODO: Implement file sending
	markUnused(conversationID, file, threadID)
	return nil, fmt.Errorf("file sending not yet implemented")
}

// GetThreads loads all messages in a discussion thread from a parent message ID.
func (w *WhatsAppProvider) GetThreads(parentMessageID string) ([]models.Message, error) {
	// TODO: Implement thread retrieval
	markUnused(parentMessageID)
	return []models.Message{}, nil
}

// AddReaction adds a reaction (emoji) to a message.
func (w *WhatsAppProvider) AddReaction(conversationID string, messageID string, emoji string) error {
	// TODO: Implement reaction adding
	markUnused(conversationID, messageID, emoji)
	return fmt.Errorf("reactions not yet implemented")
}

// RemoveReaction removes a reaction (emoji) from a message.
func (w *WhatsAppProvider) RemoveReaction(conversationID string, messageID string, emoji string) error {
	// TODO: Implement reaction removal
	markUnused(conversationID, messageID, emoji)
	return fmt.Errorf("reactions not yet implemented")
}

// SendTypingIndicator sends a typing indicator to a conversation.
func (w *WhatsAppProvider) SendTypingIndicator(conversationID string, isTyping bool) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Send typing indicator
	if isTyping {
		err = w.client.SendChatPresence(w.ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	} else {
		err = w.client.SendChatPresence(w.ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
	}
	if err != nil {
		return fmt.Errorf("failed to send typing indicator: %w", err)
	}

	return nil
}

// --- Group Management ---

// CreateGroup creates a new group conversation.
func (w *WhatsAppProvider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	// TODO: Implement group creation
	markUnused(groupName, participantIDs)
	return nil, fmt.Errorf("group creation not yet implemented")
}

// UpdateGroupName updates the name of a group.
func (w *WhatsAppProvider) UpdateGroupName(conversationID string, newName string) error {
	// TODO: Implement group name update
	markUnused(conversationID, newName)
	return fmt.Errorf("group name update not yet implemented")
}

// AddGroupParticipants adds participants to a group.
func (w *WhatsAppProvider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	// TODO: Implement adding participants
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("adding participants not yet implemented")
}

// RemoveGroupParticipants removes participants from a group.
func (w *WhatsAppProvider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	// TODO: Implement removing participants
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("removing participants not yet implemented")
}

// LeaveGroup leaves a group conversation.
func (w *WhatsAppProvider) LeaveGroup(conversationID string) error {
	// TODO: Implement leaving group
	markUnused(conversationID)
	return fmt.Errorf("leaving group not yet implemented")
}

// PromoteGroupAdmins promotes participants to admin in a group.
func (w *WhatsAppProvider) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	// TODO: Implement promoting admins
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("promoting admins not yet implemented")
}

// DemoteGroupAdmins demotes admins to regular participants in a group.
func (w *WhatsAppProvider) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	// TODO: Implement demoting admins
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("demoting admins not yet implemented")
}

// GetGroupParticipants returns the list of participants in a group.
func (w *WhatsAppProvider) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	// TODO: Implement getting participants
	markUnused(conversationID)
	return []models.GroupParticipant{}, nil
}

// --- Invite Links ---

// CreateGroupInviteLink creates an invite link for a group.
func (w *WhatsAppProvider) CreateGroupInviteLink(conversationID string) (string, error) {
	// TODO: Implement invite link creation
	markUnused(conversationID)
	return "", fmt.Errorf("invite links not yet implemented")
}

// RevokeGroupInviteLink revokes the current invite link for a group.
func (w *WhatsAppProvider) RevokeGroupInviteLink(conversationID string) error {
	// TODO: Implement invite link revocation
	markUnused(conversationID)
	return fmt.Errorf("invite links not yet implemented")
}

// JoinGroupByInviteLink joins a group using an invite link.
func (w *WhatsAppProvider) JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error) {
	// TODO: Implement joining via invite link
	markUnused(inviteLink)
	return nil, fmt.Errorf("invite links not yet implemented")
}

// JoinGroupByInviteMessage joins a group using an invite message.
func (w *WhatsAppProvider) JoinGroupByInviteMessage(inviteMessageID string) (*models.Conversation, error) {
	// TODO: Implement joining via invite message
	markUnused(inviteMessageID)
	return nil, fmt.Errorf("invite messages not yet implemented")
}

// --- Receipts ---

// MarkMessageAsRead marks a message as read.
func (w *WhatsAppProvider) MarkMessageAsRead(conversationID string, messageID string) error {
	// TODO: Implement marking message as read
	markUnused(conversationID, messageID)
	return fmt.Errorf("marking as read not yet implemented")
}

// MarkConversationAsRead marks all messages in a conversation as read.
func (w *WhatsAppProvider) MarkConversationAsRead(conversationID string) error {
	// TODO: Implement marking conversation as read
	markUnused(conversationID)
	return fmt.Errorf("marking conversation as read not yet implemented")
}

// --- App State (Pin/Mute) ---

// PinConversation pins a conversation.
func (w *WhatsAppProvider) PinConversation(conversationID string) error {
	// TODO: Implement pinning
	markUnused(conversationID)
	return fmt.Errorf("pinning not yet implemented")
}

// UnpinConversation unpins a conversation.
func (w *WhatsAppProvider) UnpinConversation(conversationID string) error {
	// TODO: Implement unpinning
	markUnused(conversationID)
	return fmt.Errorf("unpinning not yet implemented")
}

// MuteConversation mutes a conversation.
func (w *WhatsAppProvider) MuteConversation(conversationID string) error {
	// TODO: Implement muting
	markUnused(conversationID)
	return fmt.Errorf("muting not yet implemented")
}

// UnmuteConversation unmutes a conversation.
func (w *WhatsAppProvider) UnmuteConversation(conversationID string) error {
	// TODO: Implement unmuting
	markUnused(conversationID)
	return fmt.Errorf("unmuting not yet implemented")
}

// GetConversationState returns the state of a conversation (pin/mute status, etc.).
func (w *WhatsAppProvider) GetConversationState(conversationID string) (*models.Conversation, error) {
	// TODO: Implement getting conversation state
	markUnused(conversationID)
	return nil, fmt.Errorf("getting conversation state not yet implemented")
}

// --- Retry Receipts ---

// SendRetryReceipt sends a retry receipt when message decryption fails.
func (w *WhatsAppProvider) SendRetryReceipt(conversationID string, messageID string) error {
	// TODO: Implement retry receipts
	markUnused(conversationID, messageID)
	return fmt.Errorf("retry receipts not yet implemented")
}

// --- Status Messages ---

// SendStatusMessage sends a status message (broadcast to all contacts).
func (w *WhatsAppProvider) SendStatusMessage(text string, file *core.Attachment) (*models.Message, error) {
	// TODO: Implement status messages
	markUnused(text, file)
	return nil, fmt.Errorf("status messages not yet implemented")
}

// SyncHistory retrieves message history since a certain date.
// For WhatsApp, history is automatically synced when connected.
// This method triggers a refresh of contacts and conversations.
func (w *WhatsAppProvider) SyncHistory(since time.Time) error {
	w.mu.RLock()
	client := w.client
	w.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Check if client is connected (Store.ID is set after successful login)
	w.mu.RLock()
	store := client.Store
	w.mu.RUnlock()

	if store == nil || store.ID == nil {
		// Client is not connected yet, return without error
		fmt.Printf("WhatsApp: SyncHistory called but client not connected yet, skipping...\n")
		return nil
	}

	// Emit sync status event
	w.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing history since %s...", since.Format("2006-01-02 15:04:05")), -1)

	fmt.Printf("WhatsApp: SyncHistory called for messages since %s\n", since.Format("2006-01-02 15:04:05"))

	// WhatsApp automatically syncs history when connected via HistorySync events
	// We need to force a refresh of contacts to get the latest conversations
	// The actual message history sync happens automatically through whatsmeow's event system

	// Trigger a refresh of contacts in a goroutine to avoid blocking
	go func() {
		time.Sleep(500 * time.Millisecond)

		w.emitSyncStatus(core.SyncStatusFetchingContacts, "Refreshing conversations...", 90)

		contacts, err := w.GetContacts()
		if err != nil {
			fmt.Printf("WhatsApp: Failed to refresh contacts during sync: %v\n", err)
			w.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to refresh conversations: %v", err), -1)
			return
		}

		fmt.Printf("WhatsApp: Refreshed %d conversations during sync\n", len(contacts))

		select {
		case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
		default:
		}

		w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
	}()

	return nil
}
