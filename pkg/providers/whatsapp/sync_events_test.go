package whatsapp

import (
	"Loom/pkg/models"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestSplitHistoryMessagesByUnreadCount(t *testing.T) {
	messages := []models.Message{{ProtocolMsgID: "read-1"}, {ProtocolMsgID: "read-2"}, {ProtocolMsgID: "unread"}}
	read, unread := splitHistoryMessagesByUnreadCount(messages, 1, false)
	if len(read) != 2 || len(unread) != 1 || unread[0].ProtocolMsgID != "unread" {
		t.Fatalf("split = read:%v unread:%v", read, unread)
	}

	read, unread = splitHistoryMessagesByUnreadCount(messages, 0, false)
	if len(read) != len(messages) || len(unread) != 0 {
		t.Fatalf("zero unread split = read:%d unread:%d", len(read), len(unread))
	}

	read, unread = splitHistoryMessagesByUnreadCount(messages, 0, true)
	if len(read) != 0 || len(unread) != len(messages) {
		t.Fatalf("on-demand split = read:%d unread:%d", len(read), len(unread))
	}
}

func TestHistoryMessagesReadThroughOwnMessage(t *testing.T) {
	messages := []models.Message{
		{ProtocolMsgID: "incoming-before"},
		{ProtocolMsgID: "own", IsFromMe: true},
		{ProtocolMsgID: "incoming-after"},
	}

	read := historyMessagesReadThroughOwnMessage(messages)
	if len(read) != 2 || read[0].ProtocolMsgID != "incoming-before" || read[1].ProtocolMsgID != "own" {
		t.Fatalf("read-through prefix = %#v", read)
	}
	if got := historyMessagesReadThroughOwnMessage([]models.Message{{ProtocolMsgID: "incoming"}}); got != nil {
		t.Fatalf("prefix without own message = %#v, want nil", got)
	}
}

func TestReclassifyNewIncomingWhatsAppMessages(t *testing.T) {
	read := []models.Message{
		{ProtocolMsgID: "existing", Timestamp: time.Unix(1, 0)},
		{ProtocolMsgID: "new-incoming", Timestamp: time.Unix(2, 0)},
		{ProtocolMsgID: "new-own", IsFromMe: true, Timestamp: time.Unix(3, 0)},
		{ProtocolMsgID: "forced", Timestamp: time.Unix(4, 0)},
	}
	newIDs := map[string]struct{}{"new-incoming": {}, "new-own": {}, "forced": {}}
	forcedIDs := map[string]struct{}{"forced": {}}

	read, unread := reclassifyNewIncomingWhatsAppMessages(read, nil, newIDs, forcedIDs)
	if len(read) != 3 {
		t.Fatalf("read messages = %#v", read)
	}
	if len(unread) != 1 || unread[0].ProtocolMsgID != "new-incoming" {
		t.Fatalf("unread messages = %#v", unread)
	}
}

func TestCacheJoinedGroupMakesGroupImmediatelyDiscoverable(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	oldCacheTime := time.Now().Add(-time.Minute)
	provider.groupsCacheTimestamp = &oldCacheTime
	jid := types.NewJID("120363000000000000", types.GroupServer)

	provider.cacheJoinedGroup(jid, "Club Med Bodrum")

	group, ok := provider.conversations[jid.String()]
	if !ok || group.Username != "Club Med Bodrum" || !group.IsGroup {
		t.Fatalf("cached conversation = %#v, found=%v", group, ok)
	}
	if provider.knownGroups[jid.String()] != "Club Med Bodrum" {
		t.Fatalf("known group name = %q", provider.knownGroups[jid.String()])
	}
	if len(provider.groupsCache) != 1 || provider.groupsCache[0].ProviderInstanceID != "whatsapp-1" {
		t.Fatalf("groups cache = %#v", provider.groupsCache)
	}
	provider.conversationMessages["whatsapp-1::existing@s.whatsapp.net"] = []models.Message{{ProtocolMsgID: "existing"}}
	filtered := provider.filterAccountsWithHistory([]models.LinkedAccount{group})
	if len(filtered) != 1 || filtered[0].UserID != jid.String() {
		t.Fatalf("new group without messages was filtered out: %#v", filtered)
	}
}
