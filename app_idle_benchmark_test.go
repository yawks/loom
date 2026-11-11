package main

import (
	"Loom/pkg/db"
	"encoding/json"
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in real-data benchmark. The database is opened read-only, the in-memory
// store is copied before presentation changes, and avatar files use a test-only
// directory. Output contains aggregate timings/sizes, never contact contents.
// LOOM_BENCHMARK_DATABASE=/path/to/loom.db go test -run '^$' -bench BenchmarkContactBridge -benchtime=10x
func BenchmarkContactBridge(b *testing.B) {
	path := os.Getenv("LOOM_BENCHMARK_DATABASE")
	if path == "" {
		b.Skip("set LOOM_BENCHMARK_DATABASE for read-only real-data measurement")
	}
	database, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		b.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = connection.Close() })
	previous := db.DB
	db.DB = database
	b.Cleanup(func() { db.DB = previous })
	if err := db.ContactStore.Load(); err != nil {
		b.Fatal(err)
	}
	app := &App{avatarReferences: avatarReferenceCache{directory: b.TempDir()}}
	for _, compact := range []bool{false, true} {
		name := "inline"
		if compact {
			name = "lazy-avatars"
		}
		b.Run(name, func(b *testing.B) {
			contacts := db.ContactStore.GetAll()
			if compact {
				app.compactContactAvatars(contacts)
			}
			payload, err := json.Marshal(contacts)
			if err != nil {
				b.Fatal(err)
			}
			size := len(payload)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				contacts := db.ContactStore.GetAll()
				if compact {
					app.compactContactAvatars(contacts)
				}
				payload, err = json.Marshal(contacts)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(size), "payload-B")
			if len(payload) == 0 {
				b.Fatal("empty contact payload")
			}
		})
	}
}
