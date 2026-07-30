package db

import (
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestParseTimeMillisPreservesMillisecondPrecision(t *testing.T) {
	timestamp := "2026-07-29 10:11:12.345678901+00:00"
	want := time.Date(2026, 7, 29, 10, 11, 12, 345000000, time.UTC).UnixMilli()

	if got := ParseTimeMillis(timestamp); got != want {
		t.Fatalf("ParseTimeMillis(%q) = %d, want %d", timestamp, got, want)
	}
}

func TestParseTimeMillisConvertsNumericUnixSeconds(t *testing.T) {
	const seconds int64 = 1_785_319_872
	if got, want := ParseTimeMillis(seconds), seconds*1000; got != want {
		t.Fatalf("ParseTimeMillis(%d) = %d, want %d", seconds, got, want)
	}
}

func TestRepairWhatsAppConversationOwnershipUsesNamespacedInstance(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.MetaContact{},
		&models.LinkedAccount{},
		&models.Conversation{},
	); err != nil {
		t.Fatal(err)
	}

	metaA := models.MetaContact{DisplayName: "Contact A"}
	metaB := models.MetaContact{DisplayName: "Contact B"}
	if err := database.Create(&metaA).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&metaB).Error; err != nil {
		t.Fatal(err)
	}
	const jid = "33600000000@s.whatsapp.net"
	accountA := models.LinkedAccount{
		MetaContactID: metaA.ID, Protocol: "whatsapp",
		ProviderInstanceID: "whatsapp-a", UserID: jid,
	}
	accountB := models.LinkedAccount{
		MetaContactID: metaB.ID, Protocol: "whatsapp",
		ProviderInstanceID: "whatsapp-b", UserID: jid,
	}
	if err := database.Create(&accountA).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&accountB).Error; err != nil {
		t.Fatal(err)
	}

	conversation := models.Conversation{
		LinkedAccountID: accountA.ID,
		ProtocolConvID:  "whatsapp-b::" + jid,
	}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := repairWhatsAppConversationOwnership(database); err != nil {
		t.Fatal(err)
	}

	var repaired models.Conversation
	if err := database.First(&repaired, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.LinkedAccountID != accountB.ID {
		t.Fatalf("linked_account_id = %d, want WhatsApp B account %d", repaired.LinkedAccountID, accountB.ID)
	}
}

func TestRepairGoogleMessagesDMSenderNames(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.MetaContact{},
		&models.LinkedAccount{},
		&models.Conversation{},
		&models.Message{},
	); err != nil {
		t.Fatal(err)
	}

	dmAccount := models.LinkedAccount{
		Protocol: "googlemessages", ProviderInstanceID: "googlemessages-1",
		UserID: "dm-1", Username: "Correct contact",
	}
	groupAccount := models.LinkedAccount{
		Protocol: "googlemessages", ProviderInstanceID: "googlemessages-1",
		UserID: "group-1", Username: "Group", IsGroup: true,
	}
	if err := database.Create(&dmAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&groupAccount).Error; err != nil {
		t.Fatal(err)
	}
	dm := models.Conversation{LinkedAccountID: dmAccount.ID, ProtocolConvID: "googlemessages-1::dm-1"}
	group := models.Conversation{LinkedAccountID: groupAccount.ID, ProtocolConvID: "googlemessages-1::group-1", IsGroup: true}
	if err := database.Create(&dm).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	messages := []models.Message{
		{ProtocolMsgID: "incoming", ProtocolConvID: dm.ProtocolConvID, SenderName: "Wrong contact"},
		{ProtocolMsgID: "outgoing", ProtocolConvID: dm.ProtocolConvID, SenderName: "Me", IsFromMe: true},
		{ProtocolMsgID: "group", ProtocolConvID: group.ProtocolConvID, SenderName: "Group participant"},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	if err := repairGoogleMessagesDMSenderNames(database); err != nil {
		t.Fatal(err)
	}

	var repaired []models.Message
	if err := database.Order("id").Find(&repaired).Error; err != nil {
		t.Fatal(err)
	}
	if repaired[0].SenderName != "Correct contact" {
		t.Errorf("incoming DM sender = %q, want corrected contact", repaired[0].SenderName)
	}
	if repaired[1].SenderName != "Me" {
		t.Errorf("outgoing sender = %q, want unchanged", repaired[1].SenderName)
	}
	if repaired[2].SenderName != "Group participant" {
		t.Errorf("group sender = %q, want unchanged", repaired[2].SenderName)
	}
}
