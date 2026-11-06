package whatsapp

import (
	"testing"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"

	"github.com/glebarez/sqlite"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

func historyReactionProvider(t *testing.T, resumed bool) (*WhatsAppProvider, *gorm.DB, models.Message) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}, &models.MessageReceipt{}, &models.ReactionActivity{}, &models.ProviderConfiguration{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	message := models.Message{ProtocolConvID: "wa-work::chat@s.whatsapp.net", ProtocolMsgID: "sent", SenderID: "self@s.whatsapp.net", IsFromMe: true, Timestamp: time.Unix(100, 0)}
	other := models.Message{ProtocolConvID: "wa-home::chat@s.whatsapp.net", ProtocolMsgID: "other", SenderID: "self@s.whatsapp.net", IsFromMe: true}
	if err := database.Create(&[]models.Message{message, other}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("protocol_msg_id = ?", "sent").First(&message).Error; err != nil {
		t.Fatal(err)
	}
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "wa-work"
	if resumed {
		cutoff := time.Unix(200, 0)
		if err := database.Create(&models.ProviderConfiguration{InstanceID: "wa-work", LastSyncAt: &cutoff}).Error; err != nil {
			t.Fatal(err)
		}
	}
	provider.loadLastSyncTimestamp()
	return provider, database, message
}

func TestRecoveredWhatsAppReactionCreatesUnreadActivityAtItsOriginalTime(t *testing.T) {
	provider, database, message := historyReactionProvider(t, true)
	// Saving another history chunk must not move the catch-up boundary. This
	// reaction happened after the startup watermark but before the new one.
	provider.saveLastSyncTimestamp(time.Unix(400, 0))
	reaction := core.ReactionEvent{InstanceID: "wa-work", ConversationID: message.ProtocolConvID, MessageID: "sent", UserID: "alice@s.whatsapp.net", Emoji: "👍", Added: true, Historical: true, Timestamp: 250}
	if err := provider.storeHistoryReactionBatch(message.ProtocolConvID, []core.ReactionEvent{reaction}); err != nil {
		t.Fatal(err)
	}
	var activity models.ReactionActivity
	if err := database.First(&activity).Error; err != nil {
		t.Fatal(err)
	}
	if activity.Seen || !activity.Active || activity.Timestamp.Unix() != 250 || activity.ProtocolMessageID != "sent" {
		t.Fatalf("wrong recovered attention: %+v", activity)
	}
	batch, ok := (<-provider.eventChan).(core.ReactionBatchEvent)
	if !ok || len(batch.Reactions) != 1 || batch.Reactions[0].Historical || batch.Reactions[0].Timestamp != 250 {
		t.Fatalf("wrong recovered event: %+v", batch)
	}
	// Consumption followed by replay, even after a restart with an overlapping
	// recovery window, must not create another unread activity.
	if err := database.Model(&activity).Update("seen", true).Error; err != nil {
		t.Fatal(err)
	}
	provider.loadLastSyncTimestamp()
	if err := provider.storeHistoryReactionBatch(message.ProtocolConvID, []core.ReactionEvent{reaction}); err != nil {
		t.Fatal(err)
	}
	var unread int64
	database.Model(&models.ReactionActivity{}).Where("seen = ?", false).Count(&unread)
	var total int64
	database.Model(&models.ReactionActivity{}).Count(&total)
	if unread != 0 || total != 1 {
		t.Fatalf("history replay resurrected attention: unread=%d total=%d", unread, total)
	}
	var otherActivities int64
	database.Model(&models.ReactionActivity{}).Where("provider_instance_id = ?", "wa-home").Count(&otherActivities)
	if otherActivities != 0 {
		t.Fatal("recovery created attention for another provider")
	}
}

func TestHistorySyncWithOnlyStandaloneReactionRecoversAttention(t *testing.T) {
	provider, database, _ := historyReactionProvider(t, true)
	self := types.NewJID("self", types.DefaultUserServer)
	provider.client = whatsmeow.NewClient(&store.Device{ID: &self}, nil)
	history := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_RECENT.Enum(),
		Conversations: []*waHistorySync.Conversation{{
			ID: proto.String("chat@s.whatsapp.net"),
			Messages: []*waHistorySync.HistorySyncMsg{{Message: &waWeb.WebMessageInfo{
				Key:              &waProto.MessageKey{ID: proto.String("offline-reaction"), RemoteJID: proto.String("chat@s.whatsapp.net"), FromMe: proto.Bool(false)},
				MessageTimestamp: proto.Uint64(400), // delivery time differs from the reaction's sender time
				Message: &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
					Key:  &waProto.MessageKey{ID: proto.String("sent"), FromMe: proto.Bool(true)},
					Text: proto.String("👍"), SenderTimestampMS: proto.Int64(250000),
				}},
			}}},
		}},
	}
	if !provider.cacheMessagesFromHistory(history) {
		t.Fatal("history reaction batch was not persisted")
	}
	var activities []models.ReactionActivity
	if err := database.Find(&activities).Error; err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Seen || activities[0].Timestamp.Unix() != 250 || activities[0].UserID != "chat@s.whatsapp.net" {
		t.Fatalf("standalone offline reaction was not recovered: %+v", activities)
	}
	var messages int64
	database.Model(&models.Message{}).Count(&messages)
	if messages != 2 {
		t.Fatal("reaction envelope was persisted as a standalone chat message")
	}
	batch, ok := (<-provider.eventChan).(core.ReactionBatchEvent)
	if !ok || batch.ConversationID != "wa-work::chat@s.whatsapp.net" || batch.Reactions[0].Historical {
		t.Fatalf("wrong recovered batch: %+v", batch)
	}
}

func TestHistoryReactionSnapshotsAlsoRecoverOfflineAttention(t *testing.T) {
	provider, database, message := historyReactionProvider(t, true)
	snapshot := provider.convertHistoryReactions([]*waProto.Reaction{{
		Key:  &waProto.MessageKey{Participant: proto.String("alice@s.whatsapp.net")},
		Text: proto.String("❤️"), SenderTimestampMS: proto.Int64(250000),
	}}, "sent")
	if len(snapshot) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	// The target message store may already have saved the snapshot. It must not
	// suppress the separate attention activity.
	row := snapshot[0]
	row.MessageID = message.ID
	if err := database.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	event := core.ReactionEvent{InstanceID: "wa-work", ConversationID: message.ProtocolConvID, MessageID: "sent", UserID: row.UserID, Emoji: row.Emoji, Added: true, Timestamp: row.CreatedAt.Unix()}
	if err := provider.storeHistoryReactionBatch(message.ProtocolConvID, []core.ReactionEvent{event, event}); err != nil {
		t.Fatal(err)
	}
	var count int64
	database.Model(&models.ReactionActivity{}).Where("active = ? AND seen = ?", true, false).Count(&count)
	if count != 1 {
		t.Fatalf("snapshot should create exactly one unread activity, got %d", count)
	}
	database.Model(&models.Reaction{}).Count(&count)
	if count != 1 {
		t.Fatalf("snapshot duplicated persisted reactions: %d", count)
	}
}

func TestInitialAndOldHistoryReactionsDoNotCreateAttention(t *testing.T) {
	for _, tc := range []struct {
		name              string
		resumed, backfill bool
		timestamp         int64
	}{
		{"initial import", false, false, 250},
		{"old reaction", true, false, 150},
		{"undated reaction", true, false, 0},
		{"legacy repair", true, true, 250},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, database, message := historyReactionProvider(t, tc.resumed)
			provider.historyBackfills[message.ProtocolConvID] = tc.backfill
			event := core.ReactionEvent{InstanceID: "wa-work", ConversationID: message.ProtocolConvID, MessageID: "sent", UserID: "alice@s.whatsapp.net", Emoji: "👍", Added: true, Timestamp: tc.timestamp}
			if err := provider.storeHistoryReactionBatch(message.ProtocolConvID, []core.ReactionEvent{event}); err != nil {
				t.Fatal(err)
			}
			var count int64
			database.Model(&models.ReactionActivity{}).Count(&count)
			if count != 0 {
				t.Fatalf("historical import created %d activities", count)
			}
			database.Model(&models.Reaction{}).Count(&count)
			if count != 1 {
				t.Fatal("historical reaction was not recovered on the message")
			}
			batch := (<-provider.eventChan).(core.ReactionBatchEvent)
			if !batch.Reactions[0].Historical {
				t.Fatal("historical reaction was announced as new")
			}
		})
	}
}

func TestRecoveredSnapshotRemovalDismissesOldActivity(t *testing.T) {
	provider, database, message := historyReactionProvider(t, true)
	addition := core.ReactionEvent{InstanceID: "wa-work", ConversationID: message.ProtocolConvID, MessageID: "sent", UserID: "alice@s.whatsapp.net", Emoji: "👍", Added: true, Timestamp: 250}
	if err := provider.storeHistoryReactionBatch(message.ProtocolConvID, []core.ReactionEvent{addition}); err != nil {
		t.Fatal(err)
	}
	rows, changes := provider.historyReactionSnapshot([]*waProto.Reaction{{
		Key:  &waProto.MessageKey{Participant: proto.String("alice@s.whatsapp.net")},
		Text: proto.String(""), SenderTimestampMS: proto.Int64(300000),
	}}, "sent", message.ProtocolConvID)
	if len(rows) != 0 || len(changes) != 1 || changes[0].Added {
		t.Fatalf("removal snapshot was not normalized: %+v, %+v", rows, changes)
	}
	if err := provider.storeHistoryReactionBatch(message.ProtocolConvID, changes); err != nil {
		t.Fatal(err)
	}
	var count int64
	database.Model(&models.Reaction{}).Count(&count)
	if count != 0 {
		t.Fatal("snapshot removal left a visible reaction")
	}
	database.Model(&models.ReactionActivity{}).Where("active = ?", true).Count(&count)
	if count != 0 {
		t.Fatal("snapshot removal left unread attention")
	}
}
