package main

import (
	"testing"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func reactionActivityDB(t *testing.T) (*gorm.DB, models.Message) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.ReactionActivity{}, &models.Reaction{}, &models.LinkedAccount{}, &models.ParticipantProfile{}, &models.ContactAlias{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	message := models.Message{ProtocolConvID: "one::chat", ProtocolMsgID: "sent", SenderID: "self", IsFromMe: true, Timestamp: time.Unix(100, 0)}
	if err := database.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	return database, message
}

func TestReactionActivityLifecycleAndSnapshotAcknowledgement(t *testing.T) {
	database, message := reactionActivityDB(t)
	app := &App{}
	event := core.ReactionEvent{InstanceID: "one", ConversationID: message.ProtocolConvID, MessageID: message.ProtocolMsgID, UserID: "alice", Emoji: "👍", Added: true, Timestamp: 200}
	persist := func(e core.ReactionEvent) {
		t.Helper()
		if err := persistReactionActivity(database, message, e, "self"); err != nil {
			t.Fatal(err)
		}
	}
	summary := func() models.ReactionActivitySummary {
		t.Helper()
		rows, err := app.GetReactionActivitySummaries()
		if err != nil {
			t.Fatal(err)
		}
		return rows[message.ProtocolConvID]
	}
	// Providers can persist their reaction snapshot before emitting the event.
	if err := database.Create(&models.Reaction{MessageID: message.ID, UserID: "alice", Emoji: "👍"}).Error; err != nil {
		t.Fatal(err)
	}
	persist(event)
	persist(event)
	first := summary()
	if first.UnreadCount != 1 || first.Latest.Emoji != "👍" || first.Latest.ProtocolMessageID != "sent" {
		t.Fatalf("unexpected initial summary: %+v", first)
	}
	event.UserID, event.Emoji, event.Timestamp = "bob", "❤️", 201
	persist(event)
	acknowledged, err := app.MarkReactionActivitiesSeen(message.ProtocolConvID, first.ThroughID)
	if err != nil {
		t.Fatal(err)
	}
	if len(acknowledged) != 1 || acknowledged[0].UserID != "alice" || acknowledged[0].Emoji != "👍" {
		t.Fatalf("halo should target only the acknowledged snapshot: %+v", acknowledged)
	}
	if repeated, err := app.MarkReactionActivitiesSeen(message.ProtocolConvID, first.ThroughID); err != nil || len(repeated) != 0 {
		t.Fatalf("acknowledgement replay must not restart halos: %+v, %v", repeated, err)
	}
	if got := summary(); got.UnreadCount != 1 || got.Latest.UserID != "bob" {
		t.Fatalf("acknowledging a snapshot consumed newer activity: %+v", got)
	}
	if _, err := app.MarkReactionActivitiesSeen(message.ProtocolConvID, summary().ThroughID); err != nil {
		t.Fatal(err)
	}
	persist(event)
	if got := summary(); got.UnreadCount != 0 {
		t.Fatalf("duplicate resurrected read activity: %+v", got)
	}
	event.Added, event.Emoji = false, "" // canonical removal with no emoji
	persist(event)
	if got := summary(); got.Latest.UserID != "alice" || got.UnreadCount != 0 {
		t.Fatalf("removal did not restore previous preview: %+v", got)
	}
	event.Added, event.Emoji = true, "❤️"
	persist(event) // replay of removed event must stay removed
	if got := summary(); got.Latest.UserID != "alice" {
		t.Fatalf("replay restored a removed reaction: %+v", got)
	}
	event.Timestamp++
	persist(event)
	if got := summary(); got.UnreadCount != 1 || got.Latest.UserID != "bob" {
		t.Fatalf("a genuine re-add should create attention: %+v", got)
	}
}

func TestReactionActivityIgnoresOwnIncomingAndHistoricalReactions(t *testing.T) {
	database, message := reactionActivityDB(t)
	base := core.ReactionEvent{InstanceID: "one", ConversationID: message.ProtocolConvID, MessageID: message.ProtocolMsgID, UserID: "alice", Emoji: "👍", Added: true, Timestamp: 200}
	for _, tc := range []struct {
		name   string
		change func(*models.Message, *core.ReactionEvent)
	}{
		{"own", func(_ *models.Message, e *core.ReactionEvent) { e.UserID = "SELF" }},
		{"incoming target", func(m *models.Message, _ *core.ReactionEvent) { m.IsFromMe = false }},
		{"history", func(_ *models.Message, e *core.ReactionEvent) { e.Historical = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, e := message, base
			tc.change(&m, &e)
			if err := persistReactionActivity(database, m, e, "self"); err != nil {
				t.Fatal(err)
			}
		})
	}
	var count int64
	database.Model(&models.ReactionActivity{}).Count(&count)
	if count != 0 {
		t.Fatalf("ignored events created %d activities", count)
	}
	base.InstanceID = ""
	if err := persistReactionActivity(database, message, base, "self"); err == nil {
		t.Fatal("empty provider ownership must fail closed")
	}
}

func TestReactionActivityNamesAndReadStateAreIsolatedByInstance(t *testing.T) {
	database, firstMessage := reactionActivityDB(t)
	secondMessage := models.Message{ProtocolConvID: "two::chat", ProtocolMsgID: "sent-two", IsFromMe: true, SenderID: "self"}
	if err := database.Create(&secondMessage).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		instance, name string
		message        models.Message
	}{
		{"one", "Alice One", firstMessage}, {"two", "Alice Two", secondMessage},
	} {
		if err := database.Create(&models.ParticipantProfile{ProviderInstanceID: tc.instance, UserID: "alice", DisplayName: tc.name}).Error; err != nil {
			t.Fatal(err)
		}
		event := core.ReactionEvent{InstanceID: tc.instance, ConversationID: tc.message.ProtocolConvID, MessageID: tc.message.ProtocolMsgID, UserID: "alice", Emoji: "👍", Added: true, Timestamp: 200}
		if err := persistReactionActivity(database, tc.message, event, "self"); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{}
	rows, err := app.GetReactionActivitySummaries()
	if err != nil {
		t.Fatal(err)
	}
	if rows["one::chat"].Latest.UserName != "Alice One" || rows["two::chat"].Latest.UserName != "Alice Two" {
		t.Fatalf("names leaked across instances: %+v", rows)
	}
	if _, err := app.MarkReactionActivitiesSeen("one::chat", rows["two::chat"].ThroughID); err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetReactionActivitySummaries()
	if err != nil || rows["one::chat"].UnreadCount != 0 || rows["two::chat"].UnreadCount != 1 {
		t.Fatalf("read state leaked across instances: %+v, err %v", rows, err)
	}
	if err := database.Delete(&secondMessage).Error; err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetReactionActivitySummaries()
	if err != nil || len(rows) != 1 {
		t.Fatalf("deleted message should hide its activities: %+v, err %v", rows, err)
	}
}
