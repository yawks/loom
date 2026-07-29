package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func TestToModelMessage(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self",
		RefreshToken: "test-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.CacheDisplayName("8:orgid:alice", "Alice")

	created := time.Unix(1_700_000_000, 0)
	provider := NewProvider()
	message := provider.toModelMessage(client, msteams.Message{
		ID: "message-1", ThreadID: "thread-1", From: "8:orgid:alice",
		Content: "<p>Hello <strong>Teams</strong> and <em>italic</em></p>", ContentType: "html",
		MessageType: "RichText/Html", Created: created, ParentID: "parent-1",
		Attachments: []msteams.Attachment{{
			Name: "report.pdf", URL: "https://example.invalid/report.pdf",
			ContentType: "application/pdf", Size: 42,
		}},
	}, "thread-1")

	if message.ProtocolMsgID != "message-1" || message.ProtocolConvID != "thread-1" {
		t.Fatalf("unexpected identifiers: %+v", message)
	}
	if message.SenderName != "Alice" || message.IsFromMe {
		t.Fatalf("unexpected sender: %+v", message)
	}
	if !strings.Contains(message.Body, "**Teams**") || !strings.Contains(message.Body, "*italic*") {
		t.Fatalf("unexpected normalized body %q", message.Body)
	}
	if message.ThreadID != nil || message.QuotedMessageID == nil || *message.QuotedMessageID != "parent-1" {
		t.Fatalf("reply relation was not mapped: thread=%v quote=%v", message.ThreadID, message.QuotedMessageID)
	}
	if !strings.Contains(message.Attachments, "report.pdf") {
		t.Fatalf("attachment was not mapped: %s", message.Attachments)
	}
	if !message.Timestamp.Equal(created) {
		t.Fatalf("timestamp=%s, want %s", message.Timestamp, created)
	}
}

func TestReplyParentIsExtractedFromTeamsHTML(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	message := NewProvider().toModelMessage(client, msteams.Message{
		ID: "reply-1", ContentType: "html",
		Content: `<blockquote itemtype="http://schema.skype.com/Reply" itemid="parent-1">` +
			`quoted content</blockquote><p>Actual <i>reply</i></p>`,
	}, "thread-1")
	if message.QuotedMessageID == nil || *message.QuotedMessageID != "parent-1" {
		t.Fatalf("reply parent not extracted: %+v", message.QuotedMessageID)
	}
	if message.ThreadID != nil || strings.Contains(message.Body, "quoted content") || !strings.Contains(message.Body, "*reply*") {
		t.Fatalf("reply body not normalized: thread=%v body=%q", message.ThreadID, message.Body)
	}
}

func TestCapabilities(t *testing.T) {
	got := NewProvider().GetCapabilities()
	want := core.Capabilities{
		SupportsThreads: true, SupportsReactions: true,
		SupportsTypingIndicator: true, SupportsDeleteMessage: true,
		SupportsEditMessage: true, SupportsReadReceipts: true,
		NativeEmojiReactions: true,
	}
	if got != want {
		t.Fatalf("capabilities=%+v, want %+v", got, want)
	}
}

func TestSkippableHistoryError(t *testing.T) {
	if !isSkippableHistoryError(fmt.Errorf("fetch history: %w", msteams.ErrForbidden)) {
		t.Fatal("wrapped forbidden error should be skipped")
	}
	if !isSkippableHistoryError(fmt.Errorf("fetch history: %w", msteams.ErrNotFound)) {
		t.Fatal("wrapped not-found error should be skipped")
	}
	if isSkippableHistoryError(msteams.ErrUnauthorized) {
		t.Fatal("authentication errors must still stop synchronization")
	}
}

func TestMRILookupKey(t *testing.T) {
	first := mriLookupKey("8:orgid:7b645402-1edd-4874-8a0b-41bb2477353b")
	second := mriLookupKey("7B645402-1EDD-4874-8A0B-41BB2477353B")
	if first == "" || first != second {
		t.Fatalf("MRI keys differ: %q != %q", first, second)
	}
}

func TestVirtualTeamsThreads(t *testing.T) {
	for _, threadID := range []string{"48:calllogs", "48:mentions", "48:notifications"} {
		if !isVirtualTeamsThread(threadID) {
			t.Errorf("%q should be virtual", threadID)
		}
	}
	if isVirtualTeamsThread("19:group@thread.v2") {
		t.Fatal("normal group thread must not be filtered")
	}
}

func TestCallPayloadIsConvertedToCallMessage(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	provider := NewProvider()
	message := provider.toModelMessage(client, msteams.Message{
		ID: "call-1", ThreadID: "thread-1", From: "8:orgid:alice",
		MessageType: "Event/Call", Created: time.Unix(123, 0),
		Content: `<ended/><partlist><part><displayName>Alice</displayName><duration>1849</duration></part>` +
			`<part><displayName>Bob</displayName><duration>1849</duration></part></partlist>` +
			`<callEventType>callEnded</callEventType>` +
			`<conversationUrl>https://api.flightproxy.teams.microsoft.com/api/call</conversationUrl>` +
			`<a href="https://teams.microsoft.com/meet/123?p=abc&amp;x=1">Join</a>`,
	}, "thread-1")
	if message.CallOutcome != "CONNECTED" || message.CallDurationSecs == nil || *message.CallDurationSecs != 1849 {
		t.Fatalf("call summary was not parsed: %+v", message)
	}
	if message.CallType != "call_ended" {
		t.Fatalf("call type=%q, want call_ended", message.CallType)
	}
	if strings.Contains(message.Body, "partlist") {
		t.Fatalf("raw call payload leaked into body: %q", message.Body)
	}
	if !strings.Contains(message.CallParticipants, "Alice") || message.CallUrl != "https://teams.microsoft.com/meet/123?p=abc&x=1" {
		t.Fatalf("call metadata was not parsed: %+v", message)
	}
}

func TestIsMicrosoftFileURL(t *testing.T) {
	for _, fileURL := range []string{
		"https://api.asm.skype.com/v1/objects/file/views/original",
		"https://tenant.sharepoint.com/personal/user/Documents/report.pdf",
		"https://teams.microsoft.com/api/file",
	} {
		if !isMicrosoftFileURL(fileURL) {
			t.Errorf("expected Microsoft file URL: %s", fileURL)
		}
	}
	for _, fileURL := range []string{
		"https://example.com/file.pdf",
		"https://sharepoint.com.evil.example/file.pdf",
		"not-a-url",
	} {
		if isMicrosoftFileURL(fileURL) {
			t.Errorf("unexpected Microsoft file URL: %s", fileURL)
		}
	}
}

func TestTeamsAttachmentType(t *testing.T) {
	for _, test := range []struct {
		name, mimeType, want string
	}{
		{"photo.jpg", "", "image"},
		{"capture.png", "image/png", "image"},
		{"clip.mp4", "", "video"},
		{"report.pdf", "application/pdf", "document"},
	} {
		got, _ := teamsAttachmentType(test.name, test.mimeType)
		if got != test.want {
			t.Errorf("teamsAttachmentType(%q, %q)=%q, want %q", test.name, test.mimeType, got, test.want)
		}
	}
}

func TestTeamsHTMLTableToMarkdown(t *testing.T) {
	got := teamsHTMLToMarkdown(
		`<table><thead><tr><th>Name</th><th>Status</th></tr></thead>` +
			`<tbody><tr><td>Marco</td><td>External</td></tr></tbody></table>`,
	)
	for _, expected := range []string{
		"| Name | Status |",
		"| --- | --- |",
		"| Marco | External |",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("table markdown %q does not contain %q", got, expected)
		}
	}
}

func TestParticipantNamesFromMessagesIgnoresSelfAndTechnicalIDs(t *testing.T) {
	got := participantNamesFromMessages([]models.Message{
		{SenderID: "8:orgid:self", SenderName: "Me"},
		{SenderID: "8:orgid:marco", SenderName: "Marco CORVINI"},
		{SenderID: "8:orgid:unknown", SenderName: "8:orgid:unknown"},
	}, "8:orgid:self")
	if got != "Marco CORVINI" {
		t.Fatalf("participant name=%q, want Marco CORVINI", got)
	}
}
