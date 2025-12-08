package whatsapp

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"net/http"
	"path/filepath"
)

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
