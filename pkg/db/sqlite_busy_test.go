package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type sqliteCodeError int

func (err sqliteCodeError) Error() string { return fmt.Sprintf("sqlite error (%d)", err) }
func (err sqliteCodeError) Code() int     { return int(err) }

func TestIsSQLiteBusy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "busy", err: sqliteCodeError(5), want: true},
		{name: "busy snapshot", err: sqliteCodeError(517), want: true},
		{name: "wrapped busy snapshot", err: fmt.Errorf("store messages: %w", sqliteCodeError(517)), want: true},
		{name: "fallback driver message", err: errors.New("database is locked (517)"), want: true},
		{name: "constraint", err: sqliteCodeError(19), want: false},
		{name: "other", err: errors.New("network unavailable"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSQLiteBusy(test.err); got != test.want {
				t.Fatalf("isSQLiteBusy(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestSQLiteConnectionOptionsApplyToEveryConnection(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "loom.db")+sqliteConnectionOptions), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(3)

	for range 3 {
		connection, err := sqlDB.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()

		var busyTimeout, foreignKeys int
		if err := connection.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if busyTimeout != 30000 || foreignKeys != 1 {
			t.Fatalf("connection options = busy_timeout %d, foreign_keys %d", busyTimeout, foreignKeys)
		}
	}
}
