package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	waSyncAction "go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"gorm.io/gorm"
)

func TestWhatsAppCallEventFlowConnected(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"

	callerJID, _ := types.ParseJID("33617590388@s.whatsapp.net")
	callID := "TEST_CALL_12345"
	callStart := time.Date(2026, time.August, 21, 20, 7, 0, 0, time.Local)
	callAccept := callStart.Add(2 * time.Second)
	callEnd := callAccept.Add(17 * time.Second)

	// 1. CallOffer event
	offerEvt := &events.CallOffer{
		BasicCallMeta: types.BasicCallMeta{
			CallCreator: callerJID,
			CallID:      callID,
			Timestamp:   callStart,
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
	if !info.StartTime.Equal(callStart) {
		t.Fatalf("Expected call start %v, got %v", callStart, info.StartTime)
	}
	if info.CallMessage == nil || !info.CallMessage.Timestamp.Equal(callStart) {
		t.Fatalf("Expected message timestamp to use the offer time %v, got %#v", callStart, info.CallMessage)
	}

	// 2. CallAccept event
	acceptEvt := &events.CallAccept{
		BasicCallMeta: types.BasicCallMeta{
			CallCreator: callerJID,
			CallID:      callID,
			Timestamp:   callAccept,
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
			Timestamp:   callEnd,
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

	provider.mu.RLock()
	messages := provider.conversationMessages[info.CallMessage.ProtocolConvID]
	provider.mu.RUnlock()
	if len(messages) != 1 {
		t.Fatalf("Expected one call message, got %d", len(messages))
	}
	message := messages[0]
	if !message.Timestamp.Equal(callStart) {
		t.Errorf("Expected completed call to keep start timestamp %v, got %v", callStart, message.Timestamp)
	}
	if message.CallOutcome != "CONNECTED" {
		t.Errorf("Expected CONNECTED outcome, got %q", message.CallOutcome)
	}
	if message.CallDurationSecs == nil || *message.CallDurationSecs != 17 {
		t.Errorf("Expected 17 second duration, got %v", message.CallDurationSecs)
	}
}

func TestCallLogOutcomeAcceptedElsewhereIsConnected(t *testing.T) {
	duration := int32(17)
	if outcome := callLogOutcome("ACCEPTEDELSEWHERE", &duration); outcome != "CONNECTED" {
		t.Fatalf("Expected linked-device answer to be CONNECTED, got %q", outcome)
	}
}

func TestAppStateCallLogCreatesOfflineCallSummary(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := db.DB.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	provider := NewWhatsAppProvider()
	provider.config = core.ProviderConfig{"_instance_id": "whatsapp-test"}
	callStart := time.Date(2026, time.August, 21, 20, 7, 0, 0, time.UTC)
	durationMillis := int64(17_000)
	startMillis := callStart.UnixMilli()
	callID := "OFFLINE_CALL"
	creator := "33617590388@s.whatsapp.net"
	result := waSyncAction.CallLogRecord_ACCEPTEDELSEWHERE
	callType := waSyncAction.CallLogRecord_REGULAR
	isIncoming := true

	provider.eventHandler(&events.AppState{SyncActionValue: &waSyncAction.SyncActionValue{
		CallLogAction: &waSyncAction.CallLogAction{CallLogRecord: &waSyncAction.CallLogRecord{
			CallResult:     &result,
			Duration:       &durationMillis,
			StartTime:      &startMillis,
			IsIncoming:     &isIncoming,
			CallID:         &callID,
			CallCreatorJID: &creator,
			CallType:       &callType,
		}},
	}})

	var message models.Message
	if err := db.DB.Where("protocol_msg_id LIKE ?", "call_OFFLINE_CALL%").First(&message).Error; err != nil {
		t.Fatal(err)
	}
	if !message.Timestamp.Equal(callStart) {
		t.Errorf("offline call timestamp = %v, want %v", message.Timestamp, callStart)
	}
	if message.CallOutcome != "CONNECTED" {
		t.Errorf("offline call outcome = %q, want CONNECTED", message.CallOutcome)
	}
	if message.CallDurationSecs == nil || *message.CallDurationSecs != 17 {
		t.Errorf("offline call duration = %v, want 17", message.CallDurationSecs)
	}
}
