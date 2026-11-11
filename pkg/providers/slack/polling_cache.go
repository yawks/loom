package slack

import (
	"Loom/pkg/db"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

type slackPollingSnapshot struct {
	mu            sync.Mutex
	database      *sql.DB
	instanceID    string
	versions      db.TableVersions
	expiresAt     time.Time
	conversations []slackFallbackConversation
}

// Idle ticks read only memory. Any committed message/account mutation rearms
// the authoritative provider-scoped queries on the next tick. Table-wide
// versions deliberately invalidate conservatively for unknown/bulk writes;
// periodic reconciliation also covers writes from outside this process.
func (p *SlackProvider) cachedFallbackConversations(database *gorm.DB) ([]slackFallbackConversation, error) {
	instanceID := strings.TrimSpace(p.getInstanceId())
	if instanceID == "" {
		return nil, fmt.Errorf("Slack polling requires a provider instance ID")
	}
	versions, tracked := db.CurrentTableVersions(database)
	if !tracked {
		return slackFallbackConversations(database, instanceID)
	}
	connection, err := database.DB()
	if err != nil {
		return nil, err
	}
	cache := &p.pollingSnapshot
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.database == connection && cache.instanceID == instanceID && cache.versions == versions && time.Now().Before(cache.expiresAt) {
		return append([]slackFallbackConversation(nil), cache.conversations...), nil
	}
	conversations, err := slackFallbackConversations(database, instanceID)
	if err != nil {
		return nil, err
	}
	cache.database, cache.instanceID, cache.versions = connection, instanceID, versions
	cache.conversations = append([]slackFallbackConversation(nil), conversations...)
	cache.expiresAt = time.Now().Add(time.Minute)
	// Do not overwrite a commit which raced the snapshot query.
	if after, _ := db.CurrentTableVersions(database); after != versions {
		cache.expiresAt = time.Time{}
	}
	return conversations, nil
}
