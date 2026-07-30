package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
)

func TestSeedMockData(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatalf("initialize mock database: %v", err)
	}
	if err := seedMockData(); err != nil {
		t.Fatalf("seed mock database: %v", err)
	}

	var contacts, conversations, messages, providers int64
	for name, query := range map[string]struct {
		model any
		count *int64
	}{
		"contacts":      {&models.MetaContact{}, &contacts},
		"conversations": {&models.Conversation{}, &conversations},
		"messages":      {&models.Message{}, &messages},
		"providers":     {&models.ProviderConfiguration{}, &providers},
	} {
		if err := db.DB.Model(query.model).Count(query.count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
	}

	if contacts != 10 || conversations != 10 || messages != 29 || providers != 5 {
		t.Fatalf(
			"unexpected mock data counts: contacts=%d conversations=%d messages=%d providers=%d",
			contacts, conversations, messages, providers,
		)
	}

	var missingContactAvatars int64
	if err := db.DB.Model(&models.MetaContact{}).
		Where("avatar_url = '' OR avatar_url NOT LIKE 'https://randomuser.me/api/portraits/%'").
		Count(&missingContactAvatars).Error; err != nil {
		t.Fatalf("count missing contact avatars: %v", err)
	}
	if missingContactAvatars != 0 {
		t.Fatalf("%d mock contacts do not have an embedded image avatar", missingContactAvatars)
	}

}
