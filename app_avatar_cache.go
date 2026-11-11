package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"Loom/pkg/models"
)

type avatarReferenceCache struct {
	mu          sync.Mutex
	directory   string
	paths       map[string]string
	sourceBytes int
}

// compactAvatarReference preserves the existing local-path/GetAvatar contract.
// Legacy persisted inline images are detected by format, never provider ID.
// Store full-resolution bytes once; bridge snapshots then carry only a stable
// path, and the image is loaded only when a corresponding avatar is displayed.
func (cache *avatarReferenceCache) compact(source string) string {
	if !strings.HasPrefix(source, "data:image/") {
		return source
	}
	header, encoded, ok := strings.Cut(source, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return source
	}
	extension := map[string]string{
		"data:image/png;base64": ".png", "data:image/jpeg;base64": ".jpg",
		"data:image/gif;base64": ".gif", "data:image/webp;base64": ".webp",
	}[header]
	if extension == "" {
		return source
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if path := cache.paths[source]; path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return source
	}
	directory := cache.directory
	if directory == "" {
		root, err := os.UserCacheDir()
		if err != nil {
			return source
		}
		directory = filepath.Join(root, "Loom", "inline-avatars")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return source
	}
	path := filepath.Join(directory, fmt.Sprintf("%x%s", sha256.Sum256(data), extension))
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(data)) {
		// Atomic publication also repairs files left incomplete by an old crash.
		file, err := os.CreateTemp(directory, ".avatar-*")
		if err != nil {
			return source
		}
		temporary := file.Name()
		defer os.Remove(temporary)
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return source
		}
		if err := os.Rename(temporary, path); err != nil {
			return source
		}
	}
	// String keys reference existing immutable data without copying/hashing it
	// into a temporary byte buffer on every contact refresh. Bound retention of
	// obsolete sources; clearing the lookup is safe (files are content-addressed).
	const maxSourceBytes = 16 * 1024 * 1024
	if len(source) > maxSourceBytes {
		return path
	}
	if cache.paths == nil || len(cache.paths) >= 512 || cache.sourceBytes+len(source) > maxSourceBytes {
		cache.paths = make(map[string]string)
		cache.sourceBytes = 0
	}
	if _, exists := cache.paths[source]; !exists {
		cache.sourceBytes += len(source)
	}
	cache.paths[source] = path
	return path
}

func (a *App) compactContactAvatars(contacts []models.MetaContact) {
	for i := range contacts {
		contacts[i].AvatarURL = a.avatarReferences.compact(contacts[i].AvatarURL)
		for j := range contacts[i].LinkedAccounts {
			account := &contacts[i].LinkedAccounts[j]
			account.AvatarURL = a.avatarReferences.compact(account.AvatarURL)
		}
	}
}
