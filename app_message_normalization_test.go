package main

import (
	"Loom/pkg/models"
	"strings"
	"testing"
)

func TestCanonicalizeAttachmentTypesRepairsLegacyAdaptiveCard(t *testing.T) {
	message := models.Message{Attachments: `[{"type":"teams_card","cardJson":"{}"}]`}

	canonicalizeAttachmentTypes(&message)

	if !strings.Contains(message.Attachments, `"type":"adaptive_card"`) {
		t.Fatalf("attachment was not canonicalized: %s", message.Attachments)
	}
	if strings.Contains(message.Attachments, `"type":"teams_card"`) {
		t.Fatalf("legacy attachment type leaked through: %s", message.Attachments)
	}
}

func TestCanonicalizeAttachmentTypesLeavesMalformedPayloadUntouched(t *testing.T) {
	message := models.Message{Attachments: `not-json`}
	canonicalizeAttachmentTypes(&message)
	if message.Attachments != `not-json` {
		t.Fatalf("malformed attachment payload changed: %s", message.Attachments)
	}
}
