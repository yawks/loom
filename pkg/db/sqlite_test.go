package db

import (
	"Loom/pkg/models"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestEnsureIndicesCreatesMessageQueryIndices(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureIndices(database); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"idx_messages_conv_latest_jd", "idx_messages_active_calls"} {
		var sql string
		if err := database.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", name).Scan(&sql).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sql, "WHERE deleted_at IS NULL") {
			t.Fatalf("index %s was not created as a partial non-deleted index: %q", name, sql)
		}
	}
}

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

func TestNormalizeLegacyZeroCallDurations(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	positive := int32(12)
	legacy := models.Message{ProtocolMsgID: "legacy-zero-call", CallType: "incoming_call", CallDurationSecs: &zero}
	valid := models.Message{ProtocolMsgID: "valid-call", CallType: "incoming_call", CallDurationSecs: &positive}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}

	if err := normalizeLegacyZeroCallDurations(database); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&legacy, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&valid, valid.ID).Error; err != nil {
		t.Fatal(err)
	}
	if legacy.CallDurationSecs != nil {
		t.Fatalf("legacy zero duration = %v, want nil", legacy.CallDurationSecs)
	}
	if valid.CallDurationSecs == nil || *valid.CallDurationSecs != 12 {
		t.Fatalf("valid duration = %v, want 12", valid.CallDurationSecs)
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
