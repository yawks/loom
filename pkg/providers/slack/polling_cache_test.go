package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"errors"
	"fmt"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pollingTestDatabase(tb testing.TB) *gorm.DB {
	tb.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		tb.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.LinkedAccount{}); err != nil {
		tb.Fatal(err)
	}
	if err := db.EnsureMessagePollingIndices(database); err != nil {
		tb.Fatal(err)
	}
	if err := db.EnableChangeTracking(database); err != nil {
		tb.Fatal(err)
	}
	connection, _ := database.DB()
	connection.SetMaxOpenConns(1)
	tb.Cleanup(func() { db.ForgetChangeTracking(database); _ = connection.Close() })
	return database
}

func TestPollingCacheInvalidationAndIsolation(t *testing.T) {
	database := pollingTestDatabase(t)
	p := &SlackProvider{config: core.ProviderConfig{}}
	p.config["_instance_id"] = "slack-1"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	messages := []models.Message{
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "own", Timestamp: time.Now()},
		{ProtocolConvID: "teams-1::C1", ProtocolMsgID: "foreign", Timestamp: time.Now()},
	}
	must(database.Create(&messages).Error)
	var queries atomic.Int64
	must(database.Callback().Query().After("gorm:query").Register("test:count_query", func(*gorm.DB) { queries.Add(1) }))
	must(database.Callback().Row().After("gorm:row").Register("test:count_row", func(*gorm.DB) { queries.Add(1) }))
	load := func(want int) []slackFallbackConversation {
		t.Helper()
		rows, err := p.cachedFallbackConversations(database)
		if err != nil || len(rows) != want {
			t.Fatalf("snapshot=%+v err=%v want=%d", rows, err, want)
		}
		for _, row := range rows {
			if len(row.ProtocolConvID) < 9 || row.ProtocolConvID[:9] != "slack-1::" {
				t.Fatalf("foreign row: %+v", row)
			}
		}
		return rows
	}
	first := load(1)
	if queries.Load() == 0 {
		t.Fatal("query counter inactive")
	}
	first[0].ProtocolConvID = "mutated"
	queries.Store(0)
	load(1)
	if queries.Load() != 0 {
		t.Fatal("cache hit queried SQL")
	}
	rollback := errors.New("rollback")
	err := db.Transaction(database, func(tx *gorm.DB) error {
		if err := tx.Create(&models.Message{ProtocolConvID: "slack-1::C2", ProtocolMsgID: "rollback"}).Error; err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	load(1)
	if queries.Load() != 0 {
		t.Fatal("rollback invalidated cache")
	}
	must(db.Transaction(database, func(tx *gorm.DB) error {
		return tx.Create(&models.LinkedAccount{ProviderInstanceID: "slack-1", UserID: "C2", IsGroup: true}).Error
	}))
	load(2)
	if queries.Load() == 0 {
		t.Fatal("commit did not invalidate")
	}
	queries.Store(0)
	must(db.Transaction(database, func(tx *gorm.DB) error {
		message := models.Message{ProtocolConvID: "slack-1::C3", ProtocolMsgID: "committed", Timestamp: time.Now()}
		return tx.Create(&message).Error
	}))
	load(3)
	if queries.Load() == 0 {
		t.Fatal("message commit did not invalidate")
	}
	queries.Store(0)
	p.pollingSnapshot.expiresAt = time.Time{}
	load(3)
	if queries.Load() == 0 {
		t.Fatal("expiry did not reconcile")
	}
	queries.Store(0)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := p.cachedFallbackConversations(database)
			if err != nil || len(rows) != 3 {
				t.Errorf("concurrent snapshot=%+v err=%v", rows, err)
				return
			}
			rows[0].ProtocolConvID = "caller-local mutation"
		}()
	}
	wg.Wait()
	load(3)
	if queries.Load() != 0 {
		t.Fatal("concurrent idle reads queried SQL")
	}
	var foreign models.Message
	must(database.First(&foreign, messages[1].ID).Error)
	if foreign.ProtocolMsgID != "foreign" {
		t.Fatal("foreign row changed")
	}
	p.config["_instance_id"] = ""
	if _, err := p.cachedFallbackConversations(database); err == nil {
		t.Fatal("empty instance accepted")
	}
}

func BenchmarkPollingSnapshot(b *testing.B) {
	database := pollingTestDatabase(b)
	messages := make([]models.Message, 10000)
	for i := range messages {
		messages[i] = models.Message{ProtocolMsgID: fmt.Sprint(i), ProtocolConvID: fmt.Sprintf("slack-1::C%d", i%100), Timestamp: time.Now()}
	}
	if err := database.CreateInBatches(&messages, 100).Error; err != nil {
		b.Fatal(err)
	}
	p := &SlackProvider{config: core.ProviderConfig{"_instance_id": "slack-1"}}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%v", cached), func(b *testing.B) {
			if _, err := p.cachedFallbackConversations(database); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var err error
				if cached {
					_, err = p.cachedFallbackConversations(database)
				} else {
					_, err = slackFallbackConversations(database, "slack-1")
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
