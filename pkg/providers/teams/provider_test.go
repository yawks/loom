package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"encoding/base64"
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

func TestTeamsAdaptiveCardReplacesUnsupportedPlaceholder(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	card := `[{"type":"AdaptiveCard","content":{"type":"AdaptiveCard","body":[` +
		`{"type":"TextBlock","text":"Deployment complete"},` +
		`{"type":"FactSet","facts":[{"title":"Environment:","value":"Production"}]}` +
		`],"actions":[{"type":"Action.OpenUrl","title":"Open dashboard","url":"https://example.invalid/dashboard"}]}}]`
	message := NewProvider().toModelMessage(client, msteams.Message{
		ID: "card-1", ContentType: "html", MessageType: "RichText/Html",
		Content:    `<p>Card - access it on <a href="https://go.skype.com/cards.unsupported">https://go.skype.com/cards.unsupported</a>. Card</p>`,
		Properties: map[string]any{"cards": card},
	}, "thread-1")

	for _, expected := range []string{"Deployment complete", "**Environment:** Production", "[Open dashboard](https://example.invalid/dashboard)"} {
		if !strings.Contains(message.Body, expected) {
			t.Errorf("card body %q does not contain %q", message.Body, expected)
		}
	}
	if strings.Contains(message.Body, "cards.unsupported") {
		t.Fatalf("unsupported placeholder was retained: %q", message.Body)
	}
}

func TestTeamsSwiftAdaptiveCardReplacesUnsupportedPlaceholder(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	activity := `{"type":"message","attachments":[{"contentType":"application/vnd.microsoft.card.adaptive","content":{"type":"AdaptiveCard","body":[{"type":"TextBlock","text":"Workflow completed"}]}}]}`
	encoded := base64.StdEncoding.EncodeToString([]byte(activity))
	message := NewProvider().toModelMessage(client, msteams.Message{
		ID: "swift-card-1", ContentType: "text", MessageType: "RichText/Media_Card",
		Content: `<URIObject type="SWIFT.1"><a href="https://go.skype.com/cards.unsupported">Card</a>` +
			`<Swift b64="` + encoded + `"></Swift><Description>Card</Description></URIObject>`,
	}, "thread-1")

	if message.Body != "Workflow completed" {
		t.Fatalf("swift card body=%q", message.Body)
	}
	if !strings.Contains(message.Attachments, `"type":"teams_card"`) ||
		!strings.Contains(message.Attachments, `"cardJson"`) {
		t.Fatalf("structured card attachment was not retained: %s", message.Attachments)
	}
}

func TestCapabilities(t *testing.T) {
	got := NewProvider().GetCapabilities()
	want := core.Capabilities{
		SupportsThreads: false, SupportsReactions: true,
		SupportsTypingIndicator: true, SupportsDeleteMessage: true,
		SupportsEditMessage: true, SupportsReadReceipts: true,
		SupportsPinMessage: true, SupportsListMessagePins: true,
		SupportsScheduledMessages: true, SupportsListScheduledMessages: true,
		MessagePinScope:            string(models.MessagePinScopeShared),
		SupportsGroupManagement:    true,
		SupportsAddGroupMembers:    true,
		SupportsRemoveGroupMembers: true,
		SupportsRenameGroup:        true,
		SupportsGroupDescription:   true,
		SupportsGroupAdminRoles:    true,
		SupportsLeaveGroup:         true,
		NativeEmojiReactions:       true,
		SupportsContactDirectory:   true, SupportsDirectConversation: true,
		SupportsGroupConversation: true, SupportsGroupTitle: true,
		GroupConversationTypes: "group",
	}
	if got != want {
		t.Fatalf("capabilities=%+v, want %+v", got, want)
	}
}

func TestTypingEventUsesCanonicalConversationID(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.CacheDisplayName("8:orgid:alice", "Alice")

	provider := NewProvider()
	provider.instance = "teams-work"
	provider.handleRemoteEvent(client, msteams.Event{
		Type: msteams.EventTypeTyping, ThreadID: "19:group@thread.v2", TypingFrom: "8:orgid:alice",
	})

	event := (<-provider.eventChan).(core.TypingEvent)
	if event.ConversationID != "teams-work::19:group@thread.v2" || event.UserName != "Alice" || !event.IsTyping {
		t.Fatalf("unexpected typing event: %+v", event)
	}
}

func TestSendMessageRejectsEmptyPayload(t *testing.T) {
	if _, err := NewProvider().SendMessage("conversation", "", nil, nil); err == nil {
		t.Fatal("empty Teams message should be rejected before reaching the service")
	}
}

func TestTeamsUserMRIRequiresDirectConversationResolution(t *testing.T) {
	for _, id := range []string{"8:orgid:alice", "1:alice", "4:alice", "28:bot"} {
		if !isTeamsUserMRI(id) {
			t.Fatalf("%q should be recognized as a user MRI", id)
		}
	}
	for _, id := range []string{"19:alice_me@unq.gbl.spaces", "19:group@thread.v2", "teams-1::19:chat@thread.v2"} {
		if isTeamsUserMRI(id) {
			t.Fatalf("%q should be recognized as a conversation MRI", id)
		}
	}
}

func TestTeamsDMParticipantMRI(t *testing.T) {
	threadID := "19:6815e2df-8147-4ab5-8d28-b935a253334a_cd0ce28e-581e-422a-a157-7427f06e3496@unq.gbl.spaces"
	got := teamsDMParticipantMRI(threadID, "8:orgid:6815e2df-8147-4ab5-8d28-b935a253334a")
	if got != "8:orgid:cd0ce28e-581e-422a-a157-7427f06e3496" {
		t.Fatalf("teamsDMParticipantMRI()=%q", got)
	}
}

func TestThreadsAreUnsupported(t *testing.T) {
	provider := NewProvider()
	threadID := "parent"
	if _, err := provider.SendMessage("conversation", "body", nil, &threadID); err == nil {
		t.Fatal("SendMessage with a thread ID should be rejected")
	}
	if _, err := provider.SendThreadReply("conversation", "body", threadID, "quoted"); err == nil {
		t.Fatal("SendThreadReply should be rejected")
	}
}

func TestInlineReplyHTMLUsesQuoteWithoutThreadParent(t *testing.T) {
	body := NewProvider().inlineReplyHTML("conversation", "message-1")
	if parent := msteams.ExtractReplyParent(body); parent != "message-1" {
		t.Fatalf("inline reply parent=%q, want message-1", parent)
	}
	if !strings.Contains(body, `itemtype="http://schema.skype.com/Reply"`) {
		t.Fatalf("inline reply HTML=%q", body)
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
	for _, threadID := range []string{"48:calllogs", "48:mentions", "48:notifications", "48:notes", "48:drafts"} {
		if !isVirtualTeamsThread(threadID) {
			t.Errorf("%q should be virtual", threadID)
		}
	}
	if isVirtualTeamsThread("19:group@thread.v2") {
		t.Fatal("normal group thread must not be filtered")
	}
}

func TestCreatedUnnamedGroupDisplayName(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self", SkypeToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.CacheDisplayName("8:orgid:alice", "Alice Martin")
	client.CacheDisplayName("8:orgid:bob", "Bob Durand")
	got := NewProvider().createdGroupDisplayName(client, []string{"8:orgid:alice", "8:orgid:bob"})
	if got != "Alice Martin, Bob Durand" {
		t.Fatalf("group display name=%q", got)
	}
}

func TestConversationSyncLowerBoundUsesConversationWatermark(t *testing.T) {
	global := time.Date(2026, 8, 9, 22, 30, 0, 0, time.UTC)
	conversation := time.Date(2026, 8, 7, 15, 17, 30, 0, time.UTC)
	lower := teamsSyncLowerBound(global, &conversation)
	if want := time.Date(2026, 8, 7, 15, 12, 30, 0, time.UTC); !lower.Equal(want) {
		t.Fatalf("conversation lower bound=%s, want %s", lower, want)
	}
	if got := teamsSyncLowerBound(global, nil); !got.IsZero() {
		t.Fatalf("new conversation lower bound=%s, want zero", got)
	}
}

func TestOldestTeamsMessageTimeHandlesFilteredEmptyPage(t *testing.T) {
	if got := oldestTeamsMessageTime(nil); !got.IsZero() {
		t.Fatalf("empty page oldest=%s, want zero", got)
	}
	newer := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)
	got := oldestTeamsMessageTime([]msteams.Message{{Created: newer}, {}, {Created: older}})
	if !got.Equal(older) {
		t.Fatalf("oldest=%s, want %s", got, older)
	}
}

func TestTeamsPresenceStatus(t *testing.T) {
	for _, test := range []struct {
		availability string
		activity     string
		want         string
	}{
		{"Busy", "InAMeeting", "meeting"},
		{"Busy", "InACall", "busy"},
		{"DoNotDisturb", "Presenting", "dnd"},
		{"Available", "Available", "online"},
		{"Away", "Away", "away"},
		{"Offline", "OffWork", "offline"},
	} {
		if got := teamsPresenceStatus(test.availability, test.activity); got != test.want {
			t.Errorf("teamsPresenceStatus(%q, %q)=%q, want %q", test.availability, test.activity, got, test.want)
		}
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
	if message.CallOutcome != "ENDED" || message.CallDurationSecs == nil || *message.CallDurationSecs != 1849 {
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
	if message.CallLinkAction != "join" {
		t.Fatalf("call link action=%q, want join", message.CallLinkAction)
	}
}

func TestCallSummaryMarksSelfParticipantAsConnected(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self", SkypeToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	provider := NewProvider()
	provider.session = &session{DisplayName: "Me User"}
	message := provider.toModelMessage(client, msteams.Message{
		ID: "call-self", MessageType: "ThreadActivity/CallEnded", Created: time.Unix(123, 0),
		Content: `<ended/><partlist><part><displayName>Me User</displayName><duration>900</duration></part>` +
			`<part><displayName>Other User</displayName><duration>900</duration></part></partlist>`,
	}, "thread-1")
	if message.CallOutcome != "CONNECTED" || message.CallDurationSecs == nil || *message.CallDurationSecs != 900 {
		t.Fatalf("self participant summary was not recognized: %+v", message)
	}
}

func TestScheduledCallStartIncludesEnterpriseJoinURL(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	provider := NewProvider()
	message := provider.toModelMessage(client, msteams.Message{
		ID: "call-start", ThreadID: "thread-1", From: "8:orgid:alice",
		MessageType: "ThreadActivity/CallStarted", Created: time.Unix(123, 0),
		Content: `https://teams.microsoft.com/l/meetup-join/19%3ameeting_example%40thread.v2/0?context=%7b%22Tid%22%3a%22tenant%22%7d`,
	}, "thread-1")

	if message.CallType != "scheduled_start" {
		t.Fatalf("call type=%q, want scheduled_start", message.CallType)
	}
	if message.CallUrl != `https://teams.microsoft.com/l/meetup-join/19%3ameeting_example%40thread.v2/0?context=%7b%22Tid%22%3a%22tenant%22%7d` {
		t.Fatalf("call URL=%q", message.CallUrl)
	}
	if message.CallLinkAction != "join" {
		t.Fatalf("call link action=%q, want join", message.CallLinkAction)
	}
}

func TestAdHocCallFallsBackToTeamsConversation(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	provider := NewProvider()
	message := provider.toModelMessage(client, msteams.Message{
		ID: "call-start", ThreadID: "19:group@thread.v2", From: "8:orgid:alice",
		MessageType: "ThreadActivity/CallStarted", Created: time.Unix(123, 0),
		Content: `<conversationUrl>https://api.flightproxy.teams.microsoft.com/api/v2/ep/conv</conversationUrl>`,
	}, "19:group@thread.v2")

	if message.CallUrl != "https://teams.microsoft.com/l/chat/19:group@thread.v2/conversations" {
		t.Fatalf("call URL=%q", message.CallUrl)
	}
	if message.CallLinkAction != "open" {
		t.Fatalf("call link action=%q, want open", message.CallLinkAction)
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

func TestTeamsHTMLRichPresentation(t *testing.T) {
	got := teamsHTMLToMarkdown(
		`<p><span style="color:#c4314b;background-color:rgb(255, 240, 200);font-size:18px;text-decoration:underline">` +
			`<strong>Important</strong></span> <font color="blue" size="5">Grand</font></p>`,
	)
	for _, expected := range []string{
		`<loom-style color="#c4314b" background="rgb(255, 240, 200)" size="18px" underline="true">**Important**</loom-style>`,
		`<loom-style color="blue" size="24px">Grand</loom-style>`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("rich text %q does not contain %q", got, expected)
		}
	}
}

func TestTeamsHTMLFormattingMovesEdgeSpacesOutsideMarkdownMarkers(t *testing.T) {
	got := teamsHTMLToMarkdown(
		`<p>+<strong> il nous a remonté plusieurs régressions sur la partie téléphone hier</strong>,</p>` +
			`<p><strong>anonymisation </strong><a href="https://zaion-team.atlassian.net/browse/BUILD-5852">https://zaion-team.atlassian.net/browse/BUILD-5852</a> + <strong>P3 </strong>:</p>` +
			`<p>Vision client :<em> 2 tickets rejetés + 4 tickets à faire </em></p>`,
	)
	want := "+ **il nous a remonté plusieurs régressions sur la partie téléphone hier**,\n" +
		"**anonymisation** [https://zaion-team.atlassian.net/browse/BUILD-5852](https://zaion-team.atlassian.net/browse/BUILD-5852) + **P3** :\n" +
		"Vision client : *2 tickets rejetés + 4 tickets à faire*"
	if got != want {
		t.Fatalf("Teams formatting = %q, want %q", got, want)
	}
}

func TestTeamsHTMLNormalizesLiteralMalformedBold(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			`<p>Bonjour MEP **backend **VSR **GENERATION-WB-PB** </p>`,
			`Bonjour MEP **backend** VSR **GENERATION-WB-PB**`,
		},
		{
			`<p>**Risque : **Faible** **</p>`,
			`**Risque :** Faible`,
		},
		{
			`<p>\*\*On n'est pas bons là \*\*</p>`,
			`**On n'est pas bons là**`,
		},
	} {
		if got := teamsHTMLToMarkdown(test.input); got != test.want {
			t.Errorf("teamsHTMLToMarkdown(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestTeamsHTMLNormalizesDuplicatedMarkdownLink(t *testing.T) {
	input := `[[https://www.linkedin.com/in/melanieantonelli?utm\_source=share\_via&utm\_content=profile&utm\_medium=member\_ios](https://www.linkedin.com/in/melanieantonelli?utm_source=share_via\&utm_content=profile\&utm_medium=member_ios)]\([https://www.linkedin.com/in/melanieantonelli?utm\_source=share\_via&utm\_content=profile&utm\_medium=member\_ios](https://www.linkedin.com/in/melanieantonelli?utm_source=share_via\&utm_content=profile\&utm_medium=member_ios))`
	want := `[https://www.linkedin.com/in/melanieantonelli?utm\_source=share\_via&utm\_content=profile&utm\_medium=member\_ios](https://www.linkedin.com/in/melanieantonelli?utm_source=share_via\&utm_content=profile\&utm_medium=member_ios)`
	if got := teamsHTMLToMarkdown(input); got != want {
		t.Fatalf("duplicated Teams link = %q, want %q", got, want)
	}
}

func TestTeamsHTMLInlineEmoji(t *testing.T) {
	got := teamsHTMLToMarkdown(`<p>Hello <img itemtype="http://schema.skype.com/Emoji" itemid="wink" src="emoji.png"></p>`)
	if got != "Hello 😉" {
		t.Fatalf("inline emoji body=%q", got)
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

func TestTeamsAttachmentHTML(t *testing.T) {
	image := teamsAttachmentHTML(&msteams.Attachment{
		Name: "photo.jpg", URL: "https://api.asm.skype.com/image", ContentType: "image/jpeg", Size: 12,
	}, "")
	if !strings.Contains(image, "AMSImage") || !strings.Contains(image, "photo.jpg") {
		t.Fatalf("image HTML=%q", image)
	}
	document := teamsAttachmentHTML(&msteams.Attachment{
		Name: "report.pdf", URL: "https://api.asm.skype.com/file", ContentType: "application/pdf", Size: 42,
	}, "<strong>Report</strong>")
	for _, expected := range []string{"URIObject", `url="https://api.asm.skype.com/file"`,
		`<a href="https://api.asm.skype.com/file">report.pdf</a>`, "OriginalName", `FileSize v="42"`, "<strong>Report</strong><br>"} {
		if !strings.Contains(document, expected) {
			t.Fatalf("document HTML %q does not contain %q", document, expected)
		}
	}
}

func TestTeamsHTMLListAndReplyWithIncorrectContentType(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	provider := NewProvider()

	list := provider.toModelMessage(client, msteams.Message{
		ID: "list", From: "8:orgid:imed", ContentType: "text",
		Content: `<p>Remarques :</p><ul><li>Premier point</li><li>Second point</li></ul>`,
	}, "conversation")
	if !strings.Contains(list.Body, "Remarques :\n- Premier point\n- Second point") {
		t.Fatalf("list body=%q", list.Body)
	}

	reply := provider.toModelMessage(client, msteams.Message{
		ID: "reply", From: "8:orgid:nadia", ContentType: "text",
		Content: `<div><blockquote itemscope itemtype="http://schema.skype.com/Reply" itemid="list">` +
			`<p itemprop="preview">Remarques : • Premier point</p></blockquote><p>Ma réponse</p></div>`,
	}, "conversation")
	if reply.QuotedMessageID == nil || *reply.QuotedMessageID != "list" {
		t.Fatalf("reply parent=%v", reply.QuotedMessageID)
	}
	if strings.Contains(reply.Body, "Premier point") || reply.Body != "Ma réponse" {
		t.Fatalf("reply body=%q", reply.Body)
	}
}
