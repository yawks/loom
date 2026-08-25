package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"gorm.io/gorm"
)

func setupSlackMessageDeletionTest(t *testing.T) (*SlackProvider, models.Message) {
	t.Helper()
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

	provider := NewSlackProvider()
	provider.config = core.ProviderConfig{"_instance_id": "slack-test"}
	message := models.Message{
		ProtocolConvID: core.BuildConvID("slack-test", "C1"),
		ProtocolMsgID:  "1700000000.000001",
		Body:           "to be deleted",
		IsFromMe:       true,
	}
	if err := db.DB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	return provider, message
}

func TestReconcileSlackMessageWindowMarksMissingMessagesDeleted(t *testing.T) {
	provider, missing := setupSlackMessageDeletionTest(t)
	kept := models.Message{
		ProtocolConvID: missing.ProtocolConvID,
		ProtocolMsgID:  "1700000000.000002",
		Body:           "still on Slack",
		Timestamp:      time.Unix(1700000000, 2_000),
	}
	missing.Timestamp = time.Unix(1700000000, 1_000)
	if err := db.DB.Save(&missing).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&kept).Error; err != nil {
		t.Fatal(err)
	}

	provider.reconcileSlackMessageWindow(
		missing.ProtocolConvID,
		[]goslack.Message{{Msg: goslack.Msg{Timestamp: kept.ProtocolMsgID}}},
		time.Unix(1699999999, 0),
		time.Unix(1700000001, 0),
	)

	assertSlackMessageDeleted(t, provider, missing)
	var storedKept models.Message
	if err := db.DB.First(&storedKept, kept.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedKept.IsDeleted {
		t.Fatal("message still returned by Slack was marked deleted")
	}
}

func assertSlackMessageDeleted(t *testing.T, provider *SlackProvider, message models.Message) {
	t.Helper()
	var stored models.Message
	if err := db.DB.First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsDeleted || stored.DeletedTimestamp == nil {
		t.Fatalf("stored message was not marked deleted: %+v", stored)
	}
	select {
	case event := <-provider.eventChan:
		messageEvent, ok := event.(core.MessageEvent)
		if !ok || !messageEvent.Message.IsDeleted || messageEvent.Message.ProtocolMsgID != message.ProtocolMsgID {
			t.Fatalf("unexpected deletion event: %#v", event)
		}
	default:
		t.Fatal("expected a message event for the deletion")
	}
}

func TestHandleRTMMessageEventDeletesExistingMessage(t *testing.T) {
	provider, message := setupSlackMessageDeletionTest(t)
	provider.handleRTMMessageEvent(&goslack.MessageEvent{Msg: goslack.Msg{
		Channel:          "C1",
		SubType:          goslack.MsgSubTypeMessageDeleted,
		DeletedTimestamp: message.ProtocolMsgID,
		EventTimestamp:   "1700000001.000001",
	}})
	assertSlackMessageDeleted(t, provider, message)
}

func TestHandleSocketMessageEventDeletesExistingMessage(t *testing.T) {
	provider, message := setupSlackMessageDeletionTest(t)
	provider.handleMessageEvent(&slackevents.MessageEvent{
		Channel:          "C1",
		SubType:          goslack.MsgSubTypeMessageDeleted,
		DeletedTimeStamp: message.ProtocolMsgID,
		EventTimeStamp:   "1700000001.000001",
	})
	assertSlackMessageDeleted(t, provider, message)
}
