package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPersistParticipantProfileIsScopedAndKeepsRichFields(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.ParticipantProfile{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })

	for _, profile := range []models.ContactProfile{
		{ProviderInstanceID: "work", UserID: "same-id", DisplayName: "Alice", AvatarURL: "alice.png", Emails: []string{"alice@example.com"}, PhoneNumbers: []string{"+331"}, Company: "Example"},
		{ProviderInstanceID: "personal", UserID: "same-id", DisplayName: "Bob"},
	} {
		if err := persistParticipantProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	if err := persistParticipantProfile(models.ContactProfile{ProviderInstanceID: "work", UserID: "same-id", DisplayName: "Alice Updated"}); err != nil {
		t.Fatal(err)
	}
	if err := persistParticipantProfile(models.ContactProfile{ProviderInstanceID: "work", UserID: "same-id", DisplayName: "Vous"}); err != nil {
		t.Fatal(err)
	}

	var rows []models.ParticipantProfile
	if err := database.Order("provider_instance_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("profiles = %d, want 2 provider-scoped rows", len(rows))
	}
	var work models.ParticipantProfile
	if err := database.Where("provider_instance_id = ? AND user_id = ?", "work", "same-id").First(&work).Error; err != nil {
		t.Fatal(err)
	}
	got := contactProfileFromParticipantCache(work)
	if got.DisplayName != "Alice Updated" || got.AvatarURL != "alice.png" || got.Company != "Example" || len(got.Emails) != 1 || len(got.PhoneNumbers) != 1 {
		t.Fatalf("unexpected cached profile: %+v", got)
	}
}
