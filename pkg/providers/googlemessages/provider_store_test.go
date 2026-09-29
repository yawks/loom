package googlemessages

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"gorm.io/gorm"
)

func TestStoreConversationRetriesSQLiteBusy(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "loom.db")+"?_txlock=immediate&_pragma=busy_timeout(1)&_pragma=journal_mode(WAL)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}

	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })
	if err := db.ContactStore.Load(); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
	})
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, func() {
		_, _ = conn.ExecContext(context.Background(), "COMMIT")
	})

	provider := &Provider{instance: "googlemessages-1"}
	remote := &gmproto.Conversation{ConversationID: "conversation-1", Name: "PACIFICA"}
	if err := provider.storeConversation(remote); err != nil {
		t.Fatalf("store conversation while SQLite writer is busy: %v", err)
	}

	var count int64
	if err := database.Model(&models.Conversation{}).Where("protocol_conv_id = ?", "googlemessages-1::conversation-1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored conversations = %d, want 1", count)
	}
}

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

func TestStoreMessagesUsesConversationFromProviderInstance(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}

	for _, instance := range []string{"googlemessages-1", "googlemessages-2"} {
		meta := models.MetaContact{DisplayName: instance}
		if err := database.Create(&meta).Error; err != nil {
			t.Fatal(err)
		}
		account := models.LinkedAccount{MetaContactID: meta.ID, Protocol: providerID, ProviderInstanceID: instance, UserID: "conversation-1"}
		if err := database.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: instance + "::conversation-1"}).Error; err != nil {
			t.Fatal(err)
		}
	}

	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })
	provider := &Provider{instance: "googlemessages-1"}
	message := models.Message{ProtocolConvID: "googlemessages-1::conversation-1", ProtocolMsgID: "message-1", Timestamp: time.Now()}
	if err := provider.storeMessages([]models.Message{message}); err != nil {
		t.Fatal(err)
	}

	var stored models.Message
	if err := database.Where("protocol_msg_id = ?", message.ProtocolMsgID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var conversation models.Conversation
	if err := database.Where("protocol_conv_id = ?", message.ProtocolConvID).First(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ConversationID != conversation.ID {
		t.Fatalf("message conversation ID = %d, want %d", stored.ConversationID, conversation.ID)
	}
}
