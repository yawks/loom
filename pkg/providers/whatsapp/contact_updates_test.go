package whatsapp

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"

	"gorm.io/gorm"
)

func TestContactUpdatesPreserveLegacyLinkAndSkipUnchangedExtra(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB.DB(); _ = sqlDB.Close(); db.DB = nil })
	w := NewWhatsAppProvider()
	w.config["_instance_id"] = "whatsapp-1"
	// Reproduce a persisted legacy row whose zero FK was accepted historically.
	if err := db.DB.Exec("PRAGMA foreign_keys=OFF").Error; err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{"whatsapp-1", "whatsapp-2"} {
		account := models.LinkedAccount{ProviderInstanceID: instance, Protocol: "whatsapp", UserID: "person", Username: "Old", Status: "away", Extra: `{"lid":"123@lid"}`}
		if err := db.DB.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DB.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		t.Fatal(err)
	}
	w.updateLinkedAccountName("person", "New")
	var own, other models.LinkedAccount
	if err := db.ForProvider(db.DB, "whatsapp-1").LinkedAccounts().First(&own).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.ForProvider(db.DB, "whatsapp-2").LinkedAccounts().First(&other).Error; err != nil {
		t.Fatal(err)
	}
	if own.Username != "New" || own.Status != "away" || own.MetaContactID != 0 || other.Username != "Old" {
		t.Fatalf("unexpected accounts: %+v / %+v", own, other)
	}
	data := WhatsAppExtraData{LID: "123@lid", PhoneNumber: "123@s.whatsapp.net"}
	if err := w.updateLinkedAccountExtra("person", data); err != nil {
		t.Fatal(err)
	}
	if err := db.ForProvider(db.DB, "whatsapp-2").LinkedAccounts().First(&other).Error; err != nil {
		t.Fatal(err)
	}
	if other.Extra != `{"lid":"123@lid"}` {
		t.Fatalf("foreign account metadata changed: %s", other.Extra)
	}
	writes := 0
	if err := db.DB.Callback().Update().Before("gorm:update").Register("test:updates", func(tx *gorm.DB) { writes++ }); err != nil {
		t.Fatal(err)
	}
	if err := w.updateLinkedAccountExtra("person", data); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("unchanged metadata generated %d updates", writes)
	}
	w.updateLinkedAccountName("new-person", "Created")
	var created models.LinkedAccount
	if err := db.ForProvider(db.DB, "whatsapp-1").LinkedAccounts().Where("user_id = ?", "new-person").First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.MetaContactID == 0 {
		t.Fatal("created account lacks valid meta-contact")
	}
}
