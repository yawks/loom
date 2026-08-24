package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateInstanceIDSkipsOrphanedLocalState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("get config directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "Loom", "whatsapp-1"), 0o700); err != nil {
		t.Fatalf("create orphaned instance directory: %v", err)
	}

	pm := NewProviderManager()
	if got := pm.generateInstanceID("whatsapp"); got != "whatsapp-2" {
		t.Fatalf("generateInstanceID() = %q, want %q", got, "whatsapp-2")
	}
}
