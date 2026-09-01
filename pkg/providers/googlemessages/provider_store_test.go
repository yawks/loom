package googlemessages

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"gorm.io/gorm"
)

func TestStoreConversationDoesNotRewriteUnchangedRows(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}

	meta := models.MetaContact{DisplayName: "PACIFICA"}
	if err := database.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	account := models.LinkedAccount{
		MetaContactID: meta.ID, Protocol: providerID, ProviderInstanceID: "googlemessages-1",
		UserID: "conversation-1", Username: "PACIFICA", Status: "offline", ConversationID: "conversation-1",
	}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: "googlemessages-1::conversation-1", GroupName: "PACIFICA"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	message := models.Message{
		ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID,
		ProtocolMsgID: "message-1", SenderName: "PACIFICA", Timestamp: time.Now(),
	}
	if err := database.Create(&message).Error; err != nil {
		t.Fatal(err)
	}

	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })
	if err := db.ContactStore.Load(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond)
	provider := &Provider{instance: "googlemessages-1"}
	remote := &gmproto.Conversation{ConversationID: "conversation-1", Name: "PACIFICA"}
	if err := provider.storeConversation(remote); err != nil {
		t.Fatal(err)
	}

	var storedMeta models.MetaContact
	var storedAccount models.LinkedAccount
	var storedConversation models.Conversation
	if err := database.First(&storedMeta, meta.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&storedAccount, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&storedConversation, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !storedMeta.UpdatedAt.Equal(meta.UpdatedAt) {
		t.Fatal("unchanged meta contact was rewritten")
	}
	if !storedAccount.UpdatedAt.Equal(account.UpdatedAt) {
		t.Fatal("unchanged linked account was rewritten")
	}
	if !storedConversation.UpdatedAt.Equal(conversation.UpdatedAt) {
		t.Fatal("unchanged conversation was rewritten")
	}
}
