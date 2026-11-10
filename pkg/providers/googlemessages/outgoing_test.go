package googlemessages

import (
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func TestMessageConfirmationLinksLocalEcho(t *testing.T) {
	p := &Provider{instance: "googlemessages-1"}
	remote := &gmproto.Message{MessageID: "9876", ConversationID: "1491", TmpID: "send-uuid"}
	message := p.toModelMessage(remote, "", "")
	if message.ProtocolMsgID != "9876" || message.ProtocolConvID != "googlemessages-1::1491" {
		t.Fatalf("canonical identity lost: %+v", message)
	}
	if message.ReplacesMessageID != "temp-send-uuid" {
		t.Fatalf("replacement ID = %q", message.ReplacesMessageID)
	}
	remote.TmpID = ""
	if message := p.toModelMessage(remote, "", ""); message.ReplacesMessageID != "" {
		t.Fatalf("message without a send token replaces %q", message.ReplacesMessageID)
	}
}
