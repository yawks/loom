package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetAllLastMessagesCollapsesConcurrentCacheMisses(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE INDEX idx_messages_conv_latest_jd ON messages(protocol_conv_id, julianday(timestamp) DESC, id DESC) WHERE deleted_at IS NULL`).Error; err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ProtocolConvID: "conversation-a", ProtocolMsgID: "older", Body: "older", Timestamp: base},
		{ProtocolConvID: "conversation-a", ProtocolMsgID: "newer", Body: "newer", Timestamp: base.Add(time.Minute)},
		{ProtocolConvID: "conversation-a", ProtocolMsgID: "empty-technical", Timestamp: base.Add(2 * time.Minute)},
		{ProtocolConvID: "conversation-b", ProtocolMsgID: "only", Body: "only", Timestamp: base},
		{ProtocolConvID: "conversation-c", ProtocolMsgID: "media", Attachments: `[{"type":"image","fileName":"image.jpg"}]`, Timestamp: base},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	var queries atomic.Int32
	if err := database.Callback().Query().Before("gorm:query").Register("test:count_last_message_queries", func(tx *gorm.DB) {
		if tx.Statement.SQL.String() == "" {
			return
		}
		queries.Add(1)
		time.Sleep(25 * time.Millisecond)
	}); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	start := make(chan struct{})
	results := make(chan map[string]models.Message, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := app.GetAllLastMessages()
			results <- result
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if got := result["conversation-a"].ProtocolMsgID; got != "newer" {
			t.Fatalf("latest message = %q, want newer", got)
		}
		if got := result["conversation-b"].ProtocolMsgID; got != "only" {
			t.Fatalf("latest message = %q, want only", got)
		}
		if got := result["conversation-c"].ProtocolMsgID; got != "media" {
			t.Fatalf("latest media message = %q, want media", got)
		}
	}
	if got := queries.Load(); got != 1 {
		t.Fatalf("database queries = %d, want 1", got)
	}
}
