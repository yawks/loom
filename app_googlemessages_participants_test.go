package main

import (
	"testing"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers/googlemessages"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGoogleMessagesSendersDoNotResolveThroughConversationIDs(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&models.LinkedAccount{}, &models.Conversation{}, &models.ParticipantProfile{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	manager := core.NewProviderManager()
	for _, instance := range []string{"googlemessages-1", "googlemessages-2"} {
		manager.AddProvider(instance, googlemessages.NewProvider())
	}
	for _, row := range []models.LinkedAccount{
		{ProviderInstanceID: "googlemessages-1", UserID: "2", Username: "Colissimo"},
		{ProviderInstanceID: "googlemessages-1", UserID: "23", Username: "Sebastien Lemoine"},
		{ProviderInstanceID: "googlemessages-1", UserID: "1446", Username: "06 09 89 36 55"},
	} {
		if err = database.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err = database.Create(&models.ParticipantProfile{ProviderInstanceID: row.ProviderInstanceID, UserID: row.UserID, DisplayName: row.Username}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Zero local IDs exercise both history fetched from the provider and realtime
	// events; provider ownership must be resolved independently for each message.
	messages := []models.Message{
		{ProtocolConvID: "googlemessages-1::1442", SenderID: "1446", SenderName: "Desmond Jean-Paul"},
		{ProtocolConvID: "googlemessages-1::1442", SenderID: "2", SenderName: "Mathieu Changeat", IsFromMe: true},
		{ProtocolConvID: "googlemessages-1::21", SenderID: "23", SenderName: "Naima Kachouir"},
		{ProtocolConvID: "googlemessages-2::21", SenderID: "23", SenderName: "Different account"},
	}
	app := &App{providerManager: manager}
	app.enrichMessagesWithSenderNames(messages)
	for i, want := range []string{"Desmond Jean-Paul", "Mathieu Changeat", "Naima Kachouir", "Different account"} {
		if messages[i].SenderName != want {
			t.Fatalf("sender %d = %q, want %q", i, messages[i].SenderName, want)
		}
	}
	names, err := app.GetParticipantNamesForConversation("googlemessages-1::21", []string{"participant:23", "participant:2"})
	if err != nil || names["participant:23"] != "Naima Kachouir" || names["participant:2"] != "Mathieu Changeat" {
		t.Fatalf("participant names: %v, %v", names, err)
	}
}
