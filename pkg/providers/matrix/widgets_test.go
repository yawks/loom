package matrix

import (
	"encoding/json"
	"testing"
)

func stateKey(value string) *string { return &value }

func TestNormalizeWidgetEventsInitialUpdateRemovalAndDeduplication(t *testing.T) {
	events := []matrixEvent{
		{Type: "im.vector.modular.widgets", StateKey: stateKey("litefeed"), Content: json.RawMessage(`{"name":"Old","url":"https://old.example/widget"}`)},
		{Type: "m.widget", StateKey: stateKey("litefeed"), Content: json.RawMessage(`{"type":"m.custom","name":"Litefeed","url":"https://bot.example.org/?token=SECRET"}`)},
		{Type: "m.widget", StateKey: stateKey("removed"), Content: json.RawMessage(`{}`)},
	}
	applications := normalizeWidgetEvents(events)
	if len(applications) != 1 {
		t.Fatalf("applications = %+v", applications)
	}
	if applications[0].ID != "litefeed" || applications[0].Name != "Litefeed" || applications[0].Origin != "https://bot.example.org" || applications[0].LaunchURL != "https://bot.example.org/?token=SECRET" {
		t.Fatalf("application = %+v", applications[0])
	}
}

func TestNormalizeWidgetEventsRejectsUnsafeURLs(t *testing.T) {
	events := []matrixEvent{{Type: "m.widget", StateKey: stateKey("bad"), Content: json.RawMessage(`{"url":"javascript:alert(1)"}`)}}
	if applications := normalizeWidgetEvents(events); len(applications) != 0 {
		t.Fatalf("applications = %+v", applications)
	}
}
