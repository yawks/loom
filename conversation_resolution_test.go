package main

import (
	"testing"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetProviderForNamespacedConversationDoesNotFallBack(t *testing.T) {
	app := &App{providerManager: core.NewProviderManager()}

	if got := app.getProviderForConversation("whatsapp-1::33617590388@s.whatsapp.net"); got != nil {
		t.Fatal("an unavailable WhatsApp instance must not fall back to the active provider")
	}
}

func TestSameParticipantSetIgnoresAuthenticatedUser(t *testing.T) {
	selected := map[string]bool{"alice": true, "bob": true}
	directory := map[string]bool{"alice": true, "bob": true}
	participants := []models.GroupParticipant{{UserID: "me"}, {UserID: "bob"}, {UserID: "alice"}}
	if !sameParticipantSet(selected, participants, directory, "me") {
		t.Fatal("expected the same selectable participants regardless of order and authenticated user")
	}
}

func TestPersistCreatedConversationReusesExistingSlackRecords(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE UNIQUE INDEX idx_test_provider_user ON linked_accounts(provider_instance_id, user_id)").Error; err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	if err := db.ContactStore.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.DB = previousDB
		if previousDB != nil {
			_ = db.ContactStore.Load()
		}
	})

	app := &App{}
	first := &models.Conversation{ProtocolConvID: "C123", GroupName: "project", IsGroup: true}
	created, err := app.persistCreatedConversation("slack-work", "slack", "private_channel", "project", first)
	if err != nil {
		t.Fatal(err)
	}
	second := &models.Conversation{ProtocolConvID: "C123", GroupName: "project", IsGroup: true}
	reused, err := app.persistCreatedConversation("slack-work", "slack", "private_channel", "project", second)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != reused.ID || first.ID != second.ID {
		t.Fatalf("records were not reused: contacts %d/%d, conversations %d/%d", created.ID, reused.ID, first.ID, second.ID)
	}
	var accountCount, conversationCount int64
	database.Model(&models.LinkedAccount{}).Count(&accountCount)
	database.Model(&models.Conversation{}).Count(&conversationCount)
	if accountCount != 1 || conversationCount != 1 {
		t.Fatalf("unexpected record counts: accounts=%d conversations=%d", accountCount, conversationCount)
	}
}

func TestSameParticipantSetRejectsExtraDirectoryContact(t *testing.T) {
	selected := map[string]bool{"alice": true, "bob": true}
	directory := map[string]bool{"alice": true, "bob": true, "charlie": true}
	participants := []models.GroupParticipant{{UserID: "alice"}, {UserID: "bob"}, {UserID: "charlie"}}
	if sameParticipantSet(selected, participants, directory, "me") {
		t.Fatal("expected a group with an extra selectable participant to be rejected")
	}
}

func TestProviderAccountsNamespaceConversationID(t *testing.T) {
	contacts := providerAccountsToMetaContacts("teams-work", []models.LinkedAccount{{
		Protocol: "teams", ProviderInstanceID: "teams-work", UserID: "8:orgid:alice",
		Username: "Alice", ConversationID: "19:alice_me@unq.gbl.spaces",
	}})
	if len(contacts) != 1 {
		t.Fatalf("got %d contacts, want 1", len(contacts))
	}
	if got := contacts[0].LinkedAccounts[0].ConversationID; got != "teams-work::19:alice_me@unq.gbl.spaces" {
		t.Fatalf("conversation ID = %q, want namespaced ID", got)
	}

	contacts = providerAccountsToMetaContacts("teams-work", contacts[0].LinkedAccounts)
	if got := contacts[0].LinkedAccounts[0].ConversationID; got != "teams-work::19:alice_me@unq.gbl.spaces" {
		t.Fatalf("already namespaced conversation ID changed to %q", got)
	}
}
