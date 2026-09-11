// Package screenprotection controls capture exclusion for Loom's main window.
// Exclusion is window-wide and depends on the capturing application's OS APIs.
package screenprotection

import "sync"

var windowMu sync.Mutex
var windowProtection protectionState

// Loom creates its single main window with ContentProtection disabled. Track
// native mutations for the process lifetime (including renderer reloads), so
// the initial disable does not require an already-visible native window.
type protectionState struct {
	possiblyEnabled bool
}

func (s *protectionState) apply(enabled bool, nativeSet func(bool) error) error {
	if !enabled && !s.possiblyEnabled {
		return nil
	}
	// A failed native call may have changed the window before verification
	// failed. Keep requiring a real disable until it succeeds.
	s.possiblyEnabled = true
	if err := nativeSet(enabled); err != nil {
		return err
	}
	s.possiblyEnabled = enabled
	return nil
}

// Set waits until the native change has completed. Callers must hide sensitive
// content until it succeeds, and remove it before disabling protection.
func Set(enabled bool) error {
	windowMu.Lock()
	defer windowMu.Unlock()
	return windowProtection.apply(enabled, set)
}
