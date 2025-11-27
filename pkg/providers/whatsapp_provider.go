// Package providers contains implementations of the Provider interface.
package providers

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
	avatarLoading        map[string]bool                 // Track which avatars are currently being loaded to avoid duplicates
	avatarLoadingMu      sync.Mutex                      // Mutex for avatarLoading map
	avatarFailures       map[string]bool                 // Track avatars that failed to load (401 errors) to avoid retrying
	avatarFailuresMu     sync.RWMutex                    // Mutex for avatarFailures map
	lastSyncTimestamp    *time.Time                      // Timestamp of last successful sync (loaded from DB)
	groupsCacheTimestamp *time.Time                      // Timestamp when groups were last fetched (to avoid repeated API calls)
	groupsCache          []models.LinkedAccount          // Cached groups from GetJoinedGroups
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

// getProfilePictureURL retrieves the profile picture URL for a given JID.
// Downloads and caches the image locally, returns the local file path.
// Returns empty string if not available or on error.
func (w *WhatsAppProvider) getProfilePictureURL(jid types.JID) string {
	if w.client == nil || jid.IsEmpty() {
		return ""
	}

	jidStr := jid.String()

	// Check if this avatar has previously failed (401 error)
	w.avatarFailuresMu.RLock()
	if w.avatarFailures[jidStr] {
		w.avatarFailuresMu.RUnlock()
		// Avatar previously failed, don't retry
		return ""
	}
	w.avatarFailuresMu.RUnlock()

	// Get cache directory - use whatsapp subdirectory
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("WhatsApp: Failed to get config directory for avatar cache: %v\n", err)
		return ""
	}
	cacheDir := filepath.Join(configDir, "Loom", "whatsapp", "avatars")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		fmt.Printf("WhatsApp: Failed to create avatar cache directory: %v\n", err)
		return ""
	}

	// Get profile picture info
	picInfo, err := w.client.GetProfilePictureInfo(w.ctx, jid, &whatsmeow.GetProfilePictureParams{
		Preview:     false,
		ExistingID:  "",
		IsCommunity: false,
	})
	if err != nil || picInfo == nil || picInfo.URL == "" {
		// Profile picture not available or error
		if err != nil {
			errStr := err.Error()
			// Check if it's a 401/not-authorized error
			if strings.Contains(errStr, "not-authorized") || strings.Contains(errStr, "401") {
				// Mark this avatar as failed to avoid retrying
				w.avatarFailuresMu.Lock()
				if w.avatarFailures == nil {
					w.avatarFailures = make(map[string]bool)
				}
				w.avatarFailures[jidStr] = true
				w.avatarFailuresMu.Unlock()
				// Save failures to disk
				w.saveAvatarFailures()
				// Don't log 401 errors as they're expected for some contacts
			} else {
				fmt.Printf("WhatsApp: Failed to get profile picture info for %s: %v\n", jidStr, err)
			}
		}
		return ""
	}

	// Generate a unique filename based on JID and picture hash
	hash := sha256.Sum256([]byte(jid.String() + picInfo.ID))
	filename := hex.EncodeToString(hash[:]) + ".jpg"

	// Check if avatar is already cached
	cachePath := filepath.Join(cacheDir, filename)
	if _, err := os.Stat(cachePath); err == nil {
		// File exists, return the path
		return cachePath
	}

	// Download the image
	resp, err := http.Get(picInfo.URL)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to download avatar for %s: %v\n", jid.String(), err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("WhatsApp: Failed to download avatar for %s: status %d\n", jid.String(), resp.StatusCode)
		return ""
	}

	// Create the file
	file, err := os.Create(cachePath)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to create avatar file: %v\n", err)
		return ""
	}
	defer file.Close()

	// Copy the image data
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to save avatar file: %v\n", err)
		os.Remove(cachePath) // Clean up on error
		return ""
	}

	return cachePath
}

// loadAvatarFailures loads the avatar failures cache from disk.
func (w *WhatsAppProvider) loadAvatarFailures() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	cacheFile := filepath.Join(configDir, "Loom", "whatsapp", "avatar_failures.json")

	data, err := os.ReadFile(cacheFile)
	if err != nil {
		// File doesn't exist yet, that's okay
		return
	}

	var failures []string
	if err := json.Unmarshal(data, &failures); err != nil {
		fmt.Printf("WhatsApp: Failed to parse avatar failures cache: %v\n", err)
		return
	}

	w.avatarFailuresMu.Lock()
	if w.avatarFailures == nil {
		w.avatarFailures = make(map[string]bool)
	}
	for _, jid := range failures {
		w.avatarFailures[jid] = true
	}
	w.avatarFailuresMu.Unlock()

	fmt.Printf("WhatsApp: Loaded %d avatar failures from cache\n", len(failures))
}

// saveAvatarFailures saves the avatar failures cache to disk.
func (w *WhatsAppProvider) saveAvatarFailures() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	cacheDir := filepath.Join(configDir, "Loom", "whatsapp")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return
	}
	cacheFile := filepath.Join(cacheDir, "avatar_failures.json")

	w.avatarFailuresMu.RLock()
	failures := make([]string, 0, len(w.avatarFailures))
	for jid := range w.avatarFailures {
		failures = append(failures, jid)
	}
	w.avatarFailuresMu.RUnlock()

	data, err := json.Marshal(failures)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to marshal avatar failures: %v\n", err)
		return
	}

	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		fmt.Printf("WhatsApp: Failed to save avatar failures cache: %v\n", err)
	}
}

// loadLastSyncTimestamp loads the last sync timestamp from database.
// This version locks the mutex itself.
func (w *WhatsAppProvider) loadLastSyncTimestamp() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLastSyncTimestampLocked()
}

// loadLastSyncTimestampLocked loads the last sync timestamp from database.
// This version assumes the mutex is already locked (for use in Init()).
func (w *WhatsAppProvider) loadLastSyncTimestampLocked() {
	if db.DB == nil {
		return
	}

	var config models.ProviderConfiguration
	err := db.DB.Where("provider_id = ?", "whatsapp").First(&config).Error
	if err == nil && config.LastSyncAt != nil {
		w.lastSyncTimestamp = config.LastSyncAt
		fmt.Printf("WhatsApp: Loaded last sync timestamp: %s\n", config.LastSyncAt.Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("WhatsApp: No previous sync timestamp found (first sync)\n")
	}
}

// saveLastSyncTimestamp saves the last sync timestamp to database.
func (w *WhatsAppProvider) saveLastSyncTimestamp(timestamp time.Time) {
	if db.DB == nil {
		return
	}

	w.mu.Lock()
	w.lastSyncTimestamp = &timestamp
	w.mu.Unlock()

	var config models.ProviderConfiguration
	err := db.DB.Where("provider_id = ?", "whatsapp").First(&config).Error
	if err == nil {
		// Update existing
		config.LastSyncAt = &timestamp
		config.UpdatedAt = time.Now()
		if err := db.DB.Save(&config).Error; err != nil {
			fmt.Printf("WhatsApp: Failed to save last sync timestamp: %v\n", err)
		} else {
			fmt.Printf("WhatsApp: Saved last sync timestamp: %s\n", timestamp.Format("2006-01-02 15:04:05"))
		}
	} else {
		// Create new
		config = models.ProviderConfiguration{
			ProviderID: "whatsapp",
			IsActive:   true,
			LastSyncAt: &timestamp,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := db.DB.Create(&config).Error; err != nil {
			fmt.Printf("WhatsApp: Failed to create provider configuration: %v\n", err)
		} else {
			fmt.Printf("WhatsApp: Created provider configuration with last sync timestamp: %s\n", timestamp.Format("2006-01-02 15:04:05"))
		}
	}
}

// downloadAndCacheAttachment downloads and caches a media attachment from WhatsApp.
// Returns the local file path and attachment info, or empty string on error.
func (w *WhatsAppProvider) downloadAndCacheAttachment(evt *events.Message, mediaType string) *models.Attachment {
	if w.client == nil {
		return nil
	}

	var media *waE2E.Message
	msg := evt.Message
	if msg == nil {
		return nil
	}

	// Determine media type and get the media message
	var fileName string
	var mimeType string
	var fileSize int64
	var thumbnailData []byte

	switch mediaType {
	case "image":
		if img := msg.GetImageMessage(); img != nil {
			media = &waE2E.Message{ImageMessage: img}
			mimeType = img.GetMimetype()
			fileSize = int64(img.GetFileLength())
			// Generate filename from mime type
			if strings.HasPrefix(mimeType, "image/") {
				ext := strings.TrimPrefix(mimeType, "image/")
				if ext == "jpeg" {
					ext = "jpg"
				}
				fileName = fmt.Sprintf("image.%s", ext)
			} else {
				fileName = "image.jpg"
			}
			if img.GetJPEGThumbnail() != nil {
				thumbnailData = img.GetJPEGThumbnail()
			}
		} else {
			return nil
		}
	case "video":
		if vid := msg.GetVideoMessage(); vid != nil {
			media = &waE2E.Message{VideoMessage: vid}
			mimeType = vid.GetMimetype()
			fileSize = int64(vid.GetFileLength())
			// Generate filename from mime type
			if strings.HasPrefix(mimeType, "video/") {
				ext := strings.TrimPrefix(mimeType, "video/")
				fileName = fmt.Sprintf("video.%s", ext)
			} else {
				fileName = "video.mp4"
			}
			if vid.GetJPEGThumbnail() != nil {
				thumbnailData = vid.GetJPEGThumbnail()
			}
		} else {
			return nil
		}
	case "audio":
		if aud := msg.GetAudioMessage(); aud != nil {
			media = &waE2E.Message{AudioMessage: aud}
			mimeType = aud.GetMimetype()
			fileSize = int64(aud.GetFileLength())
			// Generate filename from mime type
			if strings.HasPrefix(mimeType, "audio/") {
				ext := strings.TrimPrefix(mimeType, "audio/")
				fileName = fmt.Sprintf("audio.%s", ext)
			} else {
				fileName = "audio.mp3"
			}
		} else {
			return nil
		}
	case "document":
		if doc := msg.GetDocumentMessage(); doc != nil {
			media = &waE2E.Message{DocumentMessage: doc}
			fileName = doc.GetFileName()
			mimeType = doc.GetMimetype()
			fileSize = int64(doc.GetFileLength())
		} else {
			return nil
		}
	case "sticker":
		if stk := msg.GetStickerMessage(); stk != nil {
			media = &waE2E.Message{StickerMessage: stk}
			fileName = "sticker.webp"
			mimeType = "image/webp"
			fileSize = int64(stk.GetFileLength())
		} else {
			return nil
		}
	default:
		return nil
	}

	if media == nil {
		return nil
	}

	// Get cache directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("WhatsApp: Failed to get config directory for attachment cache: %v\n", err)
		return nil
	}
	cacheDir := filepath.Join(configDir, "Loom", "whatsapp", "attachments")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		fmt.Printf("WhatsApp: Failed to create attachment cache directory: %v\n", err)
		return nil
	}

	// Generate a unique filename based on message ID and media type
	hash := sha256.Sum256([]byte(evt.Info.ID + mediaType))
	ext := filepath.Ext(fileName)
	if ext == "" {
		// Determine extension from mime type
		switch {
		case strings.HasPrefix(mimeType, "image/"):
			ext = ".jpg"
		case strings.HasPrefix(mimeType, "video/"):
			ext = ".mp4"
		case strings.HasPrefix(mimeType, "audio/"):
			ext = ".mp3"
		case mimeType == "application/pdf":
			ext = ".pdf"
		case strings.HasPrefix(mimeType, "application/vnd.ms-excel") || strings.HasPrefix(mimeType, "application/vnd.openxmlformats-officedocument.spreadsheetml"):
			ext = ".xls"
		default:
			ext = ".bin"
		}
	}
	filename := hex.EncodeToString(hash[:]) + ext
	cachePath := filepath.Join(cacheDir, filename)

	// Check if file is already cached
	if _, err := os.Stat(cachePath); err == nil {
		// File exists, return the path
		att := &models.Attachment{
			Type:     mediaType,
			URL:      cachePath,
			FileName: fileName,
			FileSize: fileSize,
			MimeType: mimeType,
		}
		// Save thumbnail if available
		if len(thumbnailData) > 0 {
			thumbHash := sha256.Sum256([]byte(evt.Info.ID + mediaType + "thumb"))
			thumbFilename := hex.EncodeToString(thumbHash[:]) + ".jpg"
			thumbPath := filepath.Join(cacheDir, thumbFilename)
			if err := os.WriteFile(thumbPath, thumbnailData, 0644); err == nil {
				att.Thumbnail = thumbPath
			}
		}
		return att
	}

	// Download the media
	var downloadable whatsmeow.DownloadableMessage
	switch mediaType {
	case "image":
		downloadable = media.GetImageMessage()
	case "video":
		downloadable = media.GetVideoMessage()
	case "audio":
		downloadable = media.GetAudioMessage()
	case "document":
		downloadable = media.GetDocumentMessage()
	case "sticker":
		downloadable = media.GetStickerMessage()
	default:
		return nil
	}

	data, err := w.client.Download(w.ctx, downloadable)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to download %s attachment: %v\n", mediaType, err)
		return nil
	}

	// Create the file
	file, err := os.Create(cachePath)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to create attachment file: %v\n", err)
		return nil
	}
	defer file.Close()

	// Write the data
	_, err = file.Write(data)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to save attachment file: %v\n", err)
		os.Remove(cachePath) // Clean up on error
		return nil
	}

	att := &models.Attachment{
		Type:     mediaType,
		URL:      cachePath,
		FileName: fileName,
		FileSize: fileSize,
		MimeType: mimeType,
	}

	// Save thumbnail if available
	if len(thumbnailData) > 0 {
		thumbHash := sha256.Sum256([]byte(evt.Info.ID + mediaType + "thumb"))
		thumbFilename := hex.EncodeToString(thumbHash[:]) + ".jpg"
		thumbPath := filepath.Join(cacheDir, thumbFilename)
		if err := os.WriteFile(thumbPath, thumbnailData, 0644); err == nil {
			att.Thumbnail = thumbPath
		}
	}

	return att
}

// extractAttachments extracts attachments from a WhatsApp message.
func (w *WhatsAppProvider) extractAttachments(evt *events.Message) []models.Attachment {
	var attachments []models.Attachment
	msg := evt.Message
	if msg == nil {
		fmt.Printf("WhatsApp: extractAttachments: Message is nil\n")
		return attachments
	}

	// Check for different media types
	if imgMsg := msg.GetImageMessage(); imgMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found image message\n")
		if att := w.downloadAndCacheAttachment(evt, "image"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded image attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download image attachment\n")
		}
	}
	if vidMsg := msg.GetVideoMessage(); vidMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found video message\n")
		if att := w.downloadAndCacheAttachment(evt, "video"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded video attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download video attachment\n")
		}
	}
	if audMsg := msg.GetAudioMessage(); audMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found audio message\n")
		if att := w.downloadAndCacheAttachment(evt, "audio"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded audio attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download audio attachment\n")
		}
	}
	if docMsg := msg.GetDocumentMessage(); docMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found document message: %s\n", docMsg.GetFileName())
		if att := w.downloadAndCacheAttachment(evt, "document"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded document attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download document attachment\n")
		}
	}
	if stkMsg := msg.GetStickerMessage(); stkMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found sticker message\n")
		if att := w.downloadAndCacheAttachment(evt, "sticker"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded sticker attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download sticker attachment\n")
		}
	}

	if len(attachments) == 0 {
		fmt.Printf("WhatsApp: extractAttachments: No attachments found in message %s\n", evt.Info.ID)
	}

	return attachments
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
		avatarLoading:        make(map[string]bool),
		avatarFailures:       make(map[string]bool),
	}
}

// Init initializes the WhatsApp provider with its configuration.
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
	dbPath := filepath.Join(configDir, "Loom", "whatsapp", "whatsapp.db")
	fmt.Printf("WhatsAppProvider.Init: Database path: %s\n", dbPath)

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
// This checks if the store has an ID, which indicates previous authentication.
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
	fmt.Printf("WhatsApp: Received event type: %T\n", evt)
	switch v := evt.(type) {
	case *events.Message:
		// Convert WhatsApp message to our Message model
		fmt.Printf("WhatsApp: Received message event from %s in chat %s\n", v.Info.Sender.String(), v.Info.Chat.String())
		msg := w.convertWhatsAppMessage(v)
		if msg != nil {
			fmt.Printf("WhatsApp: Converted message successfully, ID: %s, Body: %s\n", msg.ProtocolMsgID, msg.Body)
			w.appendMessageToConversation(msg)

			// Update last sync timestamp when receiving a new message
			w.saveLastSyncTimestamp(msg.Timestamp)

			select {
			case w.eventChan <- core.MessageEvent{Message: *msg}:
				fmt.Printf("WhatsApp: MessageEvent emitted successfully for message %s\n", msg.ProtocolMsgID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for message %s\n", msg.ProtocolMsgID)
			}
			select {
			case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "message_received"}:
				fmt.Printf("WhatsApp: ContactStatusEvent emitted successfully\n")
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit ContactStatusEvent (channel full)\n")
			}
		} else {
			fmt.Printf("WhatsApp: WARNING - convertWhatsAppMessage returned nil for message from %s in chat %s\n", v.Info.Sender.String(), v.Info.Chat.String())
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
	case *events.Receipt:
		// Handle read receipts (message read confirmations)
		// Convert to ReceiptEvent and emit
		if len(v.MessageIDs) == 0 {
			fmt.Printf("WhatsApp: Receipt event has no message IDs, skipping.\n")
			break
		}
		receiptType := core.ReceiptTypeDelivery
		if v.Type == types.ReceiptTypeRead {
			receiptType = core.ReceiptTypeRead
		}
		fmt.Printf("WhatsApp: Processing receipt event for chat %s, type: %s, message IDs: %v\n", v.Chat.String(), receiptType, v.MessageIDs)
		// Emit a ReceiptEvent for each message ID
		for _, msgID := range v.MessageIDs {
			select {
			case w.eventChan <- core.ReceiptEvent{
				ConversationID: v.Chat.String(),
				MessageID:      msgID,
				ReceiptType:    receiptType,
				UserID:         v.Sender.String(),
				Timestamp:      v.Timestamp.Unix(),
			}:
				fmt.Printf("WhatsApp: ReceiptEvent emitted successfully for message %s\n", msgID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit ReceiptEvent for message %s (channel full)\n", msgID)
			}
		}
	case *events.HistorySync:
		// History sync contains conversations and messages
		fmt.Println("WhatsApp: History sync received - conversations are being synced")

		// Cache conversations from the history sync data so we can display them immediately
		if v != nil && v.Data != nil {
			w.cacheConversationsFromHistory(v.Data)
			w.cacheMessagesFromHistory(v.Data)

			// Update last sync timestamp after successful history sync
			now := time.Now()
			w.saveLastSyncTimestamp(now)
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

	// Extract attachments (download and cache them)
	attachments := w.extractAttachments(evt)
	var attachmentsJSON string
	if len(attachments) > 0 {
		attJSON, err := json.Marshal(attachments)
		if err == nil {
			attachmentsJSON = string(attJSON)
			fmt.Printf("WhatsApp: Extracted %d attachments for message %s: %s\n", len(attachments), msgID, attachmentsJSON)
		} else {
			fmt.Printf("WhatsApp: Failed to marshal attachments: %v\n", err)
		}
	} else {
		// Check if message has media but no attachments were extracted
		if msg.GetImageMessage() != nil || msg.GetVideoMessage() != nil || msg.GetAudioMessage() != nil || msg.GetDocumentMessage() != nil || msg.GetStickerMessage() != nil {
			fmt.Printf("WhatsApp: Message %s has media but no attachments were extracted\n", msgID)
		}
	}

	// Get sender avatar URL (only for messages not from me)
	var senderAvatarURL string
	if !isFromMe {
		// First, try to get avatar from cached conversations
		w.mu.RLock()
		if cached, exists := w.conversations[senderID]; exists && cached.AvatarURL != "" {
			senderAvatarURL = cached.AvatarURL
			w.mu.RUnlock()
		} else {
			w.mu.RUnlock()
			// Check if avatar is already being loaded to avoid duplicate requests
			w.avatarLoadingMu.Lock()
			isLoading := w.avatarLoading[senderID]
			if !isLoading {
				w.avatarLoading[senderID] = true
			}
			w.avatarLoadingMu.Unlock()

			// Only load if not already loading
			if !isLoading {
				// If not in cache, try to load it (this will cache it for future use)
				// We do this synchronously for messages as they arrive one by one
				// and we want the avatar to be available immediately
				senderAvatarURL = w.getProfilePictureURL(evt.Info.Sender)
				// If we got an avatar, update the cache
				if senderAvatarURL != "" {
					w.mu.Lock()
					if cached, exists := w.conversations[senderID]; exists {
						cached.AvatarURL = senderAvatarURL
						w.conversations[senderID] = cached
					} else {
						// Create a new entry in cache for this sender
						w.conversations[senderID] = models.LinkedAccount{
							Protocol:  "whatsapp",
							UserID:    senderID,
							Username:  senderName,
							AvatarURL: senderAvatarURL,
							Status:    "offline",
							CreatedAt: timestamp,
							UpdatedAt: timestamp,
						}
					}
					w.mu.Unlock()

					// Persist avatar to database
					if db.DB != nil {
						var existing models.LinkedAccount
						err := db.DB.Where("protocol = ? AND user_id = ?", "whatsapp", senderID).First(&existing).Error
						if err == nil {
							// Update existing
							existing.AvatarURL = senderAvatarURL
							existing.UpdatedAt = timestamp
							if err := db.DB.Save(&existing).Error; err != nil {
								fmt.Printf("WhatsApp: Failed to save sender avatar to database: %v\n", err)
							}
						} else {
							// Create new
							newAccount := models.LinkedAccount{
								Protocol:  "whatsapp",
								UserID:    senderID,
								Username:  senderName,
								AvatarURL: senderAvatarURL,
								Status:    "offline",
								CreatedAt: timestamp,
								UpdatedAt: timestamp,
							}
							if err := db.DB.Create(&newAccount).Error; err != nil {
								fmt.Printf("WhatsApp: Failed to create sender LinkedAccount in database: %v\n", err)
							}
						}
					}
				}

				// Mark as no longer loading
				w.avatarLoadingMu.Lock()
				delete(w.avatarLoading, senderID)
				w.avatarLoadingMu.Unlock()
			}
		}
	}

	return &models.Message{
		ProtocolConvID:  convID,
		ProtocolMsgID:   msgID,
		SenderID:        senderID,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            body,
		Timestamp:       timestamp,
		IsFromMe:        isFromMe,
		Attachments:     attachmentsJSON,
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

	// Persist messages to database
	if db.DB != nil {
		// Store messages in database (upsert by ProtocolMsgID)
		// Note: We store messages with ProtocolConvID, even if Conversation doesn't exist yet
		// This allows us to load messages on startup and filter conversations properly
		for _, msg := range messages {
			if msg.ProtocolMsgID == "" {
				continue
			}
			var existingMsg models.Message
			err := db.DB.Where("protocol_msg_id = ?", msg.ProtocolMsgID).First(&existingMsg).Error
			if err != nil {
				// Message doesn't exist, create it
				// Set ProtocolConvID so we can query by conversation later
				msg.ProtocolConvID = convID
				if err := db.DB.Create(&msg).Error; err != nil {
					fmt.Printf("WhatsApp: Failed to persist message %s: %v\n", msg.ProtocolMsgID, err)
				}
			} else {
				// Message exists, update it if needed
				msg.ID = existingMsg.ID
				msg.ProtocolConvID = convID
				if err := db.DB.Save(&msg).Error; err != nil {
					fmt.Printf("WhatsApp: Failed to update message %s: %v\n", msg.ProtocolMsgID, err)
				}
			}
		}
	}

	return len(dedup)
}

func (w *WhatsAppProvider) appendMessageToConversation(msg *models.Message) {
	if msg == nil {
		return
	}
	w.storeMessagesForConversation(msg.ProtocolConvID, []models.Message{*msg})
}

func (w *WhatsAppProvider) hasConversationHistory(convID string) bool {
	// First check in-memory cache
	w.mu.RLock()
	if len(w.conversationMessages[convID]) > 0 {
		w.mu.RUnlock()
		return true
	}
	w.mu.RUnlock()

	// If not in cache, check database
	if db.DB != nil {
		var count int64
		err := db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ?", convID).
			Count(&count).Error
		if err == nil && count > 0 {
			return true
		}
	}

	return false
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
	fmt.Printf("WhatsApp: GetContacts called\n")
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Check if client is connected (Store.ID is set after successful login)
	if w.client.Store == nil || w.client.Store.ID == nil {
		fmt.Printf("WhatsApp: GetContacts - client not connected, returning empty\n")
		return []models.LinkedAccount{}, nil
	}
	fmt.Printf("WhatsApp: GetContacts - starting to load conversations\n")

	// Start with cached conversations discovered via history sync
	// Make a copy to avoid holding the lock while processing fallback
	w.mu.RLock()
	cachedCount := len(w.conversations)
	cachedConversations := make([]models.LinkedAccount, 0, cachedCount)
	for _, conv := range w.conversations {
		// Make a copy to ensure we have the latest avatar URL
		cachedConversations = append(cachedConversations, conv)
	}
	w.mu.RUnlock()

	linkedAccounts := make([]models.LinkedAccount, 0, cachedCount+32)
	seen := make(map[string]struct{}, cachedCount)
	for _, conv := range cachedConversations {
		linkedAccounts = append(linkedAccounts, conv)
		seen[conv.UserID] = struct{}{}
	}

	// Fall back to the contact store and joined groups
	// Note: getContactsFallback may need to write to w.knownGroups, so we don't hold the lock
	fmt.Printf("WhatsApp: GetContacts - calling getContactsFallback\n")
	fallbackAccounts, err := w.getContactsFallback()
	if err != nil {
		fmt.Printf("WhatsApp: GetContacts - getContactsFallback error: %v\n", err)
		return nil, err
	}
	fmt.Printf("WhatsApp: GetContacts - getContactsFallback returned %d accounts\n", len(fallbackAccounts))

	// Get all avatars from cache in one pass to minimize lock contention
	w.mu.RLock()
	avatarCache := make(map[string]string, len(w.conversations))
	for userID, conv := range w.conversations {
		if conv.AvatarURL != "" {
			avatarCache[userID] = conv.AvatarURL
		}
	}
	w.mu.RUnlock()

	// Merge fallback accounts with cached conversations
	for _, acc := range fallbackAccounts {
		if _, exists := seen[acc.UserID]; exists {
			// If already in cache, check if cache has avatar and fallback doesn't
			// Update the entry in linkedAccounts with avatar from cache if available
			if avatarURL, hasAvatar := avatarCache[acc.UserID]; hasAvatar {
				for i := range linkedAccounts {
					if linkedAccounts[i].UserID == acc.UserID && linkedAccounts[i].AvatarURL == "" {
						linkedAccounts[i].AvatarURL = avatarURL
						break
					}
				}
			}
			continue
		}
		// Check if cache has avatar for this account
		if avatarURL, hasAvatar := avatarCache[acc.UserID]; hasAvatar {
			acc.AvatarURL = avatarURL
		}
		linkedAccounts = append(linkedAccounts, acc)
		seen[acc.UserID] = struct{}{}
	}

	filtered := w.filterAccountsWithHistory(linkedAccounts)
	if len(filtered) > 0 {
		linkedAccounts = filtered
	}

	// Final pass: ensure all avatars from cache are included
	// This ensures that avatars loaded asynchronously are visible
	for i := range linkedAccounts {
		if linkedAccounts[i].AvatarURL == "" {
			if avatarURL, hasAvatar := avatarCache[linkedAccounts[i].UserID]; hasAvatar {
				linkedAccounts[i].AvatarURL = avatarURL
			}
		}
	}

	fmt.Printf("WhatsApp: GetContacts returning %d conversations (%d cached + %d fallback)\n", len(linkedAccounts), cachedCount, len(fallbackAccounts))

	// Load avatars asynchronously in background (non-blocking)
	// Only load if we have accounts that need avatars
	// Use a separate goroutine to avoid blocking
	go func() {
		// Check if any avatars are already being loaded
		w.avatarLoadingMu.Lock()
		hasLoading := len(w.avatarLoading) > 0
		w.avatarLoadingMu.Unlock()

		// Only start loading if not already loading
		if !hasLoading {
			w.loadAvatarsAsync(linkedAccounts)
		}
	}()

	return linkedAccounts, nil
}

// loadAvatarsAsync loads profile pictures for contacts in the background.
// This is non-blocking and doesn't affect the main GetContacts flow.
func (w *WhatsAppProvider) loadAvatarsAsync(accounts []models.LinkedAccount) {
	if w.client == nil {
		return
	}

	// Filter accounts that need avatars
	accountsToLoad := make([]models.LinkedAccount, 0)

	for _, acc := range accounts {
		// Skip groups
		jid, err := types.ParseJID(acc.UserID)
		if err != nil || jid.Server == types.GroupServer {
			continue
		}

		// Check if avatar is already being loaded
		w.avatarLoadingMu.Lock()
		isLoading := w.avatarLoading[acc.UserID]
		w.avatarLoadingMu.Unlock()
		if isLoading {
			continue // Already loading, skip
		}

		// Check if avatar URL is already set and file exists
		if acc.AvatarURL != "" {
			if _, err := os.Stat(acc.AvatarURL); err == nil {
				continue // Avatar file exists, skip
			}
		}

		// Check if avatar is already in cached conversations with a valid path
		w.mu.RLock()
		if cached, exists := w.conversations[acc.UserID]; exists && cached.AvatarURL != "" {
			if _, err := os.Stat(cached.AvatarURL); err == nil {
				w.mu.RUnlock()
				continue // Avatar already cached and file exists, skip
			}
		}
		w.mu.RUnlock()

		accountsToLoad = append(accountsToLoad, acc)
	}

	if len(accountsToLoad) == 0 {
		return
	}

	// Emit start status
	total := len(accountsToLoad)
	w.emitSyncStatus(core.SyncStatusFetchingAvatars, fmt.Sprintf("Loading profile pictures (%d contacts)...", total), 0)

	// Limit concurrent requests to avoid rate limiting
	const maxConcurrent = 5
	sem := make(chan struct{}, maxConcurrent)
	var loaded int
	var progressMu sync.Mutex
	done := make(chan struct{}, 1) // Channel to signal when all are done

	for _, acc := range accountsToLoad {
		jid, _ := types.ParseJID(acc.UserID) // We already validated above

		// Check if avatar is already being loaded to avoid duplicates
		w.avatarLoadingMu.Lock()
		isLoading := w.avatarLoading[acc.UserID]
		if !isLoading {
			w.avatarLoading[acc.UserID] = true
		}
		w.avatarLoadingMu.Unlock()

		if isLoading {
			continue // Skip if already loading
		}

		// Use semaphore to limit concurrent requests
		sem <- struct{}{}
		go func(account models.LinkedAccount, j types.JID) {
			defer func() {
				<-sem
				// Mark as no longer loading
				w.avatarLoadingMu.Lock()
				delete(w.avatarLoading, account.UserID)
				w.avatarLoadingMu.Unlock()
			}()

			avatarURL := w.getProfilePictureURL(j)
			if avatarURL != "" {
				fmt.Printf("WhatsApp: Loaded avatar for %s: %s\n", account.UserID, avatarURL)
				// Update the cached conversation if it exists
				w.mu.Lock()
				if cached, exists := w.conversations[account.UserID]; exists {
					cached.AvatarURL = avatarURL
					w.conversations[account.UserID] = cached
					fmt.Printf("WhatsApp: Updated cached conversation avatar for %s\n", account.UserID)
				} else {
					// Create entry in cache
					w.conversations[account.UserID] = models.LinkedAccount{
						Protocol:  "whatsapp",
						UserID:    account.UserID,
						Username:  account.Username,
						AvatarURL: avatarURL,
						Status:    account.Status,
						CreatedAt: account.CreatedAt,
						UpdatedAt: time.Now(),
					}
					fmt.Printf("WhatsApp: Created cached conversation entry for %s\n", account.UserID)
				}
				w.mu.Unlock()

				// Persist avatar to database
				if db.DB != nil {
					var existing models.LinkedAccount
					err := db.DB.Where("protocol = ? AND user_id = ?", "whatsapp", account.UserID).First(&existing).Error
					if err == nil {
						// Update existing
						existing.AvatarURL = avatarURL
						existing.UpdatedAt = time.Now()
						if err := db.DB.Save(&existing).Error; err != nil {
							fmt.Printf("WhatsApp: Failed to save avatar to database for %s: %v\n", account.UserID, err)
						}
					} else {
						// Create new
						newAccount := models.LinkedAccount{
							Protocol:  "whatsapp",
							UserID:    account.UserID,
							Username:  account.Username,
							AvatarURL: avatarURL,
							Status:    account.Status,
							CreatedAt: account.CreatedAt,
							UpdatedAt: time.Now(),
						}
						if err := db.DB.Create(&newAccount).Error; err != nil {
							fmt.Printf("WhatsApp: Failed to create LinkedAccount in database for %s: %v\n", account.UserID, err)
						}
					}
				}

				// Emit a contact refresh event to update the UI
				select {
				case w.eventChan <- core.ContactStatusEvent{UserID: account.UserID, Status: "avatar_updated"}:
					fmt.Printf("WhatsApp: Emitted avatar_updated event for %s\n", account.UserID)
				default:
					fmt.Printf("WhatsApp: Failed to emit avatar_updated event (channel full)\n")
				}
			} else {
				fmt.Printf("WhatsApp: No avatar available for %s\n", account.UserID)
			}

			// Update progress
			progressMu.Lock()
			loaded++
			currentLoaded := loaded
			isComplete := currentLoaded >= total
			progressMu.Unlock()

			progress := int((float64(currentLoaded) / float64(total)) * 100)
			w.emitSyncStatus(core.SyncStatusFetchingAvatars, fmt.Sprintf("Loading profile pictures (%d/%d)...", currentLoaded, total), progress)

			// If all avatars are loaded, signal completion (only once)
			if isComplete {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		}(acc, jid)
	}

	// Wait for all avatars to be loaded, then emit final completed status
	go func() {
		// Wait for all goroutines to complete
		for i := 0; i < total; i++ {
			<-sem // Wait for each goroutine to release semaphore
		}
		// Emit final completed status
		w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Profile pictures loaded (%d contacts)", total), 100)
	}()
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
	fmt.Printf("WhatsApp: getContactsFallback called\n")
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

		// Don't fetch profile pictures synchronously - it blocks and causes rate limiting
		// Avatars will be loaded lazily when needed or in background
		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    jid.String(),
			Username:  displayName,
			AvatarURL: "", // Will be loaded asynchronously if needed
			Status:    "offline",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	// Add known groups (tracked from messages)
	// Copy the map first to avoid holding the lock during lookupDisplayName
	w.mu.RLock()
	knownGroupsCopy := make(map[string]string, len(w.knownGroups))
	for k, v := range w.knownGroups {
		knownGroupsCopy[k] = v
	}
	w.mu.RUnlock()

	// Now iterate over the copy without holding the lock
	for groupJID, groupName := range knownGroupsCopy {
		displayName := groupName
		if jid, err := types.ParseJID(groupJID); err == nil {
			displayName = w.lookupDisplayName(jid, groupName)
		}
		// Groups don't have profile pictures, so avatarURL is empty
		linkedAccounts = append(linkedAccounts, models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    groupJID,
			Username:  displayName,
			AvatarURL: "",
			Status:    "offline",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	// Try to get groups using GetJoinedGroups if available
	// Only fetch if cache is empty or older than 1 hour to avoid rate limiting
	shouldFetchGroups := false
	w.mu.RLock()
	if w.groupsCacheTimestamp == nil {
		shouldFetchGroups = true
	} else {
		// Refresh cache if older than 1 hour
		if time.Since(*w.groupsCacheTimestamp) > 1*time.Hour {
			shouldFetchGroups = true
		}
	}
	w.mu.RUnlock()

	if shouldFetchGroups && w.client != nil && w.client.Store.ID != nil {
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
				// Groups don't have profile pictures, so avatarURL is empty
				linkedAccounts = append(linkedAccounts, models.LinkedAccount{
					Protocol:  "whatsapp",
					UserID:    groupJID,
					Username:  displayName,
					AvatarURL: "",
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

			// Cache the groups and timestamp
			// Build cache outside the lock first
			groupsToCache := make([]models.LinkedAccount, 0, groupsAdded)
			cacheNow := time.Now()
			for _, acc := range linkedAccounts {
				if jid, err := types.ParseJID(acc.UserID); err == nil && jid.Server == types.GroupServer {
					groupsToCache = append(groupsToCache, acc)
				}
			}
			// Now update the cache with the lock
			w.mu.Lock()
			w.groupsCache = groupsToCache
			w.groupsCacheTimestamp = &cacheNow
			w.mu.Unlock()
		} else if err != nil {
			fmt.Printf("WhatsApp: Could not get groups via GetJoinedGroups: %v\n", err)
		} else {
			fmt.Printf("WhatsApp: No groups found via GetJoinedGroups\n")
		}
	} else if !shouldFetchGroups {
		// Use cached groups - copy first to avoid holding lock
		w.mu.RLock()
		cachedGroupsCopy := make([]models.LinkedAccount, len(w.groupsCache))
		copy(cachedGroupsCopy, w.groupsCache)
		cacheAge := time.Since(*w.groupsCacheTimestamp)
		w.mu.RUnlock()

		if len(cachedGroupsCopy) > 0 {
			fmt.Printf("WhatsApp: Using cached groups (cache age: %v)\n", cacheAge)
			for _, cachedGroup := range cachedGroupsCopy {
				alreadyAdded := false
				for _, acc := range linkedAccounts {
					if acc.UserID == cachedGroup.UserID {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					linkedAccounts = append(linkedAccounts, cachedGroup)
				}
			}
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

		// Don't fetch profile pictures synchronously - it blocks and causes rate limiting
		// Avatars will be loaded lazily when needed or in background
		linked := models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    jid.String(),
			Username:  displayName,
			AvatarURL: "", // Will be loaded asynchronously if needed
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

	// Parse conversation ID to determine if it's a group
	chatJID, err := types.ParseJID(conversationID)
	if err != nil {
		return []models.Message{}, fmt.Errorf("invalid conversation ID: %w", err)
	}
	isGroup := chatJID.Server == types.GroupServer

	// First check in-memory cache
	w.mu.RLock()
	messages, ok := w.conversationMessages[conversationID]
	w.mu.RUnlock()

	if !ok || len(messages) == 0 {
		// If not in cache, try to load from database
		if db.DB != nil {
			var dbMessages []models.Message
			query := db.DB.Where("protocol_conv_id = ?", conversationID).
				Order("timestamp ASC")
			if limit > 0 {
				query = query.Limit(limit)
			}
			if err := query.Find(&dbMessages).Error; err == nil && len(dbMessages) > 0 {
				// Enrich messages with sender names and avatars
				w.enrichMessagesWithSenderInfo(dbMessages, chatJID, isGroup)

				// Load into cache for future use
				w.mu.Lock()
				if w.conversationMessages == nil {
					w.conversationMessages = make(map[string][]models.Message)
				}
				w.conversationMessages[conversationID] = dbMessages
				w.mu.Unlock()
				messages = dbMessages
				ok = true
			}
		}
	} else {
		// Even if in cache, ensure messages are enriched (in case they were loaded before enrichment was added)
		w.enrichMessagesWithSenderInfo(messages, chatJID, isGroup)
	}

	if !ok || len(messages) == 0 {
		return []models.Message{}, nil
	}

	start := 0
	if limit > 0 && len(messages) > limit {
		start = len(messages) - limit
	}

	result := make([]models.Message, len(messages)-start)
	copy(result, messages[start:])
	// Ensure result messages are also enriched
	// Note: enrichMessagesWithSenderInfo may take time if loading avatars,
	// but it should not block indefinitely
	fmt.Printf("WhatsApp: GetConversationHistory: Enriching %d messages for conversation %s\n", len(result), conversationID)
	w.enrichMessagesWithSenderInfo(result, chatJID, isGroup)
	fmt.Printf("WhatsApp: GetConversationHistory: Enriched %d messages, returning\n", len(result))
	return result, nil
}

// enrichMessagesWithSenderInfo enriches messages with sender names and avatars.
func (w *WhatsAppProvider) enrichMessagesWithSenderInfo(messages []models.Message, chatJID types.JID, isGroup bool) {
	if len(messages) == 0 {
		return
	}

	fmt.Printf("WhatsApp: enrichMessagesWithSenderInfo: Processing %d messages\n", len(messages))

	for i := range messages {
		msg := &messages[i]

		// Skip if already enriched
		if msg.SenderName != "" && msg.SenderAvatarURL != "" {
			continue
		}

		// Parse sender JID
		senderJID, err := types.ParseJID(msg.SenderID)
		if err != nil {
			continue
		}

		// Get sender name
		if msg.SenderName == "" {
			if msg.IsFromMe {
				msg.SenderName = "You"
			} else {
				if isGroup {
					msg.SenderName = w.lookupSenderNameInGroup(senderJID, chatJID)
				} else {
					msg.SenderName = w.lookupSenderName(senderJID)
				}
				// Fallback to sender ID if still empty
				if msg.SenderName == "" {
					msg.SenderName = msg.SenderID
				}
			}
		}

		// Get sender avatar URL (only for messages not from me)
		if !msg.IsFromMe && msg.SenderAvatarURL == "" {
			// First, try to get avatar from cached conversations
			w.mu.RLock()
			if cached, exists := w.conversations[msg.SenderID]; exists && cached.AvatarURL != "" {
				msg.SenderAvatarURL = cached.AvatarURL
				w.mu.RUnlock()
			} else {
				w.mu.RUnlock()

				// Check if avatar is already being loaded to avoid duplicate requests
				w.avatarLoadingMu.Lock()
				isLoading := w.avatarLoading[msg.SenderID]
				if !isLoading {
					w.avatarLoading[msg.SenderID] = true
				}
				w.avatarLoadingMu.Unlock()

				// Only try to load if not already loading
				if !isLoading {
					// Try to get avatar URL (this will cache it if found)
					msg.SenderAvatarURL = w.getProfilePictureURL(senderJID)

					// Mark as no longer loading
					w.avatarLoadingMu.Lock()
					delete(w.avatarLoading, msg.SenderID)
					w.avatarLoadingMu.Unlock()

					// If we got an avatar, update the cache
					if msg.SenderAvatarURL != "" {
						w.mu.Lock()
						if cached, exists := w.conversations[msg.SenderID]; exists {
							cached.AvatarURL = msg.SenderAvatarURL
							w.conversations[msg.SenderID] = cached
						} else {
							// Create a new entry in cache for this sender
							w.conversations[msg.SenderID] = models.LinkedAccount{
								Protocol:  "whatsapp",
								UserID:    msg.SenderID,
								Username:  msg.SenderName,
								AvatarURL: msg.SenderAvatarURL,
								Status:    "offline",
								CreatedAt: msg.Timestamp,
								UpdatedAt: msg.Timestamp,
							}
						}
						w.mu.Unlock()
					} else {
						// Avatar not available - mark in cache to avoid repeated attempts
						// Use a special marker to indicate we tried and failed
						w.mu.Lock()
						if cached, exists := w.conversations[msg.SenderID]; exists {
							// Keep existing entry, just mark that we tried
							w.conversations[msg.SenderID] = cached
						} else {
							// Create entry without avatar to mark that we tried
							w.conversations[msg.SenderID] = models.LinkedAccount{
								Protocol:  "whatsapp",
								UserID:    msg.SenderID,
								Username:  msg.SenderName,
								AvatarURL: "", // Empty means we tried and it's not available
								Status:    "offline",
								CreatedAt: msg.Timestamp,
								UpdatedAt: msg.Timestamp,
							}
						}
						w.mu.Unlock()
					}
				}
			}
		}
	}
}

// loadMessagesFromDatabase loads cached messages from the database on startup.
// This version locks the mutex itself.
func (w *WhatsAppProvider) loadMessagesFromDatabase() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadMessagesFromDatabaseLocked()
}

// loadMessagesFromDatabaseLocked loads cached messages from the database.
// This version assumes the mutex is already locked (for use in Init()).
func (w *WhatsAppProvider) loadMessagesFromDatabaseLocked() {
	if db.DB == nil {
		return
	}

	// Load messages grouped by conversation
	var messages []models.Message
	if err := db.DB.Order("protocol_conv_id, timestamp ASC").Find(&messages).Error; err != nil {
		fmt.Printf("WhatsApp: Failed to load messages from database: %v\n", err)
		return
	}

	if len(messages) == 0 {
		// Still try to load conversations even if no messages
		w.loadConversationsFromDatabaseLocked()
		return
	}

	if w.conversationMessages == nil {
		w.conversationMessages = make(map[string][]models.Message)
	}

	// Group messages by conversation ID
	for _, msg := range messages {
		if msg.ProtocolConvID != "" {
			w.conversationMessages[msg.ProtocolConvID] = append(w.conversationMessages[msg.ProtocolConvID], msg)
		}
	}

	// Sort messages within each conversation
	// Note: We don't enrich messages here because enrichMessagesWithSenderInfo
	// needs to lock the mutex, which is already locked. Enrichment will happen
	// lazily when GetConversationHistory is called.
	for convID, convMessages := range w.conversationMessages {
		// Sort messages
		sort.SliceStable(convMessages, func(i, j int) bool {
			return convMessages[i].Timestamp.Before(convMessages[j].Timestamp)
		})
		// Update the cache with sorted messages
		w.conversationMessages[convID] = convMessages
	}

	fmt.Printf("WhatsApp: Loaded %d messages from database across %d conversations\n", len(messages), len(w.conversationMessages))

	// Also load conversations from database
	w.loadConversationsFromDatabaseLocked()
}

// loadConversationsFromDatabaseLocked loads conversations from the database.
// This version assumes the mutex is already locked.
// Note: We don't enrich conversations here because enrichment needs to unlock
// the mutex, which would break the locking contract. Enrichment will happen
// lazily when GetContacts is called.
func (w *WhatsAppProvider) loadConversationsFromDatabaseLocked() {
	if db.DB == nil {
		return
	}

	var linkedAccounts []models.LinkedAccount
	if err := db.DB.Where("protocol = ?", "whatsapp").Find(&linkedAccounts).Error; err == nil {
		// w.mu is already locked, so we can directly access w.conversations
		if w.conversations == nil {
			w.conversations = make(map[string]models.LinkedAccount)
		}
		for _, acc := range linkedAccounts {
			// Verify avatar file exists before using it
			if acc.AvatarURL != "" {
				if _, err := os.Stat(acc.AvatarURL); err != nil {
					// File doesn't exist, clear the avatar URL
					acc.AvatarURL = ""
				}
			}

			// Only add if not already in cache
			if existing, exists := w.conversations[acc.UserID]; !exists {
				w.conversations[acc.UserID] = acc
			} else {
				// Update if existing entry is missing name or avatar
				if existing.Username == "" || existing.Username == existing.UserID {
					existing.Username = acc.Username
				}
				// Only update avatar if the one from DB exists and is valid
				if existing.AvatarURL == "" && acc.AvatarURL != "" {
					existing.AvatarURL = acc.AvatarURL
				} else if existing.AvatarURL != "" && acc.AvatarURL != "" {
					// Both have avatars, prefer the one from DB if file exists
					if _, err := os.Stat(acc.AvatarURL); err == nil {
						existing.AvatarURL = acc.AvatarURL
					}
				}
				w.conversations[acc.UserID] = existing
			}
		}
		fmt.Printf("WhatsApp: Loaded %d conversations from database\n", len(linkedAccounts))
	}
}

// enrichConversationInfo enriches a LinkedAccount with display name and avatar if missing.
// This version assumes the mutex is NOT locked (to avoid deadlocks).
func (w *WhatsAppProvider) enrichConversationInfo(acc models.LinkedAccount) models.LinkedAccount {
	// Parse JID
	jid, err := types.ParseJID(acc.UserID)
	if err != nil {
		return acc
	}

	// Enrich username if missing or just the ID
	if acc.Username == "" || acc.Username == acc.UserID {
		displayName := w.lookupDisplayName(jid, "")
		if displayName != "" && displayName != acc.UserID {
			acc.Username = displayName
		} else if acc.Username == "" {
			acc.Username = acc.UserID
		}
	}

	// Enrich avatar if missing
	if acc.AvatarURL == "" {
		avatarURL := w.getProfilePictureURL(jid)
		if avatarURL != "" {
			acc.AvatarURL = avatarURL
		}
	}

	return acc
}

// SendMessage sends a text message to a given conversation.
func (w *WhatsAppProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
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
	sentMessage := &models.Message{
		ProtocolConvID: conversationID,
		ProtocolMsgID:  resp.ID,
		SenderID:       w.client.Store.ID.String(),
		Body:           text,
		Timestamp:      time.Now(),
		IsFromMe:       true,
	}

	// Store message in conversation cache and database
	w.appendMessageToConversation(sentMessage)

	// Emit MessageEvent to notify frontend
	select {
	case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
		fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent message %s\n", sentMessage.ProtocolMsgID)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent message %s\n", sentMessage.ProtocolMsgID)
	}

	return sentMessage, nil
}

// SendFile sends a file to a given conversation without text.
func (w *WhatsAppProvider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	if file == nil {
		return nil, fmt.Errorf("file is required")
	}

	markUnused(threadID)

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Determine media type and upload with correct type
	var attachmentType string
	var uploadType whatsmeow.MediaType

	mimeType := strings.ToLower(file.MimeType)
	if strings.HasPrefix(mimeType, "image/") {
		uploadType = whatsmeow.MediaImage
		attachmentType = "image"
	} else if strings.HasPrefix(mimeType, "video/") {
		uploadType = whatsmeow.MediaVideo
		attachmentType = "video"
	} else if strings.HasPrefix(mimeType, "audio/") {
		uploadType = whatsmeow.MediaAudio
		attachmentType = "audio"
	} else {
		uploadType = whatsmeow.MediaDocument
		attachmentType = "document"
	}

	// Upload the file
	uploadResp, err := w.client.Upload(w.ctx, file.Data, uploadType)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	// Create message based on media type
	var msg *waE2E.Message
	if attachmentType == "image" {
		msg = &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           &uploadResp.URL,
				Mimetype:      &file.MimeType,
				Caption:       nil,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	} else if attachmentType == "video" {
		msg = &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				URL:           &uploadResp.URL,
				Mimetype:      &file.MimeType,
				Caption:       nil,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	} else if attachmentType == "audio" {
		msg = &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				URL:           &uploadResp.URL,
				Mimetype:      &file.MimeType,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	} else {
		// Send as document
		fileName := file.FileName
		if fileName == "" {
			fileName = "file"
		}
		msg = &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           &uploadResp.URL,
				Mimetype:      &file.MimeType,
				FileName:      &fileName,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	}

	// Send message
	resp, err := w.client.SendMessage(w.ctx, jid, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to send file: %w", err)
	}

	// Cache the file locally
	configDir, err := os.UserConfigDir()
	if err == nil {
		cacheDir := filepath.Join(configDir, "Loom", "whatsapp", "attachments")
		os.MkdirAll(cacheDir, 0700)

		hash := sha256.Sum256([]byte(resp.ID + attachmentType))
		ext := filepath.Ext(file.FileName)
		if ext == "" {
			// Determine extension from mime type
			switch {
			case strings.HasPrefix(mimeType, "image/"):
				ext = ".jpg"
			case strings.HasPrefix(mimeType, "video/"):
				ext = ".mp4"
			case strings.HasPrefix(mimeType, "audio/"):
				ext = ".mp3"
			case mimeType == "application/pdf":
				ext = ".pdf"
			default:
				ext = ".bin"
			}
		}
		filename := hex.EncodeToString(hash[:]) + ext
		cachePath := filepath.Join(cacheDir, filename)

		// Save file to cache
		if err := os.WriteFile(cachePath, file.Data, 0644); err == nil {
			// Create attachment info
			attachment := models.Attachment{
				Type:     attachmentType,
				URL:      cachePath,
				FileName: file.FileName,
				FileSize: int64(file.FileSize),
				MimeType: file.MimeType,
			}

			// Convert to JSON for storage
			attachmentsJSON, _ := json.Marshal([]models.Attachment{attachment})

			// Convert to our Message model
			sentMessage := &models.Message{
				ProtocolConvID: conversationID,
				ProtocolMsgID:  resp.ID,
				SenderID:       w.client.Store.ID.String(),
				Body:           "",
				Attachments:    string(attachmentsJSON),
				Timestamp:      time.Now(),
				IsFromMe:       true,
			}

			// Store message in conversation cache and database
			w.appendMessageToConversation(sentMessage)

			// Emit MessageEvent to notify frontend
			select {
			case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
				fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent file %s\n", sentMessage.ProtocolMsgID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent file %s\n", sentMessage.ProtocolMsgID)
			}

			return sentMessage, nil
		}
	}

	// If caching failed, still return the message without attachment info
	sentMessage := &models.Message{
		ProtocolConvID: conversationID,
		ProtocolMsgID:  resp.ID,
		SenderID:       w.client.Store.ID.String(),
		Body:           "",
		Timestamp:      time.Now(),
		IsFromMe:       true,
	}

	w.appendMessageToConversation(sentMessage)

	select {
	case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
		fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent file %s\n", sentMessage.ProtocolMsgID)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent file %s\n", sentMessage.ProtocolMsgID)
	}

	return sentMessage, nil
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

// MarkMessageAsRead sends a read receipt for a specific message.
func (w *WhatsAppProvider) MarkMessageAsRead(conversationID string, messageID string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	chatJID, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Get message from database to find the sender
	var message models.Message
	if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&message).Error; err != nil {
		fmt.Printf("WhatsApp: Warning - Could not find message %s in database: %v\n", messageID, err)
		// If message not found, use chatJID as participantJID (fallback)
		err = w.client.MarkRead(w.ctx, []types.MessageID{types.MessageID(messageID)}, time.Now(), chatJID, chatJID, types.ReceiptTypeRead)
		if err != nil {
			return fmt.Errorf("failed to send read receipt: %w", err)
		}
		fmt.Printf("WhatsApp: Sent read receipt for message %s in conversation %s (using chatJID as participant)\n", messageID, conversationID)
		return nil
	}

	// Determine participantJID based on message sender
	var participantJID types.JID
	if message.IsFromMe {
		// If message is from me, participantJID should be my own JID
		if w.client.Store != nil && w.client.Store.ID != nil {
			participantJID = *w.client.Store.ID
		} else {
			participantJID = chatJID
		}
	} else {
		// If message is from someone else, participantJID is the sender's JID
		senderJID, err := types.ParseJID(message.SenderID)
		if err != nil {
			fmt.Printf("WhatsApp: Warning - Could not parse sender ID %s: %v, using chatJID\n", message.SenderID, err)
			participantJID = chatJID
		} else {
			participantJID = senderJID
		}
	}

	// Send read receipt using MarkRead method
	// MarkRead signature: (ctx, messageIDs, timestamp, chatJID, participantJID, receiptType...)
	// participantJID is the JID of the person who sent the message
	err = w.client.MarkRead(w.ctx, []types.MessageID{types.MessageID(messageID)}, time.Now(), chatJID, participantJID, types.ReceiptTypeRead)
	if err != nil {
		return fmt.Errorf("failed to send read receipt: %w", err)
	}

	fmt.Printf("WhatsApp: Sent read receipt for message %s in conversation %s (participant: %s)\n", messageID, conversationID, participantJID.String())
	return nil
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

	// Use last sync timestamp if since is zero or very old
	w.mu.RLock()
	lastSync := w.lastSyncTimestamp
	w.mu.RUnlock()

	if since.IsZero() || since.Before(time.Now().Add(-24*time.Hour)) {
		if lastSync != nil {
			since = *lastSync
			fmt.Printf("WhatsApp: Using last sync timestamp: %s\n", since.Format("2006-01-02 15:04:05"))
		} else {
			// First sync - sync from 30 days ago
			since = time.Now().Add(-30 * 24 * time.Hour)
			fmt.Printf("WhatsApp: First sync - syncing from 30 days ago: %s\n", since.Format("2006-01-02 15:04:05"))
		}
	}

	// Emit sync status event
	w.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing history since %s...", since.Format("2006-01-02 15:04:05")), -1)

	fmt.Printf("WhatsApp: SyncHistory called for messages since %s\n", since.Format("2006-01-02 15:04:05"))

	// WhatsApp automatically syncs history when connected via HistorySync events
	// We need to force a refresh of contacts to get the latest conversations
	// The actual message history sync happens automatically through whatsmeow's event system

	// Trigger a refresh of contacts in a goroutine to avoid blocking
	go func() {
		// Wait a bit for whatsmeow to process any pending sync events
		time.Sleep(2 * time.Second)

		w.emitSyncStatus(core.SyncStatusFetchingContacts, "Refreshing conversations...", 90)

		contacts, err := w.GetContacts()
		if err != nil {
			fmt.Printf("WhatsApp: Failed to refresh contacts during sync: %v\n", err)
			w.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to refresh conversations: %v", err), -1)
			return
		}

		fmt.Printf("WhatsApp: Refreshed %d conversations during sync\n", len(contacts))

		// Emit contact refresh event
		select {
		case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
		default:
		}

		// Emit completed status - this is the final event for manual sync
		fmt.Printf("WhatsApp: Emitting completed sync status for manual sync with %d conversations\n", len(contacts))
		w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
		fmt.Printf("WhatsApp: Completed sync status emitted for manual sync\n")
	}()

	return nil
}
