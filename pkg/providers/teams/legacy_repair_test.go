package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLegacyRepairCadenceIsPerInstanceAndRetriesFailures(t *testing.T) {
	now := time.Now()
	one, two := NewProvider(), NewProvider()
	calls := 0
	repair := func() error { calls++; return nil }
	for range 10 {
		if err := one.runLegacyRepairs(now, repair); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("repeated full-history repair %d times", calls)
	}
	if err := two.runLegacyRepairs(now, repair); err != nil || calls != 2 {
		t.Fatal("provider instances share a repair cooldown")
	}
	if err := one.runLegacyRepairs(now.Add(30*time.Minute), func() error { calls++; return errors.New("busy") }); err == nil {
		t.Fatal("lost repair error")
	}
	if err := one.runLegacyRepairs(now.Add(30*time.Minute+time.Second), repair); err != nil || calls != 3 {
		t.Fatal("failure retry is not bounded")
	}
	if err := one.runLegacyRepairs(now.Add(31*time.Minute), repair); err != nil || calls != 4 {
		t.Fatal("failed repair did not retry")
	}
}

func TestLegacyRepairDoesNotReadOrMutateAnotherProvider(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	rows := []models.Message{
		{ProtocolConvID: "teams-one::chat", ProtocolMsgID: "own", Body: "<p><strong>hello</strong></p>"},
		// Both would require network reads if mistakenly enumerated as work.
		{ProtocolConvID: "teams-two::chat", ProtocolMsgID: "foreign-card", Body: "https://go.skype.com/cards.unsupported"},
		{ProtocolConvID: "teams-two::chat", ProtocolMsgID: "foreign-recording", Body: "Play"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	p := NewProvider()
	p.instance = "teams-one"
	// nil client intentionally makes any foreign network work fail the test.
	if err := p.repairLegacyMessages(nil); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows[1:] {
		var stored models.Message
		if err := database.First(&stored, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Body != row.Body {
			t.Fatal("foreign message modified")
		}
	}
	for len(p.eventChan) > 0 {
		event := <-p.eventChan
		if err := core.ValidateProviderEventOwnership("teams-one", event); err != nil {
			t.Fatal(err)
		}
	}
	p.instance = ""
	if _, err := p.repairStoredCards(nil); err == nil {
		t.Fatal("empty instance accepted")
	}
}
