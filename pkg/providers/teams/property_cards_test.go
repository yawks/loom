package teams

import (
	"encoding/json"
	"strings"
	"testing"

	"Loom/pkg/models"
	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func TestPropertyAdaptiveCardKeepsLayoutAndCompanionMessage(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// Shape of the issue-preview cards from November 9: the card lives in
	// JSON-encoded properties.cards, not in a SWIFT attachment.
	card := map[string]any{
		"type": "AdaptiveCard", "version": "1.5",
		"body": []any{
			map[string]any{"type": "ColumnSet", "columns": []any{
				map[string]any{"type": "Column", "width": "34px", "items": []any{
					map[string]any{"type": "Image", "url": "https://example.com/bug.png", "width": "30px", "height": "30px"},
				}},
				map[string]any{"type": "Column", "width": "stretch", "items": []any{
					map[string]any{"type": "TextBlock", "text": "[BUILD-6317: Authentication](https://example.com/BUILD-6317)", "weight": "bolder"},
					map[string]any{"type": "TextBlock", "text": "Assigned to Minh PHAM", "isSubtle": true},
				}},
			}},
			map[string]any{"type": "ColumnSet", "columns": []any{
				map[string]any{"type": "Column", "width": "auto", "items": []any{map[string]any{"type": "TextBlock", "text": "**High**"}}},
				map[string]any{"type": "Column", "width": "47px", "items": []any{map[string]any{"type": "TextBlock", "text": "**16 NOV**"}}},
				map[string]any{"type": "Column", "width": "auto", "items": []any{map[string]any{"type": "TextBlock", "text": "**IN PROGRESS**", "color": "accent"}}},
			}},
			map[string]any{"type": "ActionSet", "actions": []any{
				map[string]any{"type": "Action.Submit", "title": "Comment", "data": map[string]any{"command": "comment"}},
			}},
		},
	}
	for _, contentAsString := range []bool{false, true} {
		var content any = card
		if contentAsString {
			data, _ := json.Marshal(card)
			content = string(data)
		}
		wrapper := map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "content": content, "appName": "Jira", "appIcon": "https://example.com/app.png"}
		properties, _ := json.Marshal([]any{wrapper})
		for _, body := range []string{"<p>Bonjour, voici le ticket.</p>", ""} {
			message := NewProvider().toModelMessage(client, msteams.Message{
				ID: "issue-card", Content: body, ContentType: "html", MessageType: "RichText/Html",
				Properties: map[string]any{"cards": string(properties)},
			}, "room")
			var attachments []models.Attachment
			if err := json.Unmarshal([]byte(message.Attachments), &attachments); err != nil || len(attachments) != 1 {
				t.Fatalf("attachments = %s, %v", message.Attachments, err)
			}
			attachment := attachments[0]
			if attachment.Type != "adaptive_card" || attachment.FileName != "Jira" || attachment.Thumbnail != "https://example.com/app.png" {
				t.Fatalf("card metadata lost: %+v", attachment)
			}
			var restored map[string]any
			if err := json.Unmarshal([]byte(attachment.CardJSON), &restored); err != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(card)
			if string(original) != attachment.CardJSON {
				t.Fatalf("structured card changed: %s", attachment.CardJSON)
			}
			if body != "" {
				if message.Body != "Bonjour, voici le ticket." || !attachment.IsSupplementary {
					t.Fatalf("companion message was replaced/duplicated: %+v", message)
				}
			} else if !strings.Contains(message.Body, "BUILD-6317") || attachment.IsSupplementary {
				t.Fatal("standalone card must retain its searchable fallback")
			}
		}
	}
}
