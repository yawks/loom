package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"gorm.io/gorm"
)

func deletionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous; sqlDB, _ := database.DB(); _ = sqlDB.Close() })
	return database
}

func TestLocallyDeletedMessageIsNotReemitted(t *testing.T) {
	database := deletionTestDB(t)
	conversation := models.Conversation{ProtocolConvID: "teams-1::room"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	message := models.Message{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "deleted", Body: "original"}
	if err := database.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Delete(&message).Error; err != nil {
		t.Fatal(err)
	}
	provider := NewProvider()
	provider.instance = "teams-1"
	client, err := msteams.NewClient(msteams.ClientConfig{TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	editedAt := time.Now()
	for _, edited := range []bool{false, true} {
		eventType := msteams.EventTypeNewMessage
		if edited {
			eventType = msteams.EventTypeEditMessage
		}
		provider.handleRemoteEvent(client, msteams.Event{
			Type: eventType, ThreadID: "room", Timestamp: editedAt,
			Message: &msteams.Message{ID: "deleted", From: client.UserMRI(), Content: "late echo"},
		})
		incoming := []models.Message{{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "deleted", Body: "late echo", IsEdited: edited, EditedTimestamp: &editedAt}}
		if err := provider.storeMessages(incoming); err != nil {
			t.Fatal(err)
		}
		provider.emit(core.MessageEvent{InstanceID: provider.instance, Message: incoming[0]})
		provider.emit(core.MessageBatchEvent{InstanceID: provider.instance, ConversationID: conversation.ProtocolConvID, Messages: incoming})
		select {
		case event := <-provider.eventChan:
			t.Fatalf("deleted message was emitted: %+v", event)
		default:
		}
	}
	var stored models.Message
	if err := database.Unscoped().First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.DeletedAt.Valid || stored.Body != "original" {
		t.Fatalf("local tombstone changed: %+v", stored)
	}
}

func TestHistoryExcludesLocalDeletionsForThisProviderOnly(t *testing.T) {
	database := deletionTestDB(t)
	for _, message := range []models.Message{
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "deleted"},
		{ProtocolConvID: "teams-2::room", ProtocolMsgID: "other-provider"},
	} {
		if err := database.Create(&message).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Delete(&message).Error; err != nil {
			t.Fatal(err)
		}
	}
	provider := NewProvider()
	provider.instance = "teams-1"
	incoming := []models.Message{
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "deleted"},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "other-provider"},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "visible"},
	}
	visible, err := provider.excludeLocallyDeletedMessages(incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 || visible[0].ProtocolMsgID != "other-provider" || visible[1].ProtocolMsgID != "visible" {
		t.Fatalf("unexpected history: %+v", visible)
	}
	provider.instance = ""
	if _, err := provider.excludeLocallyDeletedMessages(incoming); err == nil {
		t.Fatal("expected missing instance ID error")
	}
}

func TestDelayedEditPreservesRemoteDeletion(t *testing.T) {
	database := deletionTestDB(t)
	conversation := models.Conversation{ProtocolConvID: "teams-1::room"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Unix(200, 0)
	message := models.Message{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "deleted", IsDeleted: true, DeletedTimestamp: &deletedAt}
	provider := NewProvider()
	if err := provider.storeMessages([]models.Message{message}); err != nil {
		t.Fatal(err)
	}
	editedAt := time.Unix(100, 0)
	incoming := []models.Message{{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "deleted", IsEdited: true, EditedTimestamp: &editedAt}}
	if err := provider.storeMessages(incoming); err != nil {
		t.Fatal(err)
	}
	if !incoming[0].IsDeleted || incoming[0].DeletedTimestamp == nil || !incoming[0].DeletedTimestamp.Equal(deletedAt) {
		t.Fatalf("late edit cleared deletion: %+v", incoming[0])
	}
}
