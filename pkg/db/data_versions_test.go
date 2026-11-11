package db

import (
	"Loom/pkg/models"
	"errors"
	"fmt"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"sync"
	"testing"
)

func trackedTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.LinkedAccount{}, &models.MetaContact{}); err != nil {
		t.Fatal(err)
	}
	if err := EnableChangeTracking(database); err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { ForgetChangeTracking(database); _ = connection.Close() })
	return database
}

func TestTableVersionsCommitBoundaries(t *testing.T) {
	database := trackedTestDatabase(t)
	versions := func() TableVersions {
		v, ok := CurrentTableVersions(database)
		if !ok {
			t.Fatal("not tracked")
		}
		return v
	}
	check := func(want TableVersions) {
		t.Helper()
		if got := versions(); got != want {
			t.Fatalf("versions = %+v, want %+v", got, want)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(database.Create(&models.Message{ProtocolMsgID: "initial"}).Error)
	check(TableVersions{Messages: 1})
	rollback := errors.New("rollback")
	err := Transaction(database, func(tx *gorm.DB) error {
		must(tx.Create(&models.Message{ProtocolMsgID: "rollback"}).Error)
		check(TableVersions{Messages: 1})
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	check(TableVersions{Messages: 1})
	must(Transaction(database.Session(&gorm.Session{}), func(tx *gorm.DB) error {
		must(tx.Create(&models.Message{ProtocolMsgID: "committed"}).Error)
		must(Transaction(tx, func(nested *gorm.DB) error {
			must(nested.Create(&models.LinkedAccount{UserID: "nested"}).Error)
			return nil
		}))
		err := Transaction(tx, func(nested *gorm.DB) error {
			must(nested.Create(&models.LinkedAccount{UserID: "rolled-back-nested"}).Error)
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		check(TableVersions{Messages: 1})
		return nil
	}))
	check(TableVersions{Messages: 2, Accounts: 1})
	// A nested success must still disappear with its outer rollback.
	err = Transaction(database, func(tx *gorm.DB) error {
		must(Transaction(tx, func(nested *gorm.DB) error {
			return nested.Create(&models.Message{ProtocolMsgID: "nested-rollback"}).Error
		}))
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	check(TableVersions{Messages: 2, Accounts: 1})
	must(database.Exec("UPDATE messages SET body = ? WHERE protocol_msg_id = ?", "raw", "initial").Error)
	check(TableVersions{Messages: 3, Accounts: 1})
	must(database.Model(&models.Message{}).Where("protocol_msg_id = ?", "initial").Update("body", "updated").Error)
	check(TableVersions{Messages: 4, Accounts: 1})
	must(database.Where("protocol_msg_id = ?", "initial").Delete(&models.Message{}).Error)
	check(TableVersions{Messages: 5, Accounts: 1})
	must(database.Create(&models.MetaContact{DisplayName: "unrelated"}).Error)
	must(database.Exec("UPDATE messages SET body = 'miss' WHERE id = -1").Error)
	check(TableVersions{Messages: 5, Accounts: 1})
	must(database.Exec("/* repair */ WITH selected AS (SELECT id FROM messages) UPDATE messages SET body = 'cte' WHERE id IN (SELECT id FROM selected)").Error)
	check(TableVersions{Messages: 6, Accounts: 1})
	err = Transaction(database, func(tx *gorm.DB) error {
		must(tx.Exec("UPDATE messages SET body = 'rollback raw'").Error)
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	check(TableVersions{Messages: 6, Accounts: 1})
}

func TestTableVersionsRetryOnlyPublishesSuccessfulAttempt(t *testing.T) {
	database := trackedTestDatabase(t)
	attempts := 0
	err := Transaction(database, func(tx *gorm.DB) error {
		attempts++
		message := models.Message{ProtocolMsgID: "retry"}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		if attempts == 1 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := CurrentTableVersions(database)
	if attempts != 2 || v.Messages != 1 {
		t.Fatalf("attempts=%d versions=%+v", attempts, v)
	}
}

func TestTableVersionsConcurrentReadsAndWrites(t *testing.T) {
	database := trackedTestDatabase(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				CurrentTableVersions(database.Session(&gorm.Session{}))
			}
		}()
	}
	for i := range 20 {
		if err := Transaction(database, func(tx *gorm.DB) error {
			message := models.Message{ProtocolMsgID: fmt.Sprint(i)}
			return tx.Create(&message).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	versions, _ := CurrentTableVersions(database)
	if versions.Messages != 20 {
		t.Fatalf("versions = %+v", versions)
	}
}
