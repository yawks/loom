package signal

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/backuppb"
	signalstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/util/dbutil"
)

type Provider struct {
	mu           sync.RWMutex
	config       core.ProviderConfig
	ctx          context.Context
	cancel       context.CancelFunc
	db           *dbutil.Database
	store        *signalstore.Container
	client       *signalmeow.Client
	events       chan core.ProviderEvent
	qrCode       string
	qrErr        error
	provisioning bool
	history      map[string][]models.Message
	transferMu   sync.Mutex
}

func NewProvider() core.Provider {
	return &Provider{events: make(chan core.ProviderEvent, 256), history: make(map[string][]models.Message)}
}

func (p *Provider) Init(config core.ProviderConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config
	return p.openStoreLocked()
}

func (p *Provider) openStoreLocked() error {
	if p.db != nil {
		return nil
	}
	instanceID, _ := p.config["_instance_id"].(string)
	if instanceID == "" {
		instanceID = "signal-1"
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("signal config directory: %w", err)
	}
	dir := filepath.Join(base, "Loom", instanceID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create signal data directory: %w", err)
	}
	raw, err := sql.Open("sqlite3", filepath.Join(dir, "signal.db")+"?_foreign_keys=on&_busy_timeout=10000")
	if err != nil {
		return fmt.Errorf("open signal store: %w", err)
	}
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		raw.Close()
		return fmt.Errorf("wrap signal store: %w", err)
	}
	store := signalstore.NewStore(db, dbutil.NoopLogger)
	ctx, cancel := context.WithCancel(context.Background())
	if err = store.Upgrade(ctx); err != nil {
		cancel()
		db.Close()
		return fmt.Errorf("upgrade signal store: %w", err)
	}
	p.ctx, p.cancel, p.db, p.store = ctx, cancel, db, store
	devices, err := store.GetAllDevices(ctx)
	if err != nil {
		return fmt.Errorf("load signal session: %w", err)
	}
	if len(devices) > 0 {
		p.setClientLocked(devices[0])
	}
	return nil
}

func (p *Provider) setClientLocked(device *signalstore.Device) {
	log := zerolog.Nop()
	p.client = signalmeow.NewClient(device, log, p.handleSignalEvent)
	p.client.SyncContactsOnConnect = true
}

func (p *Provider) GetConfig() core.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}
func (p *Provider) SetConfig(c core.ProviderConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = c
	return nil
}
func (p *Provider) IsAuthenticated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.client != nil && p.client.IsLoggedIn()
}

func (p *Provider) Connect() error {
	p.mu.Lock()
	if p.db == nil {
		if err := p.openStoreLocked(); err != nil {
			p.mu.Unlock()
			return err
		}
	}
	if p.ctx == nil {
		p.ctx, p.cancel = context.WithCancel(context.Background())
	}
	client := p.client
	if client == nil {
		if !p.provisioning {
			p.provisioning = true
			go p.runProvisioning(p.ctx)
		}
		p.mu.Unlock()
		return nil
	}
	ctx := p.ctx
	p.mu.Unlock()
	_, err := client.StartReceiveLoops(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("connect signal: %w", err)
	}
	return nil
}

func (p *Provider) ResetAuthentication() error {
	p.mu.Lock()
	client := p.client
	store := p.store
	p.mu.Unlock()

	// StopReceiveLoops waits for the event handler to finish. Never hold p.mu
	// here: an in-flight event calls instanceID(), which takes p.mu for reading.
	if client != nil {
		_ = client.StopReceiveLoops()
	}
	devices, err := store.GetAllDevices(context.Background())
	if err != nil {
		return err
	}
	for _, device := range devices {
		if err = store.DeleteDevice(context.Background(), &device.DeviceData); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.client, p.qrErr, p.qrCode = nil, nil, ""
	p.mu.Unlock()
	return nil
}

func (p *Provider) runProvisioning(ctx context.Context) {
	ch := signalmeow.PerformProvisioning(ctx, p.store, "Loom", true)
	for response := range ch {
		p.mu.Lock()
		switch response.State {
		case signalmeow.StateProvisioningURLReceived:
			p.qrCode, p.qrErr = response.ProvisioningURL, nil
		case signalmeow.StateProvisioningDataReceived:
			device, err := p.store.DeviceByACI(ctx, response.ProvisioningData.ACI)
			if err == nil && device != nil {
				p.setClientLocked(device)
				p.qrCode = ""
			}
			p.qrErr = err
		case signalmeow.StateProvisioningError:
			p.qrErr = response.Err
		}
		p.mu.Unlock()
	}
	p.mu.Lock()
	p.provisioning = false
	client := p.client
	p.mu.Unlock()
	if client != nil {
		_, _ = client.StartReceiveLoops(ctx)
	}
}

func (p *Provider) Disconnect() error {
	p.mu.Lock()
	client := p.client
	cancel := p.cancel
	if p.cancel != nil {
		p.cancel = nil
		p.ctx = nil
	}
	p.mu.Unlock()

	// The receive loops may be inside handleSignalEvent, which takes p.mu.
	// Waiting for them while holding that mutex deadlocks configuration reads
	// such as ProviderManager.GetConfiguredProviders.
	if cancel != nil {
		cancel()
	}
	if client != nil {
		_ = client.StopReceiveLoops()
	}
	return nil
}

func (p *Provider) StreamEvents() (<-chan core.ProviderEvent, error) { return p.events, nil }
func (p *Provider) SyncHistory(since time.Time) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return errors.New("signal is not paired")
	}
	if err := client.SendContactSyncRequest(context.Background()); err != nil {
		return err
	}
	return p.syncTransferHistory(since)
}

func (p *Provider) GetAuthQRCode() (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.qrErr != nil {
		return "", p.qrErr
	}
	return p.qrCode, nil
}

func (p *Provider) Cleanup() error {
	_ = p.Disconnect()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db != nil {
		_ = p.db.Close()
		p.db = nil
	}
	instanceID, _ := p.config["_instance_id"].(string)
	if instanceID == "" {
		return nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(base, "Loom", instanceID))
}

func (p *Provider) GetContacts() ([]models.LinkedAccount, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return []models.LinkedAccount{}, nil
	}
	recipients, err := client.Store.RecipientStore.LoadAllContacts(context.Background())
	if err != nil {
		return nil, err
	}
	instanceID := p.instanceID()
	out := make([]models.LinkedAccount, 0, len(recipients))
	for _, r := range recipients {
		id := r.ACI.String()
		if r.ACI.String() == "00000000-0000-0000-0000-000000000000" {
			id = "PNI:" + r.PNI.String()
		}
		name := r.ContactName
		if name == "" {
			name = r.Nickname
		}
		if name == "" {
			name = r.Profile.Name
		}
		if name == "" {
			name = r.E164
		}
		out = append(out, models.LinkedAccount{Protocol: "signal", ProviderInstanceID: instanceID, UserID: id, Username: name, Status: "offline", ConversationID: core.BuildConvID(instanceID, id)})
	}
	// Signal's transfer archive is authoritative for the conversation list. It
	// includes groups and chats whose peer is not present in the regular contact
	// sync (for example an old message request).
	chats, err := client.Store.BackupStore.GetBackupChats(context.Background())
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(out))
	for _, account := range out {
		seen[account.UserID] = true
	}
	for _, chat := range chats {
		recipient, loadErr := client.Store.BackupStore.GetBackupRecipient(context.Background(), chat.RecipientId)
		if loadErr != nil || recipient == nil {
			continue
		}
		id, name, isGroup := backupConversationIdentity(recipient)
		if recipient.GetSelf() != nil {
			id = client.Store.ACI.String()
		}
		if id == "" || seen[id] {
			continue
		}
		if name == "" {
			name = id
		}
		out = append(out, models.LinkedAccount{Protocol: "signal", ProviderInstanceID: instanceID, UserID: id, Username: name, Status: "offline", IsGroup: isGroup, ConversationID: core.BuildConvID(instanceID, id)})
		seen[id] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

func backupConversationIdentity(recipient *backuppb.Recipient) (id, name string, isGroup bool) {
	switch dest := recipient.GetDestination().(type) {
	case *backuppb.Recipient_Contact:
		if len(dest.Contact.GetAci()) == 16 {
			id = libsignalgo.NewACIServiceID(uuid.UUID(dest.Contact.GetAci())).String()
		}
		if id == "" && len(dest.Contact.GetPni()) == 16 {
			id = libsignalgo.NewPNIServiceID(uuid.UUID(dest.Contact.GetPni())).String()
		}
		name = dest.Contact.GetProfileGivenName()
		if family := dest.Contact.GetProfileFamilyName(); family != "" {
			name = strings.TrimSpace(name + " " + family)
		}
		if name == "" && dest.Contact.GetE164() != 0 {
			name = fmt.Sprintf("+%d", dest.Contact.GetE164())
		}
	case *backuppb.Recipient_Self:
		id = "self"
		name = "Note to Self"
	case *backuppb.Recipient_Group:
		if len(dest.Group.GetMasterKey()) != libsignalgo.GroupMasterKeyLength {
			return "", "", false
		}
		groupID, err := libsignalgo.GroupMasterKey(dest.Group.GetMasterKey()).GroupIdentifier()
		if err != nil {
			return "", "", false
		}
		id = base64.StdEncoding.EncodeToString(groupID[:])
		name = dest.Group.GetSnapshot().GetTitle().GetTitle()
		isGroup = true
	}
	return
}

func (p *Provider) GetContactName(id string) (string, error) {
	contacts, err := p.GetContacts()
	if err != nil {
		return "", err
	}
	for _, c := range contacts {
		if c.UserID == core.StripConvID(id) {
			return c.Username, nil
		}
	}
	return "", fmt.Errorf("signal contact not found: %s", id)
}
func (p *Provider) RefreshContact(string) error { return p.SyncHistory(time.Time{}) }

func (p *Provider) GetConversationHistory(id string, limit int, before, since *time.Time) ([]models.Message, error) {
	p.mu.RLock()
	src := append([]models.Message(nil), p.history[core.StripConvID(id)]...)
	p.mu.RUnlock()
	out := src[:0]
	for _, m := range src {
		if before != nil && !m.Timestamp.Before(*before) {
			continue
		}
		if since != nil && m.Timestamp.Before(*since) {
			continue
		}
		out = append(out, m)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (p *Provider) GetCapabilities() core.Capabilities {
	return core.Capabilities{
		SupportsReactions: true, SupportsTypingIndicator: true, SupportsDeleteMessage: true, SupportsEditMessage: true,
		SupportsReadReceipts: true, SupportsQRCodeAuth: true, NativeEmojiReactions: true,
		ReadCursorAuthoritativeForNewMessages: true, OwnActivityAdvancesReadBoundary: true,
		SupportsContactDirectory: true, SupportsDirectConversation: true, SupportsPhoneNumberRecipient: false,
		SupportsGroupConversation: false,
	}
}
func (p *Provider) GetCustomEmojis() (map[string]string, error) { return nil, nil }
func (p *Provider) instanceID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	id, _ := p.config["_instance_id"].(string)
	return id
}

func (p *Provider) emit(event core.ProviderEvent) {
	select {
	case p.events <- event:
	default:
	}
}
func (p *Provider) remember(m models.Message) {
	p.mu.Lock()
	key := core.StripConvID(m.ProtocolConvID)
	for index := range p.history[key] {
		if p.history[key][index].ProtocolMsgID == m.ProtocolMsgID {
			p.history[key][index] = m
			p.mu.Unlock()
			return
		}
	}
	p.history[key] = append(p.history[key], m)
	p.mu.Unlock()
}

func (p *Provider) handleSignalEvent(raw events.SignalEvent) bool {
	switch evt := raw.(type) {
	case *events.ChatEvent:
		p.handleChatEvent(evt)
	case *events.ContactList:
		// Receiving a contact list is a completed data notification, not the
		// beginning of a long-running synchronization phase. Reporting
		// fetching_contacts here leaves the global footer stuck in an active
		// state while subsequent catch-up messages are delivered.
		p.emit(core.ContactStatusEvent{InstanceID: p.instanceID(), UserID: "refresh", Status: "new_conversations_discovered"})
	case *events.Receipt:
		p.handleReceipt(evt)
	case *events.ReadSelf:
		p.handleReadSelf(evt)
	case *events.LoggedOut:
		p.emit(core.SyncStatusEvent{InstanceID: p.instanceID(), Status: core.SyncStatusNeedsReauth, Message: evt.Error.Error(), Progress: -1})
	}
	return true
}
