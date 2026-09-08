package core

import (
	"Loom/pkg/models"
	"testing"
)

func TestValidateProviderEventOwnership(t *testing.T) {
	tests := []struct {
		name  string
		event ProviderEvent
		valid bool
	}{
		{name: "matching message", event: MessageEvent{InstanceID: "slack-1", Message: models.Message{ProtocolConvID: "slack-1::C1"}}, valid: true},
		{name: "raw conversation remains compatible", event: ReactionEvent{InstanceID: "slack-1", ConversationID: "C1"}, valid: true},
		{name: "wrong event instance", event: PresenceEvent{InstanceID: "teams-1"}},
		{name: "wrong conversation owner", event: MessageEvent{InstanceID: "slack-1", Message: models.Message{ProtocolConvID: "whatsapp-1::C1"}}},
		{name: "wrong nested batch owner", event: MessageBatchEvent{InstanceID: "slack-1", ConversationID: "slack-1::C1", Messages: []models.Message{{ProtocolConvID: "teams-1::C1"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProviderEventOwnership("slack-1", test.event)
			if (err == nil) != test.valid {
				t.Fatalf("error = %v, want valid=%v", err, test.valid)
			}
		})
	}
}
