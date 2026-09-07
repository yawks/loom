package whatsapp

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"Loom/pkg/models"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"
)

func TestCanonicalWhatsAppPollSupportsCurrentVariants(t *testing.T) {
	creation := &waE2E.PollCreationMessage{
		Name:                   proto.String("Lunch?"),
		Options:                []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Pizza")}, {OptionName: proto.String("Sushi")}},
		SelectableOptionsCount: proto.Uint32(1),
	}
	for name, message := range map[string]*waE2E.Message{
		"v1": {PollCreationMessage: creation},
		"v2": {PollCreationMessageV2: creation},
		"v3": {PollCreationMessageV3: creation},
		"v4": {PollCreationMessageV4: &waE2E.FutureProofMessage{Message: &waE2E.Message{PollCreationMessageV3: creation}}},
		"v5": {PollCreationMessageV5: creation},
		"v6": {PollCreationMessageV6: creation},
	} {
		t.Run(name, func(t *testing.T) {
			poll := canonicalWhatsAppPoll(message)
			if poll == nil || poll.Question != "Lunch?" || poll.MaxSelections != 1 || len(poll.Options) != 2 {
				t.Fatalf("unexpected canonical poll: %+v", poll)
			}
			hash := sha256.Sum256([]byte("Pizza"))
			if poll.Options[0].ID != hex.EncodeToString(hash[:]) {
				t.Fatalf("option ID is not the WhatsApp option hash")
			}
		})
	}
}

func TestReconcileLegacyEmptyMessageWithPoll(t *testing.T) {
	existing := &models.Message{ProtocolMsgID: "poll-1"}
	incoming := &models.Message{
		ProtocolMsgID:         "poll-1",
		Body:                  "Lunch?",
		Poll:                  &models.Poll{Question: "Lunch?", Options: []models.PollOption{{ID: "pizza", Text: "Pizza"}}},
		PollTransportSenderID: "123@lid",
	}
	reconcileDuplicateMessage(existing, incoming)
	if existing.Poll == nil || existing.Poll.Question != "Lunch?" || existing.Body != "Lunch?" {
		t.Fatalf("legacy message was not enriched: %+v", existing)
	}
	if existing.PollTransportSenderID != "123@lid" {
		t.Fatalf("poll transport sender was not retained: %q", existing.PollTransportSenderID)
	}
}

func TestWhatsAppAdvertisesPollVoting(t *testing.T) {
	if !NewWhatsAppProvider().GetCapabilities().SupportsPollVoting {
		t.Fatal("WhatsApp must advertise poll voting")
	}
}

func TestCanonicalWhatsAppPollResultSnapshot(t *testing.T) {
	message := &waE2E.Message{PollResultSnapshotMessageV3: &waE2E.PollResultSnapshotMessage{
		Name: proto.String("Lunch?"),
		PollVotes: []*waE2E.PollResultSnapshotMessage_PollVote{
			{OptionName: proto.String("Pizza"), OptionVoteCount: proto.Int64(3)},
			{OptionName: proto.String("Sushi"), OptionVoteCount: proto.Int64(2)},
		},
	}}
	poll := canonicalWhatsAppPoll(message)
	if poll == nil || poll.Question != "Lunch?" || !poll.Closed || len(poll.Options) != 2 || poll.Options[0].Votes != 3 {
		t.Fatalf("unexpected snapshot poll: %+v", poll)
	}
}

func TestLegacyMessageBackfillDetector(t *testing.T) {
	base := time.Date(2026, time.September, 6, 19, 6, 0, 0, time.Local)
	messages := []models.Message{
		{ProtocolMsgID: "text", Body: "hello", Timestamp: base},
		{ProtocolMsgID: "empty-1", Timestamp: base.Add(time.Second)},
		{ProtocolMsgID: "empty-2", Timestamp: base.Add(15 * time.Second)},
		{ProtocolMsgID: "empty-3", Timestamp: base.Add(50 * time.Second)},
	}
	count, needed := legacyMessageBackfillState(messages)
	if count != 3 || !needed {
		t.Fatalf("count=%d needed=%v", count, needed)
	}
	messages[2].Poll = &models.Poll{Question: "Already canonical"}
	if _, needed = legacyMessageBackfillState(messages); needed {
		t.Fatal("two legacy empty rows must not trigger a backfill")
	}
	messages = append(messages, models.Message{ProtocolMsgID: "empty-4", Timestamp: base.Add(70 * time.Second)})
	messages[2].Poll.TotalVoters = 1
	if count, needed = legacyMessageBackfillState(messages); count != 3 || !needed {
		t.Fatalf("persisted live votes must not suppress historical recovery: count=%d needed=%v", count, needed)
	}

	spreadOut := []models.Message{
		{ProtocolMsgID: "media-1", Timestamp: base},
		{ProtocolMsgID: "media-2", Timestamp: base.Add(24 * time.Hour)},
		{ProtocolMsgID: "media-3", Timestamp: base.Add(48 * time.Hour)},
	}
	if count, needed = legacyMessageBackfillState(spreadOut); count != 3 || needed {
		t.Fatalf("spread-out empty messages must not trigger a backfill: count=%d needed=%v", count, needed)
	}
}

func TestApplyHistoricalPollUpdates(t *testing.T) {
	provider := NewWhatsAppProvider()
	pizzaHash := sha256.Sum256([]byte("Pizza"))
	sushiHash := sha256.Sum256([]byte("Sushi"))
	pizzaID := hex.EncodeToString(pizzaHash[:])
	sushiID := hex.EncodeToString(sushiHash[:])
	message := &models.Message{Poll: &models.Poll{Options: []models.PollOption{{ID: pizzaID, Text: "Pizza"}, {ID: sushiID, Text: "Sushi"}}}}
	provider.applyHistoricalPollUpdates(message, []*waWeb.PollUpdate{
		{PollUpdateMessageKey: &waCommon.MessageKey{Participant: proto.String("33611111111@s.whatsapp.net")}, Vote: &waE2E.PollVoteMessage{SelectedOptions: [][]byte{pizzaHash[:]}}},
		{PollUpdateMessageKey: &waCommon.MessageKey{Participant: proto.String("33622222222@s.whatsapp.net")}, Vote: &waE2E.PollVoteMessage{SelectedOptions: [][]byte{sushiHash[:]}}},
	})
	if message.Poll.TotalVoters != 2 || message.Poll.Options[0].Votes != 1 || message.Poll.Options[1].Votes != 1 {
		t.Fatalf("unexpected historical poll aggregation: %+v", message.Poll)
	}
}

func TestApplyPollSnapshotFromWebMessageInfo(t *testing.T) {
	provider := NewWhatsAppProvider()
	optionHash := sha256.Sum256([]byte("Oui"))
	message := &models.Message{Poll: &models.Poll{Options: []models.PollOption{{ID: hex.EncodeToString(optionHash[:]), Text: "Oui"}}}}
	webMessage := &waWeb.WebMessageInfo{PollUpdates: []*waWeb.PollUpdate{{
		PollUpdateMessageKey: &waCommon.MessageKey{Participant: proto.String("33611111111@s.whatsapp.net")},
		Vote:                 &waE2E.PollVoteMessage{SelectedOptions: [][]byte{optionHash[:]}},
	}}}

	if count := provider.applyPollSnapshot(message, webMessage); count != 1 {
		t.Fatalf("snapshot update count=%d", count)
	}
	if message.Poll.TotalVoters != 1 || message.Poll.Options[0].Votes != 1 {
		t.Fatalf("snapshot was not applied: %+v", message.Poll)
	}
}

func TestLegacyHistoryAnchorSkipsEmptyPollVoteRows(t *testing.T) {
	base := time.Date(2026, time.September, 6, 19, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ProtocolMsgID: "poll", Timestamp: base, Poll: &models.Poll{Question: "Question"}},
		{ProtocolMsgID: "answer", Timestamp: base.Add(time.Minute), Body: "Message after poll"},
		{ProtocolMsgID: "encrypted-vote-1", Timestamp: base.Add(2 * time.Minute)},
		{ProtocolMsgID: "encrypted-vote-2", Timestamp: base.Add(3 * time.Minute)},
	}

	anchor, ok := legacyHistoryAnchor(messages)
	if !ok || anchor.ProtocolMsgID != "answer" {
		t.Fatalf("unexpected history anchor: ok=%v message=%+v", ok, anchor)
	}
}
