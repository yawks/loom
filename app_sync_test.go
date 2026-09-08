package main

import (
	"context"
	"reflect"
	"testing"
)

func TestGetSyncingProviderIDsReturnsSortedSnapshot(t *testing.T) {
	app := NewApp()
	app.syncInProgress["teams-2"] = true
	app.syncInProgress["slack-1"] = true
	app.syncInProgress["ignored"] = false
	app.syncingProviders = map[string]bool{
		"whatsapp-pro": true,
		"teams-2":      true,
		"completed":    false,
	}

	got := app.GetSyncingProviderIDs()
	want := []string{"slack-1", "teams-2", "whatsapp-pro"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetSyncingProviderIDs() = %v, want %v", got, want)
	}
}

func TestForceSyncCompletionCancelsRegisteredSync(t *testing.T) {
	app := NewApp()
	syncCtx, cancel := context.WithCancel(context.Background())
	app.syncCancels["slack-1"] = cancel

	app.ForceSyncCompletion("slack-1")

	if syncCtx.Err() != context.Canceled {
		t.Fatalf("sync context error = %v, want context.Canceled", syncCtx.Err())
	}
}
