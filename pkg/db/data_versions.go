package db

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"gorm.io/gorm"
)

// TableVersions are conservative, process-local commit versions. They contain
// no rows or provider identifiers. A foreign provider write may invalidate a
// cache, but its subsequent reads must still use ForProvider.
type TableVersions struct{ Messages, Accounts uint64 }
type tableVersionTracker struct{ messages, accounts atomic.Uint64 }
type pendingTableChanges struct{ bits atomic.Uint32 }
type pendingChangesKey struct{}

const messageChanged uint32 = 1
const accountChanged uint32 = 2
const versionCallbackName = "loom:table_versions"

var versionTrackers sync.Map // *sql.DB -> *tableVersionTracker
var versionInstallMu sync.Mutex

// Conservative detection also covers comments and WITH-prefixed mutations.
// False positives merely expire a cache; missing a mutation would hide work.
var rawMutation = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|REPLACE)\b`)
var rawMessages = regexp.MustCompile(`(?i)\bmessages\b`)
var rawAccounts = regexp.MustCompile(`(?i)\blinked_accounts\b`)

func registeredVersionTracker(database *gorm.DB) *tableVersionTracker {
	connection, err := database.DB()
	if err != nil {
		return nil
	}
	if tracker, ok := versionTrackers.Load(connection); ok {
		return tracker.(*tableVersionTracker)
	}
	return nil
}

func (tracker *tableVersionTracker) commit(bits uint32) {
	if bits&messageChanged != 0 {
		tracker.messages.Add(1)
	}
	if bits&accountChanged != 0 {
		tracker.accounts.Add(1)
	}
}

func CurrentTableVersions(database *gorm.DB) (TableVersions, bool) {
	tracker := registeredVersionTracker(database)
	if tracker == nil {
		return TableVersions{}, false
	}
	return TableVersions{Messages: tracker.messages.Load(), Accounts: tracker.accounts.Load()}, true
}

// EnableChangeTracking must run before provider/background work starts. Hooks
// do no SQL and emit no events: one atomic update per committed batch/table.
// Explicit db.Transaction calls accumulate changes until their outer commit,
// so a rolled-back write can never publish a new cache version.
func EnableChangeTracking(database *gorm.DB) error {
	versionInstallMu.Lock()
	defer versionInstallMu.Unlock()
	connection, err := database.DB()
	if err != nil {
		return err
	}
	if _, exists := versionTrackers.Load(connection); exists {
		return nil
	}
	tracker := &tableVersionTracker{}
	record := func(tx *gorm.DB) {
		if tx.Error != nil || tx.DryRun || tx.RowsAffected == 0 {
			return
		}
		var bits uint32
		var table string
		if fields := strings.Fields(tx.Statement.Table); len(fields) > 0 {
			table = strings.Trim(fields[0], "`\"")
		}
		switch table {
		case "messages":
			bits = messageChanged
		case "linked_accounts":
			bits = accountChanged
		default:
			statement := tx.Statement.SQL.String()
			if !rawMutation.MatchString(statement) {
				return
			}
			if rawMessages.MatchString(statement) {
				bits |= messageChanged
			}
			if rawAccounts.MatchString(statement) {
				bits |= accountChanged
			}
		}
		if pending, ok := tx.Statement.Context.Value(pendingChangesKey{}).(*pendingTableChanges); ok {
			pending.bits.Or(bits)
		} else {
			tracker.commit(bits)
		}
	}
	if err := database.Callback().Create().After("gorm:commit_or_rollback_transaction").Register(versionCallbackName, record); err != nil {
		return err
	}
	if err := database.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(versionCallbackName, record); err != nil {
		return err
	}
	if err := database.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register(versionCallbackName, record); err != nil {
		return err
	}
	if err := database.Callback().Raw().After("gorm:raw").Register(versionCallbackName, record); err != nil {
		return err
	}
	versionTrackers.Store(connection, tracker)
	return nil
}

// Used when an isolated database is closed (not while it has active workers).
func ForgetChangeTracking(database *gorm.DB) {
	connection, err := database.DB()
	if err == nil {
		versionTrackers.Delete(connection)
	}
}

func transactionWithTableChanges(database *gorm.DB, fn func(*gorm.DB) error) error {
	tracker := registeredVersionTracker(database)
	if tracker == nil {
		return database.Transaction(fn)
	}
	pending := &pendingTableChanges{}
	parent, _ := database.Statement.Context.Value(pendingChangesKey{}).(*pendingTableChanges)
	err := database.Transaction(func(tx *gorm.DB) error {
		return fn(tx.WithContext(context.WithValue(tx.Statement.Context, pendingChangesKey{}, pending)))
	})
	if err == nil {
		if parent != nil {
			parent.bits.Or(pending.bits.Load())
		} else {
			tracker.commit(pending.bits.Load())
		}
	}
	return err
}
