package core

import (
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDeleteProviderDataRemovesCompleteOwnedGraph(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{},
		&models.GroupParticipant{}, &models.Message{}, &models.MessagePin{},
		&models.MessageWatchRule{}, &models.MessageWatchMatch{},
		&models.Reaction{}, &models.MessageReceipt{}, &models.ProviderConfiguration{},
		&models.ContactAlias{}, &models.LIDMapping{},
	); err != nil {
		t.Fatal(err)
	}

	sharedMeta := models.MetaContact{DisplayName: "Shared"}
	orphanMeta := models.MetaContact{DisplayName: "Only target"}
	database.Create(&sharedMeta)
	database.Create(&orphanMeta)
	target := models.LinkedAccount{MetaContactID: sharedMeta.ID, Protocol: "whatsapp", ProviderInstanceID: "wa-work", UserID: "shared-user"}
	targetOnly := models.LinkedAccount{MetaContactID: orphanMeta.ID, Protocol: "whatsapp", ProviderInstanceID: "wa-work", UserID: "target-user"}
	kept := models.LinkedAccount{MetaContactID: sharedMeta.ID, Protocol: "whatsapp", ProviderInstanceID: "wa-home", UserID: "shared-user"}
	for _, value := range []*models.LinkedAccount{&target, &targetOnly, &kept} {
		if err := database.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	targetConv := models.Conversation{LinkedAccountID: target.ID, ProtocolConvID: "wa-work::chat"}
	keptConv := models.Conversation{LinkedAccountID: kept.ID, ProtocolConvID: "wa-home::chat"}
	database.Create(&targetConv)
	database.Create(&keptConv)
	database.Create(&models.GroupParticipant{ConversationID: targetConv.ID, UserID: "member"})
	targetMessage := models.Message{ConversationID: targetConv.ID, ProtocolConvID: targetConv.ProtocolConvID, ProtocolMsgID: "target-message", Timestamp: time.Now()}
	orphanMessage := models.Message{ProtocolConvID: "wa-work::orphan", ProtocolMsgID: "orphan-message", Timestamp: time.Now()}
	keptMessage := models.Message{ConversationID: keptConv.ID, ProtocolConvID: keptConv.ProtocolConvID, ProtocolMsgID: "kept-message", Timestamp: time.Now()}
	for _, value := range []*models.Message{&targetMessage, &orphanMessage, &keptMessage} {
		if err := database.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	database.Delete(&targetMessage) // soft-deleted messages must also be purged
	database.Create(&models.Reaction{MessageID: targetMessage.ID, UserID: "member"})
	database.Create(&models.MessageReceipt{MessageID: targetMessage.ID, UserID: "member"})
	database.Create(&models.MessagePin{ProviderInstanceID: "wa-work", ProtocolMsgID: "target-message"})
	watchRule := models.MessageWatchRule{ConversationID: targetConv.ID, Pattern: "urgent"}
	database.Create(&watchRule)
	database.Create(&models.MessageWatchMatch{RuleID: watchRule.ID, MessageID: targetMessage.ID})
	database.Create(&models.ProviderConfiguration{ProviderID: "whatsapp", InstanceID: "wa-work"})
	database.Create(&models.ContactAlias{UserID: "shared-user", Alias: "Shared alias"})
	database.Create(&models.ContactAlias{UserID: "target-user", Alias: "Target alias"})

	if _, err := deleteProviderData(database, "wa-work"); err != nil {
		t.Fatal(err)
	}

	assertCount := func(model any, where string, args []any, want int64) {
		t.Helper()
		var got int64
		query := database.Unscoped().Model(model)
		if where != "" {
			query = query.Where(where, args...)
		}
		if err := query.Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%T count = %d, want %d", model, got, want)
		}
	}
	assertCount(&models.LinkedAccount{}, "provider_instance_id = ?", []any{"wa-work"}, 0)
	assertCount(&models.Conversation{}, "id = ?", []any{targetConv.ID}, 0)
	assertCount(&models.Message{}, "protocol_msg_id IN ?", []any{[]string{"target-message", "orphan-message"}}, 0)
	assertCount(&models.Reaction{}, "message_id = ?", []any{targetMessage.ID}, 0)
	assertCount(&models.MessageReceipt{}, "message_id = ?", []any{targetMessage.ID}, 0)
	assertCount(&models.GroupParticipant{}, "conversation_id = ?", []any{targetConv.ID}, 0)
	assertCount(&models.MessagePin{}, "provider_instance_id = ?", []any{"wa-work"}, 0)
	assertCount(&models.MessageWatchRule{}, "conversation_id = ?", []any{targetConv.ID}, 0)
	assertCount(&models.MessageWatchMatch{}, "rule_id = ?", []any{watchRule.ID}, 0)
	assertCount(&models.ProviderConfiguration{}, "instance_id = ?", []any{"wa-work"}, 0)
	assertCount(&models.MetaContact{}, "id = ?", []any{orphanMeta.ID}, 0)
	assertCount(&models.MetaContact{}, "id = ?", []any{sharedMeta.ID}, 1)
	assertCount(&models.ContactAlias{}, "user_id = ?", []any{"target-user"}, 0)
	assertCount(&models.ContactAlias{}, "user_id = ?", []any{"shared-user"}, 1)
	assertCount(&models.Message{}, "protocol_msg_id = ?", []any{"kept-message"}, 1)
}
