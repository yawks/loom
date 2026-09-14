package teams

import (
	"Loom/pkg/core"
	"testing"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func TestLiveCardMessageNormalizesBodyBeforeEmission(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, tc := range []struct {
		name, content, want string
		properties          map[string]any
	}{
		{"escaped wrapper", `&lt;URIObject type="SWIFT.1" url_thumbnail=""&gt;`, "Carte", nil},
		{"markdown wrapper", `[URIObject type="SWIFT.1" url\_thumbnail="`, "Carte", nil},
		{"empty raw card", `<URIObject type="SWIFT.1"></URIObject>`, "Carte", nil},
		{"available card", `[URIObject type="SWIFT.1" url\_thumbnail="`, "New issue created in Jira", map[string]any{
			"cards": map[string]any{"type": "AdaptiveCard", "body": []any{map[string]any{"type": "TextBlock", "text": "New issue created in Jira"}}},
		}},
		{"ordinary markdown", "**Hello**", "**Hello**", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewProvider()
			provider.instance = "teams-work"
			provider.handleRemoteEvent(client, msteams.Event{
				Type: msteams.EventTypeNewMessage, ThreadID: "19:room@thread.v2",
				Message: &msteams.Message{ID: "live-card", From: "8:orgid:self", Content: tc.content,
					ContentType: "text", MessageType: "RichText/Media_Card", Properties: tc.properties},
			})
			select {
			case emitted := <-provider.eventChan:
				event, ok := emitted.(core.MessageEvent)
				if !ok {
					t.Fatalf("unexpected event %T", emitted)
				}
				if event.Message.Body != tc.want {
					t.Fatalf("body = %q, want %q", event.Message.Body, tc.want)
				}
				if event.InstanceID != "teams-work" || event.Message.ProtocolConvID != "teams-work::19:room@thread.v2" {
					t.Fatalf("incorrect event ownership: %+v", event)
				}
			default:
				t.Fatal("no message event emitted")
			}
		})
	}
}
