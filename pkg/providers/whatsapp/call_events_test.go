package whatsapp

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestWhatsAppCallEventFlowConnected(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"

	callerJID, _ := types.ParseJID("33617590388@s.whatsapp.net")
	callID := "TEST_CALL_12345"

	// 1. CallOffer event
	offerEvt := &events.CallOffer{
		BasicCallMeta: types.BasicCallMeta{
			CallCreator: callerJID,
			CallID:      callID,
			Timestamp:   time.Now().Add(-10 * time.Second),
		},
	}
	provider.eventHandler(offerEvt)

	// Check active calls map
	provider.activeCallsMu.RLock()
	info, active := provider.activeCalls[callID]
	provider.activeCallsMu.RUnlock()

	if !active {
		t.Fatalf("Expected call %s to be tracked in activeCalls", callID)
	}
	if info.IsAccepted {
		t.Fatalf("Expected call to not be accepted yet")
	}

	// 2. CallAccept event
	acceptEvt := &events.CallAccept{
		BasicCallMeta: types.BasicCallMeta{
			CallCreator: callerJID,
			CallID:      callID,
			Timestamp:   time.Now().Add(-8 * time.Second),
		},
	}
	provider.eventHandler(acceptEvt)

	provider.activeCallsMu.RLock()
	info, active = provider.activeCalls[callID]
	provider.activeCallsMu.RUnlock()

	if !active || !info.IsAccepted {
		t.Fatalf("Expected call to be marked as accepted")
	}

	// 3. CallTerminate event
	termEvt := &events.CallTerminate{
		BasicCallMeta: types.BasicCallMeta{
			CallCreator: callerJID,
			CallID:      callID,
			Timestamp:   time.Now(),
		},
	}
	provider.eventHandler(termEvt)

	// Check active calls cleaned up
	provider.activeCallsMu.RLock()
	_, active = provider.activeCalls[callID]
	provider.activeCallsMu.RUnlock()

	if active {
		t.Errorf("Expected activeCall for %s to be cleaned up after CallTerminate", callID)
	}
}
