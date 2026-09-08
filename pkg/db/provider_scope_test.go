package db

import (
	"Loom/pkg/models"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestProviderScopeFiltersSharedTablesAndFailsClosed(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Conversation{}, &models.LinkedAccount{}); err != nil {
		t.Fatal(err)
	}

	messages := []models.Message{
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "slack-message"},
		{ProtocolConvID: "teams-1::C1", ProtocolMsgID: "teams-message"},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	var scoped []models.Message
	if err := ForProvider(database, "slack-1").Messages().Find(&scoped).Error; err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].ProtocolMsgID != "slack-message" {
		t.Fatalf("scoped messages = %#v", scoped)
	}
	var unowned int64
	if err := ForProvider(database, "").Messages().Count(&unowned).Error; err != nil {
		t.Fatal(err)
	}
	if unowned != 0 {
		t.Fatalf("empty provider scope exposed %d messages", unowned)
	}
}

func TestProviderScopeOwnershipUsesCompleteNamespace(t *testing.T) {
	scope := ForProvider(nil, "slack-1")
	if !scope.OwnsConversation("slack-1::C1") {
		t.Fatal("scope rejected its own conversation")
	}
	if scope.OwnsConversation("slack-10::C1") || scope.OwnsConversation("teams-1::C1") || scope.OwnsConversation("C1") {
		t.Fatal("scope accepted a conversation outside its namespace")
	}
}

func TestProviderScopeMessageQueryUsesConversationIndex(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	var plan []struct{ Detail string }
	query := ForProvider(database, "slack-1").Messages().Select("protocol_conv_id")
	statement := query.Session(&gorm.Session{DryRun: true}).Find(&[]models.Message{}).Statement
	if err := database.Raw("EXPLAIN QUERY PLAN "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	usedConversationIndex := false
	for _, row := range plan {
		if strings.Contains(row.Detail, "USING") && strings.Contains(row.Detail, "protocol_conv_id>?") {
			usedConversationIndex = true
			break
		}
	}
	if !usedConversationIndex {
		t.Fatalf("provider-scoped query did not use conversation index: %#v", plan)
	}
}
