package main

import (
	"Loom/pkg/core"
	"testing"
)

func TestIsFunctionalLiveEvent(t *testing.T) {
	tests := []struct {
		name  string
		event core.ProviderEvent
		want  bool
	}{
		{name: "live message", event: core.MessageEvent{}, want: true},
		{name: "live batch", event: core.MessageBatchEvent{IsHistorical: false}, want: true},
		{name: "historical batch", event: core.MessageBatchEvent{IsHistorical: true}, want: false},
		{name: "typing", event: core.TypingEvent{}, want: true},
		{name: "contact refresh", event: core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}, want: false},
		{name: "avatar sync", event: core.ContactStatusEvent{UserID: "person", Status: "avatar_updated"}, want: false},
		{name: "live contact state", event: core.ContactStatusEvent{UserID: "person", Status: "online"}, want: true},
		{name: "sync state", event: core.SyncStatusEvent{}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isFunctionalLiveEvent(test.event); got != test.want {
				t.Fatalf("isFunctionalLiveEvent() = %v, want %v", got, test.want)
			}
		})
	}
}
