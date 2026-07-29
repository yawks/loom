package core

import (
	"encoding/json"
	"testing"
)

func TestTypingEventJSONContract(t *testing.T) {
	event := TypingEvent{
		InstanceID:     "whatsapp-1",
		ConversationID: "group@g.us",
		UserID:         "user@s.whatsapp.net",
		UserName:       "Alice",
		IsTyping:       true,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"instanceId", "conversationId", "userId", "userName", "isTyping"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("typing event JSON is missing %q: %s", key, data)
		}
	}
}
