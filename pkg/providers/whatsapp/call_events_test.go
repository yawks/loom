package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
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

func TestCompletedCallDurationFallsBackToObservedTime(t *testing.T) {
	serverTimestamp := time.Date(2026, time.September, 10, 13, 7, 13, 0, time.UTC)
	observedAccept := serverTimestamp.Add(250 * time.Millisecond)
	duration := completedCallDuration(&activeCallInfo{
		AcceptTime:         serverTimestamp,
		AcceptObservedTime: observedAccept,
	}, serverTimestamp, observedAccept.Add(8*time.Second))

	if duration == nil || *duration != 8 {
		t.Fatalf("observed duration = %v, want 8 seconds", duration)
	}
}

func TestCompletedCallDurationDoesNotPersistFalseZero(t *testing.T) {
	serverTimestamp := time.Date(2026, time.September, 10, 13, 7, 13, 0, time.UTC)
	duration := completedCallDuration(&activeCallInfo{
		AcceptTime:         serverTimestamp,
		AcceptObservedTime: serverTimestamp,
	}, serverTimestamp, serverTimestamp.Add(500*time.Millisecond))

	if duration != nil {
		t.Fatalf("sub-second duration = %v, want nil", duration)
	}
}

func TestMarkCallAcceptedKeepsEarliestSignal(t *testing.T) {
	provider := NewWhatsAppProvider()
	callID := "EARLIEST_ACCEPT"
	provider.activeCalls[callID] = &activeCallInfo{}
	earlyServer := time.Date(2026, time.September, 10, 13, 7, 13, 0, time.UTC)
	earlyObserved := earlyServer.Add(200 * time.Millisecond)
	provider.markCallAccepted(callID, earlyServer, earlyObserved)
	provider.markCallAccepted(callID, earlyServer.Add(8*time.Second), earlyObserved.Add(8*time.Second))

	info := provider.activeCalls[callID]
	if !info.AcceptTime.Equal(earlyServer) || !info.AcceptObservedTime.Equal(earlyObserved) {
		t.Fatalf("accept signal was overwritten: server=%v observed=%v", info.AcceptTime, info.AcceptObservedTime)
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
	durationSeconds := int64(17)
	startSeconds := callStart.Unix()
	callID := "OFFLINE_CALL"
	creator := "33617590388@s.whatsapp.net"
	result := waSyncAction.CallLogRecord_ACCEPTEDELSEWHERE
	callType := waSyncAction.CallLogRecord_REGULAR
	isIncoming := true

	provider.eventHandler(&events.AppState{SyncActionValue: &waSyncAction.SyncActionValue{
		CallLogAction: &waSyncAction.CallLogAction{CallLogRecord: &waSyncAction.CallLogRecord{
			CallResult:     &result,
			Duration:       &durationSeconds,
			StartTime:      &startSeconds,
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

func TestAppStateOutgoingCallUsesRemoteParticipantConversation(t *testing.T) {
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
	startSeconds := time.Date(2026, time.August, 23, 21, 23, 20, 0, time.UTC).Unix()
	durationSeconds := int64(17)
	callID := "OUTGOING_OFFLINE_CALL"
	creator := "33677815440@s.whatsapp.net"
	remote := "33688856629@s.whatsapp.net"
	result := waSyncAction.CallLogRecord_CONNECTED
	isIncoming := false

	provider.eventHandler(&events.AppState{SyncActionValue: &waSyncAction.SyncActionValue{
		CallLogAction: &waSyncAction.CallLogAction{CallLogRecord: &waSyncAction.CallLogRecord{
			CallResult:     &result,
			Duration:       &durationSeconds,
			StartTime:      &startSeconds,
			IsIncoming:     &isIncoming,
			CallID:         &callID,
			CallCreatorJID: &creator,
			Participants: []*waSyncAction.CallLogRecord_ParticipantInfo{{
				UserJID: &remote,
			}},
		}},
	}})

	var message models.Message
	if err := db.DB.Where("protocol_msg_id LIKE ?", "call_OUTGOING_OFFLINE_CALL%").First(&message).Error; err != nil {
		t.Fatal(err)
	}
	wantConversation := "whatsapp-test::33688856629@s.whatsapp.net"
	if message.ProtocolConvID != wantConversation {
		t.Errorf("outgoing call conversation = %q, want %q", message.ProtocolConvID, wantConversation)
	}
	if message.CallType != "outgoing_voice" || !message.IsFromMe {
		t.Errorf("outgoing call = type %q, isFromMe %v", message.CallType, message.IsFromMe)
	}
	if message.CallDurationSecs == nil || *message.CallDurationSecs != 17 {
		t.Errorf("outgoing call duration = %v, want 17", message.CallDurationSecs)
	}
}

func TestConvertHistoryCallLogMessage(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config = core.ProviderConfig{"_instance_id": "whatsapp-test"}
	contact, _ := types.ParseJID("33688856629@s.whatsapp.net")
	startedAt := time.Date(2026, time.August, 23, 9, 15, 0, 0, time.UTC)
	outcome := waE2E.CallLogMessage_ACCEPTED_ELSEWHERE
	duration := int64(42)
	isVideo := false

	message := provider.convertMessage(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: contact, Sender: contact},
			ID:            "OFFLINE_HISTORY_CALL",
			Timestamp:     startedAt,
		},
		Message: &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{
			CallOutcome:  &outcome,
			DurationSecs: &duration,
			IsVideo:      &isVideo,
		}},
	})

	if message == nil {
		t.Fatal("expected call log message to be converted")
	}
	if message.CallType != "incoming_call" || message.CallOutcome != "CONNECTED" {
		t.Errorf("converted call = type %q, outcome %q", message.CallType, message.CallOutcome)
	}
	if message.CallDurationSecs == nil || *message.CallDurationSecs != 42 {
		t.Errorf("converted duration = %v, want 42", message.CallDurationSecs)
	}
	if !message.Timestamp.Equal(startedAt) {
		t.Errorf("converted timestamp = %v, want %v", message.Timestamp, startedAt)
	}
}

func TestCallAcceptedElsewhereDoesNotMeasureCompanionTermination(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_accept", true: "delayed_termination"}[accepted], func(t *testing.T) {
			previousDB := db.DB
			db.DB = nil
			t.Cleanup(func() { db.DB = previousDB })
			provider := NewWhatsAppProvider()
			provider.config["_instance_id"] = "whatsapp-test"
			caller := types.NewJID("33123456789", types.DefaultUserServer)
			start := time.Date(2026, time.September, 12, 12, 43, 24, 0, time.UTC)
			meta := types.BasicCallMeta{CallCreator: caller, CallID: "HANDOFF", Timestamp: start}
			provider.eventHandler(&events.CallOffer{BasicCallMeta: meta})
			if accepted {
				meta.Timestamp = start.Add(4 * time.Second)
				provider.eventHandler(&events.CallAccept{BasicCallMeta: meta})
			}
			meta.Timestamp = start.Add(9 * time.Second)
			provider.eventHandler(&events.CallTerminate{BasicCallMeta: meta, Reason: "accepted_elsewhere"})
			messages := provider.conversationMessages[core.BuildConvID("whatsapp-test", caller.String())]
			if len(messages) != 1 {
				t.Fatalf("got %d messages, want one", len(messages))
			}
			message := messages[0]
			if message.CallOutcome != "CONNECTED" || message.CallType != "incoming_call" {
				t.Fatalf("handoff = %s/%s, want connected incoming call", message.CallType, message.CallOutcome)
			}
			if message.CallDurationSecs != nil {
				t.Fatalf("handoff duration = %v, want unknown", *message.CallDurationSecs)
			}
		})
	}
}

func TestDuplicateCallSummaryPersistsDurationAndIsolatesProvider(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-test"
	caller := types.NewJID("33123456789", types.DefaultUserServer)
	convID := core.BuildConvID("whatsapp-test", caller.String())
	rows := []models.Message{
		{ProtocolMsgID: "SUMMARY", ProtocolConvID: convID, CallType: "incoming_call", CallOutcome: "CONNECTED", Timestamp: time.Now()},
		{ProtocolMsgID: "OTHER_SUMMARY", ProtocolConvID: core.BuildConvID("whatsapp-other", caller.String()), CallType: "incoming_call", CallOutcome: "CONNECTED", Timestamp: time.Now()},
	}
	if err := db.Transaction(database, func(tx *gorm.DB) error {
		copyRows := append([]models.Message(nil), rows...)
		return tx.Create(&copyRows).Error
	}); err != nil {
		t.Fatal(err)
	}
	provider.conversationMessages[convID] = []models.Message{rows[0]}
	duration := int64(6)
	outcome := waE2E.CallLogMessage_CONNECTED
	event := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: caller, Sender: caller}, ID: "SUMMARY", Timestamp: rows[0].Timestamp},
		Message: &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{DurationSecs: &duration, CallOutcome: &outcome}},
	}
	provider.eventHandler(event)
	// An older summary without a duration must not erase the completed result.
	event.Message.CallLogMesssage.DurationSecs = nil
	provider.eventHandler(event)
	var stored models.Message
	if err := database.Where("protocol_conv_id = ?", convID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CallDurationSecs == nil || *stored.CallDurationSecs != 6 {
		t.Fatalf("duration = %v, want 6", stored.CallDurationSecs)
	}
	if stored.IsEdited {
		t.Fatal("summary must not mark the call edited")
	}
	var other models.Message
	if err := database.Where("protocol_conv_id = ?", rows[1].ProtocolConvID).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	if other.CallDurationSecs != nil {
		t.Fatal("other provider's call was modified")
	}
}

func TestCallHandoffThenAppStateSummaryUpdatesMessageAndDuration(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-test"
	caller := types.NewJID("33612345678", types.DefaultUserServer)
	callID := "CALL_HANDOFF_123"
	start := time.Date(2026, time.September, 12, 14, 0, 0, 0, time.UTC)
	meta := types.BasicCallMeta{CallCreator: caller, CallID: callID, Timestamp: start}

	// 1. CallOffer: ringing on desktop
	provider.eventHandler(&events.CallOffer{BasicCallMeta: meta})

	// 2. CallTerminate: answered on mobile (reason: accepted_elsewhere)
	meta.Timestamp = start.Add(2 * time.Second)
	provider.eventHandler(&events.CallTerminate{BasicCallMeta: meta, Reason: "accepted_elsewhere"})

	var stored models.Message
	convID := core.BuildConvID("whatsapp-test", caller.String())
	if err := database.Where("protocol_conv_id = ?", convID).First(&stored).Error; err != nil {
		t.Fatalf("call message was not found: %v", err)
	}
	if stored.CallOutcome != "CONNECTED" {
		t.Fatalf("call outcome = %q, want CONNECTED", stored.CallOutcome)
	}
	if stored.CallDurationSecs != nil {
		t.Fatalf("initial duration = %v, want nil before summary", *stored.CallDurationSecs)
	}

	// 3. Mobile phone finishes call and uploads AppState CallLogRecord
	durationSeconds := int64(142)
	startSeconds := start.Unix()
	callerStr := caller.String()
	result := waSyncAction.CallLogRecord_CONNECTED
	callType := waSyncAction.CallLogRecord_REGULAR
	isIncoming := true

	provider.eventHandler(&events.AppState{SyncActionValue: &waSyncAction.SyncActionValue{
		CallLogAction: &waSyncAction.CallLogAction{CallLogRecord: &waSyncAction.CallLogRecord{
			CallResult:     &result,
			Duration:       &durationSeconds,
			StartTime:      &startSeconds,
			IsIncoming:     &isIncoming,
			CallID:         &callID,
			CallCreatorJID: &callerStr,
			CallType:       &callType,
		}},
	}})

	if err := database.Where("protocol_conv_id = ?", convID).First(&stored).Error; err != nil {
		t.Fatalf("call message was not found after summary: %v", err)
	}
	if stored.CallDurationSecs == nil || *stored.CallDurationSecs != 142 {
		t.Fatalf("updated duration = %v, want 142", stored.CallDurationSecs)
	}
	if stored.CallOutcome != "CONNECTED" {
		t.Fatalf("updated outcome = %q, want CONNECTED", stored.CallOutcome)
	}
}

