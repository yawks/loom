package whatsapp

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLoadLIDMappingsRepairsReactionsInOneInstanceOnly(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}, &models.LIDMapping{}); err != nil {
		t.Fatal(err)
	}

	const lid = "123456789@lid"
	const jid = "33600000000@s.whatsapp.net"
	if err := database.Create(&models.LIDMapping{LID: lid, JID: jid, Protocol: "whatsapp", LastSeen: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	messages := []models.Message{
		{ProtocolConvID: "whatsapp-a::conversation", ProtocolMsgID: "message-a", Timestamp: time.Now()},
		{ProtocolConvID: "whatsapp-b::conversation", ProtocolMsgID: "message-b", Timestamp: time.Now()},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	reactions := []models.Reaction{
		{MessageID: messages[0].ID, UserID: lid, Emoji: "👍"},
		{MessageID: messages[1].ID, UserID: lid, Emoji: "👍"},
	}
	if err := database.Create(&reactions).Error; err != nil {
		t.Fatal(err)
	}

	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-a"
	if err := provider.loadLIDMappingsFromDB(); err != nil {
		t.Fatal(err)
	}

	var stored []models.Reaction
	if err := database.Order("id").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if got := stored[0].UserID; got != jid {
		t.Fatalf("instance reaction author = %q, want %q", got, jid)
	}
	if got := stored[1].UserID; got != lid {
		t.Fatalf("other instance reaction author = %q, want %q", got, lid)
	}
}

func TestSaveLIDMappingDoesNotRewriteUnchangedMapping(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}, &models.LIDMapping{}); err != nil {
		t.Fatal(err)
	}

	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-a"
	const lid = "123456789@lid"
	const jid = "33600000000@s.whatsapp.net"
	if err := provider.saveLIDMapping(lid, jid); err != nil {
		t.Fatal(err)
	}
	var before models.LIDMapping
	if err := database.Where("lid = ?", lid).First(&before).Error; err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond)
	if err := provider.saveLIDMapping(lid, jid); err != nil {
		t.Fatal(err)
	}
	var after models.LIDMapping
	if err := database.Where("lid = ?", lid).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("unchanged mapping was rewritten: before=%s after=%s", before.UpdatedAt, after.UpdatedAt)
	}
}
