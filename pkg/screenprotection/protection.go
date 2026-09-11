// Package screenprotection controls capture exclusion for Loom's main window.
// Exclusion is window-wide and depends on the capturing application's OS APIs.
package screenprotection

import "sync"

var windowMu sync.Mutex

// Set waits until the native change has completed. Callers must hide sensitive
// content until it succeeds, and remove it before disabling protection.
func Set(enabled bool) error {
	windowMu.Lock()
	defer windowMu.Unlock()
	return set(enabled)
}
