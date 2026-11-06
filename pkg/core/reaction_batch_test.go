package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"Loom/pkg/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func reactionBatchDB(t *testing.T) (*gorm.DB, ReactionBatchEvent) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}, &models.ReactionActivity{}, &models.MessageReceipt{}); err != nil {
		t.Fatal(err)
	}
	rows := []models.Message{
		{ProtocolConvID: "one::chat", ProtocolMsgID: "sent", IsFromMe: true, SenderID: "self"},
		{ProtocolConvID: "two::chat", ProtocolMsgID: "other", IsFromMe: true, SenderID: "self"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	batch := ReactionBatchEvent{InstanceID: "one", ConversationID: "one::chat", Reactions: []ReactionEvent{{InstanceID: "one", ConversationID: "one::chat", MessageID: "sent", UserID: "alice", Emoji: "👍", Added: true, Timestamp: 200}}}
	return database, batch
}

func TestReactionBatchIsAtomicAndScoped(t *testing.T) {
	database, batch := reactionBatchDB(t)
	if err := database.Callback().Create().Before("gorm:create").Register("test:fail_activity", func(tx *gorm.DB) {
		if tx.Statement.Table == "reaction_activities" {
			tx.AddError(errors.New("activity write failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := StoreReactionBatch(database, batch, "self"); err == nil {
		t.Fatal("expected a persistence failure")
	}
	for _, model := range []any{&models.Reaction{}, &models.MessageReceipt{}, &models.ReactionActivity{}} {
		var count int64
		database.Model(model).Count(&count)
		if count != 0 {
			t.Fatalf("%T escaped the rolled-back batch", model)
		}
	}
	if err := database.Callback().Create().Remove("test:fail_activity"); err != nil {
		t.Fatal(err)
	}
	// Even a remote message ID which exists in a different instance is not work
	// for this batch. Capture loaded Message rows as well as persisted results.
	if err := database.Callback().Query().After("gorm:query").Register("test:check_loaded_scope", func(tx *gorm.DB) {
		if messages, ok := tx.Statement.Dest.(*[]models.Message); ok {
			for _, message := range *messages {
				if message.ProtocolConvID != "one::chat" {
					t.Errorf("loaded another provider's row: %+v", message)
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	foreign := batch.Reactions[0]
	foreign.MessageID = "other"
	batch.Reactions = append(batch.Reactions, foreign)
	if err := StoreReactionBatch(database, batch, "self"); err != nil {
		t.Fatal(err)
	}
	var activities []models.ReactionActivity
	database.Find(&activities)
	if len(activities) != 1 || activities[0].ConversationID != "one::chat" {
		t.Fatalf("batch crossed instance boundary: %+v", activities)
	}
	batch.InstanceID = ""
	if err := StoreReactionBatch(database, batch, "self"); err == nil {
		t.Fatal("empty instance must fail closed")
	}
}

func TestReactionBatchReplayRemovalAndReadd(t *testing.T) {
	database, batch := reactionBatchDB(t)
	apply := func(event ReactionEvent) {
		t.Helper()
		batch.Reactions = []ReactionEvent{event}
		if err := StoreReactionBatch(database, batch, "self"); err != nil {
			t.Fatal(err)
		}
	}
	added := batch.Reactions[0]
	apply(added)
	if err := database.Model(&models.ReactionActivity{}).Where("1 = 1").Update("seen", true).Error; err != nil {
		t.Fatal(err)
	}
	apply(added)
	removed := added
	removed.Added, removed.Emoji, removed.Timestamp, removed.Historical = false, "", 201, true
	apply(removed) // Historical removals still dismiss obsolete attention.
	apply(added)   // Replay must not resurrect a removed reaction.
	var active int64
	database.Model(&models.ReactionActivity{}).Where("active = ?", true).Count(&active)
	if active != 0 {
		t.Fatal("replay resurrected a removed activity")
	}
	added.Timestamp = 202
	apply(added)
	var unread []models.ReactionActivity
	database.Where("active = ? AND seen = ?", true, false).Find(&unread)
	if len(unread) != 1 || !unread[0].Timestamp.Equal(time.Unix(202, 0)) {
		t.Fatalf("genuine re-add did not create attention: %+v", unread)
	}
}

func TestReactionBatchUsesBoundedQueries(t *testing.T) {
	database, batch := reactionBatchDB(t)
	batch.Reactions = nil
	for index := range 300 {
		batch.Reactions = append(batch.Reactions, ReactionEvent{InstanceID: "one", ConversationID: "one::chat", MessageID: "sent", UserID: fmt.Sprintf("user-%d", index), Emoji: "👍", Added: true, Timestamp: 200})
	}
	queries := 0
	if err := database.Callback().Query().Before("gorm:query").Register("test:query_count", func(_ *gorm.DB) { queries++ }); err != nil {
		t.Fatal(err)
	}
	if err := StoreReactionBatch(database, batch, "self"); err != nil {
		t.Fatal(err)
	}
	if queries > 8 {
		t.Fatalf("batch performed %d reads for 300 reactions", queries)
	}
	var count int64
	database.Model(&models.ReactionActivity{}).Count(&count)
	if count != 300 {
		t.Fatalf("stored %d activities, want 300", count)
	}
}

func TestNewerSnapshotOfSameReactionCreatesAttentionAndSurvivesLateRemoval(t *testing.T) {
	database, batch := reactionBatchDB(t)
	if err := StoreReactionBatch(database, batch, "self"); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.ReactionActivity{}).Where("1 = 1").Update("seen", true).Error; err != nil {
		t.Fatal(err)
	}
	batch.Reactions[0].Timestamp = 300
	if err := StoreReactionBatch(database, batch, "self"); err != nil {
		t.Fatal(err)
	}
	removed := batch.Reactions[0]
	removed.Added, removed.Emoji, removed.Timestamp = false, "", 250
	batch.Reactions = []ReactionEvent{removed}
	if err := StoreReactionBatch(database, batch, "self"); err != nil {
		t.Fatal(err)
	}
	var active []models.ReactionActivity
	database.Where("active = ?", true).Find(&active)
	if len(active) != 1 || active[0].Seen || active[0].Timestamp.Unix() != 300 {
		t.Fatalf("newer recovered activity was lost: %+v", active)
	}
	var reaction models.Reaction
	if err := database.First(&reaction).Error; err != nil || reaction.CreatedAt.Unix() != 300 {
		t.Fatalf("canonical reaction lost the newer sender timestamp: %+v, %v", reaction, err)
	}
}
