package slack

import (
	"Loom/pkg/models"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestConvertMessageMarksDirectMentionOfSelf(t *testing.T) {
	provider := NewSlackProvider()
	provider.selfUserID = "USELF"
	message := provider.convertMessage(goslack.Message{
		Msg: goslack.Msg{Timestamp: "1700000000.000001", User: "UOTHER", Text: "hello <@USELF>"},
	}, "C1")
	if len(message.HighlightReasons) != 1 || message.HighlightReasons[0] != models.HighlightReasonDirectMention {
		t.Fatalf("unexpected highlight reasons: %v", message.HighlightReasons)
	}
}
