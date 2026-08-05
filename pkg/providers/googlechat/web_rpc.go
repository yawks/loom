package googlechat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

const (
	googleChatWebOrigin    = "https://chat.google.com"
	createUnsentMessageURL = "https://chat.google.com/u/0/api/create_unsent_message?c=32"
)

// webAPIHeaderMeta is the fixed metadata header sent in all Google Chat web API calls.
var webAPIHeaderMeta = []interface{}{
	0, 7, 1, "fr",
	[]interface{}{
		nil, nil, nil, nil, 2, 2, nil, 2, 2, 2, 2, nil, nil, nil, nil, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, nil, nil, 2, 2, nil, nil, nil, 2, 2, nil, nil, nil, nil, 2, 2, 2, 2, nil, 2, nil, nil, 2, nil, 2, 2, 2, 2, nil, 2, nil, 2, 2, 2, 2, nil, 2, 2, 2, 2, 2, 2, nil, 2,
	},
}

// unsentMsgRef holds the identifiers needed to cancel a scheduled message via
// delete_unsent_message. The payload format matches the browser: buildSpaceRef(webSpaceID, threadID).
type unsentMsgRef struct {
	webSpaceID string // web-client space ID (e.g. "D6l_zcg-ggw"), first arg to buildSpaceRef
	threadID   string // web-client thread ID (e.g. "-zrGTiAAAAE"), used in the thread-ref
}

// unsentMessageEntry is the parsed representation of one message from a
// create_unsent_message or list_unsent_messages API response.
type unsentMessageEntry struct {
	WebSpaceID  string
	ThreadID    string
	Text        string
	ScheduledAt time.Time
	MsgKey      string // timestamp-derived key (kept for use as localID)
}

type googleChatWebClient struct {
	mu            sync.RWMutex
	cookies       map[string]string
	httpClient    *http.Client
	instanceID    string
	onAuthExpired func()

	// scheduledMu guards scheduledByConv and serverRefByID.
	scheduledMu     sync.RWMutex
	scheduledByConv map[string][]models.ScheduledMessage // rawConvID → messages
	serverRefByID   map[string]unsentMsgRef              // ScheduledMessage.ID → delete ref
}

func newGoogleChatWebClient(instanceID string, cookies map[string]string, onAuthExpired func()) *googleChatWebClient {
	return &googleChatWebClient{
		cookies:         cookies,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		instanceID:      instanceID,
		onAuthExpired:   onAuthExpired,
		scheduledByConv: make(map[string][]models.ScheduledMessage),
		serverRefByID:   make(map[string]unsentMsgRef),
	}
}

func (w *googleChatWebClient) SetCookies(cookies map[string]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cookies = cookies
}

func (w *googleChatWebClient) HasCookies() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cookies["SAPISID"] != ""
}

func (w *googleChatWebClient) refreshWebSession() error {
	w.mu.RLock()
	snapshot := make(map[string]string, len(w.cookies))
	for key, value := range w.cookies {
		snapshot[key] = value
	}
	w.mu.RUnlock()
	updated, err := SilentRefreshWebSession(context.Background(), snapshot)
	if err != nil {
		return err
	}
	w.mu.Lock()
	for key, value := range updated {
		w.cookies[key] = value
	}
	w.mu.Unlock()
	return nil
}

func (w *googleChatWebClient) generateSAPISIDHash() (string, string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	sapisid := w.cookies["SAPISID"]
	if sapisid == "" {
		return "", "", fmt.Errorf("missing SAPISID cookie")
	}

	ts := fmt.Sprintf("%d", time.Now().Unix())
	hashInput := ts + " " + sapisid + " " + googleChatWebOrigin
	h := sha1.New()
	h.Write([]byte(hashInput))
	hashHex := hex.EncodeToString(h.Sum(nil))

	authHeader := fmt.Sprintf("SAPISIDHASH %s_%s", ts, hashHex)

	var cookieParts []string
	for k, v := range w.cookies {
		if k != "X-Framework-Xsrf-Token" {
			cookieParts = append(cookieParts, k+"="+v)
		}
	}
	cookieHeader := strings.Join(cookieParts, "; ")

	return authHeader, cookieHeader, nil
}

// webAPIPost sends a JSON POST to a Google Chat undocumented web API endpoint.
// endpoint is the API name (e.g. "create_unsent_message"); the URL is derived
// from the X-API-URL-Base cookie captured during Gaia browser login.
func (w *googleChatWebClient) webAPIPost(endpoint string, payload interface{}) ([]byte, error) {
	return w.webAPIPostWithRetry(endpoint, payload, true)
}

func (w *googleChatWebClient) webAPIPostWithRetry(endpoint string, payload interface{}, retry bool) ([]byte, error) {
	_, cookieHeader, err := w.generateSAPISIDHash()
	if err != nil {
		return nil, err
	}

	w.mu.RLock()
	xsrfToken := w.cookies["X-Framework-Xsrf-Token"]
	apiURLBase := w.cookies["X-API-URL-Base"]
	w.mu.RUnlock()

	if xsrfToken == "" {
		return nil, fmt.Errorf("google chat web session requires X-Framework-Xsrf-Token — please click 'Re-connecter' in provider settings to refresh web session")
	}
	if apiURLBase == "" {
		apiURLBase = createUnsentMessageURL
	}
	apiURL := strings.Replace(apiURLBase, "create_unsent_message", endpoint, 1)

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", endpoint, err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "fr")
	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("Origin", googleChatWebOrigin)
	req.Header.Set("Referer", googleChatWebOrigin+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:153.0) Gecko/20100101 Firefox/153.0")
	req.Header.Set("X-Framework-Xsrf-Token", xsrfToken)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s HTTP error: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if w.onAuthExpired != nil {
			w.onAuthExpired()
		}
		return nil, fmt.Errorf("google chat session expired (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound && retry {
		if refreshErr := w.refreshWebSession(); refreshErr != nil {
			return nil, fmt.Errorf("google chat %s returned HTTP 404 and session refresh failed: %w", endpoint, refreshErr)
		}
		return w.webAPIPostWithRetry(endpoint, payload, false)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google chat %s returned HTTP %d: %s", endpoint, resp.StatusCode, string(body))
	}
	return body, nil
}

// parseUnsentMessages parses the response body of create_unsent_message or
// list_unsent_messages.
//
// The response envelope is:
//
//	)]}'
//	[["dfe.rs.lum", [msg0, msg1, ...], ["key0", ...]]]
//
// op[1] is either:
//   - A list of messages (list response): each element is a 9-field message array.
//   - The fields of a single message (create response): the array itself has 9+ fields.
//
// op[2] holds the keys array (one entry per message, may be absent or shorter than
// the message count).
func parseUnsentMessages(body []byte) []unsentMessageEntry {
	data := bytes.TrimSpace(body)
	data = bytes.TrimPrefix(data, []byte(")]}'"))
	data = bytes.TrimSpace(data)

	var raw []interface{}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return nil
	}
	op, ok := raw[0].([]interface{})
	if !ok || len(op) < 2 {
		return nil
	}

	// op[1] = messages payload
	msgsRaw, ok := op[1].([]interface{})
	if !ok {
		return nil
	}

	// Determine format:
	//   List format:   op[1] = [msg0, msg1, …]  →  msgsRaw[0] is a message (len ≥ 8)
	//   Create format: op[1] = [spaceRef, ts, …] →  msgsRaw[0] is a spaceRef (len < 8)
	var msgList [][]interface{}
	if len(msgsRaw) > 0 {
		if first, ok2 := msgsRaw[0].([]interface{}); ok2 && len(first) >= 8 {
			// List: every element of msgsRaw is a message.
			for _, m := range msgsRaw {
				if msg, ok3 := m.([]interface{}); ok3 && len(msg) >= 8 {
					msgList = append(msgList, msg)
				}
			}
		} else {
			// Create: msgsRaw itself is the single message's field array.
			if len(msgsRaw) >= 8 {
				msgList = append(msgList, msgsRaw)
			}
		}
	}

	var result []unsentMessageEntry
	for _, msg := range msgList {
		// msg[0]: spaceRef = [webSpaceID, threadRef]
		// threadRef = [null, null, [threadID]]
		// Browser-created: webSpaceID ≠ threadID → inner thread ID is at threadRef[2][0].
		// Loom-created (older): webSpaceID = threadID → fall back to spaceRef[0].
		spaceRef, ok := msg[0].([]interface{})
		if !ok || len(spaceRef) == 0 {
			continue
		}
		webSpaceID, _ := spaceRef[0].(string)
		threadID := ""
		if len(spaceRef) >= 2 {
			if threadRef, ok2 := spaceRef[1].([]interface{}); ok2 && len(threadRef) >= 3 {
				if innerArr, ok3 := threadRef[2].([]interface{}); ok3 && len(innerArr) > 0 {
					threadID, _ = innerArr[0].(string)
				}
			}
		}
		if threadID == "" {
			threadID = webSpaceID
		}

		// msg[3]: message text
		text, _ := msg[3].(string)

		// msg[7]: scheduledDelivery = [[unixTimestamp], priority]
		var scheduledAt time.Time
		if sched, ok := msg[7].([]interface{}); ok && len(sched) > 0 {
			if tsArr, ok := sched[0].([]interface{}); ok && len(tsArr) > 0 {
				if ts, ok := tsArr[0].(float64); ok {
					scheduledAt = time.Unix(int64(ts), 0)
				}
			}
		}

		// Derive msgKey from creation timestamp: seconds * 1_000_000 + nanoseconds / 1_000.
		// The list response keysArr is a response cursor token, not a per-message key.
		// The create response keysArr[0] happens to match, but timestamp derivation is
		// simpler and correct for both cases.
		msgKey := ""
		if createTs, ok2 := msg[1].([]interface{}); ok2 && len(createTs) >= 2 {
			if sec, ok3 := createTs[0].(float64); ok3 {
				var usec int64
				if nano, ok4 := createTs[1].(float64); ok4 {
					usec = int64(nano) / 1000
				}
				msgKey = fmt.Sprintf("%d%06d", int64(sec), usec)
			}
		}

		result = append(result, unsentMessageEntry{
			ThreadID:    threadID,
			WebSpaceID:  webSpaceID,
			Text:        text,
			ScheduledAt: scheduledAt,
			MsgKey:      msgKey,
		})
	}
	return result
}

// buildSpaceRef builds the spaceRef array used in web API payloads.
// When threadID is non-empty: [spaceID, [null, null, [threadID]]]
// When threadID is empty:    [spaceID, null]
func buildSpaceRef(spaceID, threadID string) interface{} {
	if threadID != "" {
		return []interface{}{spaceID, []interface{}{nil, nil, []interface{}{threadID}}}
	}
	return []interface{}{spaceID, nil}
}

func newUnsentMessageID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate unsent message ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func buildCreateUnsentMessagePayload(unsentMessageID, webSpaceID, parentMsgWebID, text string, scheduledAt time.Time) []interface{} {
	// Browser shape: [fresh draft ID, [null, null, [stable space ID]], optional parent message ID].
	spaceRef := buildSpaceRef(unsentMessageID, webSpaceID)
	if parentMsgWebID != "" {
		spaceRef = append(spaceRef.([]interface{}), parentMsgWebID)
	}
	msgContent := []interface{}{
		spaceRef,
		nil, nil,
		text,
		nil, nil, 1,
		[]interface{}{[]interface{}{scheduledAt.Unix()}},
		"",
	}
	return []interface{}{webAPIHeaderMeta, msgContent}
}

// ScheduleMessage queues a text message on Google Chat servers via Google Chat Web API.
// webSpaceID is the web client space ID (e.g. "_q55RHW5d6o", NOT the REST API "spaces/XXX" suffix).
// threadID is empty for top-level group space messages; for DMs and thread replies it is the
// thread's web client ID (same value as the REST space ID suffix for DMs).
// parentMsgWebID is the web client ID of the thread-parent message (non-empty for thread replies
// in group spaces only). It becomes the 3rd element of the spaceRef array.
// rawConvID is the REST-format conversation ID used to tag the returned ScheduledMessage.
func (w *googleChatWebClient) ScheduleMessage(webSpaceID, threadID, parentMsgWebID, text, rawConvID string, scheduledAt time.Time) (*models.ScheduledMessage, error) {
	if !w.HasCookies() {
		return nil, fmt.Errorf("google chat web session cookies required for scheduled messages — please log in via Gaia browser")
	}
	if !scheduledAt.After(time.Now()) {
		return nil, fmt.Errorf("scheduled time must be in the future")
	}

	unsentMessageID, err := newUnsentMessageID()
	if err != nil {
		return nil, err
	}
	payload := buildCreateUnsentMessagePayload(unsentMessageID, webSpaceID, parentMsgWebID, text, scheduledAt)
	if _, err := w.webAPIPost("create_unsent_message", payload); err != nil {
		return nil, fmt.Errorf("google chat schedule error: %w", err)
	}

	localID := fmt.Sprintf("gc_sched_%d", time.Now().UnixNano())
	msg := models.ScheduledMessage{
		ID:                 localID,
		ProviderInstanceID: w.instanceID,
		ProtocolConvID:     core.BuildConvID(w.instanceID, rawConvID),
		Body:               text,
		ScheduledAt:        scheduledAt,
		CreatedAt:          time.Now(),
	}

	w.scheduledMu.Lock()
	w.scheduledByConv[rawConvID] = append(w.scheduledByConv[rawConvID], msg)
	w.serverRefByID[localID] = unsentMsgRef{webSpaceID: unsentMessageID, threadID: webSpaceID}
	w.scheduledMu.Unlock()

	return &msg, nil
}

// ListScheduledMessages fetches pending scheduled messages for this conversation from
// the Google Chat server. Falls back to the local in-memory store on error.
// threadID is the DM/thread web-client ID used to filter the server response.
func (w *googleChatWebClient) ListScheduledMessages(rawConvID, webSpaceID, threadID string) ([]models.ScheduledMessage, error) {
	payload := []interface{}{webAPIHeaderMeta, []interface{}{}, []interface{}{nil, nil, 1, nil, 1}}
	respBytes, err := w.webAPIPost("list_unsent_messages", payload)
	if err != nil {
		// Fall back to in-memory store (messages created this session).
		w.scheduledMu.RLock()
		msgs := w.scheduledByConv[rawConvID]
		out := make([]models.ScheduledMessage, len(msgs))
		copy(out, msgs)
		w.scheduledMu.RUnlock()
		return out, nil
	}

	entries := parseUnsentMessages(respBytes)

	out := make([]models.ScheduledMessage, 0, len(entries))
	w.scheduledMu.Lock()
	for _, entry := range entries {
		if entry.ThreadID != threadID {
			continue
		}
		localID := entry.MsgKey
		if localID == "" {
			localID = fmt.Sprintf("gc_sched_%s_%d", entry.ThreadID, entry.ScheduledAt.Unix())
		}
		msg := models.ScheduledMessage{
			ID:                 localID,
			ProviderInstanceID: w.instanceID,
			ProtocolConvID:     core.BuildConvID(w.instanceID, rawConvID),
			Body:               entry.Text,
			ScheduledAt:        entry.ScheduledAt,
		}
		// Keep serverRefByID current so CancelScheduledMessage can delete by this ID.
		w.serverRefByID[localID] = unsentMsgRef{webSpaceID: entry.WebSpaceID, threadID: entry.ThreadID}
		out = append(out, msg)
	}
	w.scheduledMu.Unlock()

	return out, nil
}

// CancelScheduledMessage deletes a scheduled message from Google Chat servers and
// removes it from the local store.
func (w *googleChatWebClient) CancelScheduledMessage(rawConvID, scheduledMessageID string) error {
	w.scheduledMu.Lock()
	ref, hasRef := w.serverRefByID[scheduledMessageID]
	msgs := w.scheduledByConv[rawConvID]
	for i, m := range msgs {
		if m.ID == scheduledMessageID {
			w.scheduledByConv[rawConvID] = append(msgs[:i], msgs[i+1:]...)
			break
		}
	}
	delete(w.serverRefByID, scheduledMessageID)
	w.scheduledMu.Unlock()

	if !hasRef {
		return nil
	}

	// The delete payload matches the browser: [headerMeta, buildSpaceRef(webSpaceID, threadID)]
	spaceRef := buildSpaceRef(ref.webSpaceID, ref.threadID)
	if _, err := w.webAPIPost("delete_unsent_message", []interface{}{webAPIHeaderMeta, spaceRef}); err != nil {
		return fmt.Errorf("google chat cancel scheduled message: %w", err)
	}
	return nil
}
