package teams

import (
	"encoding/base64"
	"testing"

	"Loom/pkg/models"
)

func TestTeamsReplyPreview(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"attachments":[{"contentType":"application/vnd.microsoft.card.adaptive","content":{"type":"AdaptiveCard","body":[{"type":"TextBlock","text":"Deployment complete"}]}}]}`))
	for _, tc := range []struct{ name, body, want string }{
		{"markdown", "**Hello**", "**Hello**"},
		{"html", "<p><strong>Hello</strong></p>", "**Hello**"},
		{"raw card", `<URIObject type="SWIFT.1" url_thumbnail="">payload</URIObject>`, "Carte"},
		{"legacy markdown card", `&#x20;[URIObject type="SWIFT.1" url\_thumbnail="`, "Carte"},
		{"escaped card", `&lt;URIObject type=&quot;SWIFT.1&quot;&gt;`, "Carte"},
		{"readable card", `<URIObject type="SWIFT.1"><Swift b64="` + payload + `"></Swift></URIObject>`, "Deployment complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := teamsReplyPreview(tc.body); got != tc.want {
				t.Fatalf("preview = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTeamsReplyMetadataNormalizesCardParent(t *testing.T) {
	parentID := "parent"
	messages := []models.Message{
		{ProtocolMsgID: parentID, Body: `[URIObject type="SWIFT.1" url\_thumbnail="`},
		{ProtocolMsgID: "reply", QuotedMessageID: &parentID},
	}
	NewProvider().enrichReplyMetadata(messages)
	if got := messages[1].QuotedBody; got == nil || *got != "Carte" {
		t.Fatalf("quoted body = %v", got)
	}
}
