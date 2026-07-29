package googlemessages

import (
	"Loom/pkg/core"
	"testing"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func TestGoogleMessagesReceipts(t *testing.T) {
	timestamp := time.Unix(123, 0)
	tests := []struct {
		name       string
		status     gmproto.MessageStatusType
		wantType   core.ReceiptType
		wantLength int
	}{
		{name: "sent", status: gmproto.MessageStatusType_OUTGOING_COMPLETE, wantLength: 0},
		{name: "delivered", status: gmproto.MessageStatusType_OUTGOING_DELIVERED, wantType: core.ReceiptTypeDelivery, wantLength: 1},
		{name: "read", status: gmproto.MessageStatusType_OUTGOING_DISPLAYED, wantType: core.ReceiptTypeRead, wantLength: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipts := googleMessagesReceipts(test.status, "conversation-1", timestamp)
			if len(receipts) != test.wantLength {
				t.Fatalf("got %d receipts, want %d", len(receipts), test.wantLength)
			}
			if test.wantLength == 0 {
				return
			}
			if receipts[0].ReceiptType != string(test.wantType) {
				t.Errorf("got receipt type %q, want %q", receipts[0].ReceiptType, test.wantType)
			}
			if receipts[0].UserID != "conversation-1" {
				t.Errorf("got user ID %q", receipts[0].UserID)
			}
			if !receipts[0].Timestamp.Equal(timestamp) {
				t.Errorf("got timestamp %v, want %v", receipts[0].Timestamp, timestamp)
			}
		})
	}
}
