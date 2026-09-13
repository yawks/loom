package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

func TestAuthenticatedMediaDownloadUsesConfiguredHomeserver(t *testing.T) {
	var path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.EscapedPath(), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage"))
	}))
	defer server.Close()
	p := NewProvider()
	if err := p.Init(core.ProviderConfig{"homeserver": server.URL, "access_token": "secret", "_instance_id": "matrix-1"}); err != nil {
		t.Fatal(err)
	}
	data, mimeType, err := p.GetAttachmentData(context.Background(), "mxc://remote.example/media/id")
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("authorization = %q", authorization)
	}
	if path != "/_matrix/client/v1/media/download/remote.example/media/id" {
		t.Fatalf("path = %q", path)
	}
	if !strings.HasPrefix(mimeType, "image/png") || len(data) == 0 {
		t.Fatalf("mime/data = %q/%d", mimeType, len(data))
	}
}

func TestMediaDownloadSVGIconMIME(t *testing.T) {
	for _, tc := range []struct{ name, body, declared, want string }{
		{"svg", `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`, "image/svg+xml", "image/svg+xml"},
		{"xml", `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`, "application/octet-stream", "image/svg+xml"},
		{"false declaration", `ordinary text`, "image/svg+xml", "text/plain; charset=utf-8"},
		{"other xml", `<?xml version="1.0"?><document/>`, "image/svg+xml", "text/xml; charset=utf-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.declared)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p := NewProvider()
			if err := p.Init(core.ProviderConfig{"homeserver": server.URL, "access_token": "secret"}); err != nil {
				t.Fatal(err)
			}
			data, mimeType, err := p.GetAttachmentData(context.Background(), "mxc://example.org/icon")
			if err != nil {
				t.Fatal(err)
			}
			if mimeType != tc.want || string(data) != tc.body {
				t.Fatalf("mime = %q, want %q; data = %q", mimeType, tc.want, data)
			}
		})
	}
}

func TestMediaDownloadRejectsHTTPAndNonMediaResponses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
	}{
		{"unauthorized", http.StatusUnauthorized, "application/json"},
		{"forbidden", http.StatusForbidden, "application/json"},
		{"missing", http.StatusNotFound, "application/json"},
		{"html", http.StatusOK, "text/html"},
		{"json", http.StatusOK, "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"no"}`))
			}))
			defer server.Close()
			p := NewProvider()
			_ = p.Init(core.ProviderConfig{"homeserver": server.URL, "access_token": "secret"})
			if _, _, err := p.GetAttachmentData(context.Background(), "mxc://example.org/id"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestMediaRedirectDoesNotLeakAuthorization(t *testing.T) {
	var received string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/media", http.StatusFound)
	}))
	defer source.Close()
	p := NewProvider()
	_ = p.Init(core.ProviderConfig{"homeserver": source.URL, "access_token": "secret"})
	if _, _, err := p.GetAttachmentData(context.Background(), "mxc://example.org/id"); err != nil {
		t.Fatal(err)
	}
	if received != "" {
		t.Fatalf("authorization leaked to redirect target: %q", received)
	}
}

func TestMediaDownloadUsesOnlyOwningInstanceCredentials(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage"))
	}))
	defer server.Close()
	owner := NewProvider()
	_ = owner.Init(core.ProviderConfig{"homeserver": server.URL, "access_token": "owner-token", "_instance_id": "matrix-1"})
	other := NewProvider()
	_ = other.Init(core.ProviderConfig{"homeserver": server.URL, "access_token": "other-token", "_instance_id": "matrix-2"})
	if _, _, err := owner.GetAttachmentData(context.Background(), "mxc://example.org/id"); err != nil {
		t.Fatal(err)
	}
	if got := <-requests; got != "Bearer owner-token" {
		t.Fatalf("authorization = %q", got)
	}
	select {
	case unexpected := <-requests:
		t.Fatalf("unexpected second-instance request: %q", unexpected)
	default:
	}
	_ = other
}

func TestEventToMessageNormalizesImageCaptionAndMetadata(t *testing.T) {
	p := NewProvider()
	p.instanceID = "matrix-2"
	raw := `{"msgtype":"m.image","body":"Numéro privé\nAppel entrant\nHorodatage : 09/09/2026 12:42","filename":"litefeed.png","url":"mxc://example.org/media-id","info":{"mimetype":"image/png","size":12345,"w":640,"h":360},"format":"org.matrix.custom.html","formatted_body":"<h3>Numéro privé</h3><script>bad()</script><p>Appel entrant<br>Horodatage : 09/09/2026 12:42</p><img src=\"mxc://example.org/media-id\">"}`
	message, ok := p.eventToMessage("!room:example.org", matrixEvent{Type: "m.room.message", EventID: "$1", Content: json.RawMessage(raw)})
	if !ok {
		t.Fatal("message rejected")
	}
	if strings.Contains(message.Body, "bad") || strings.Contains(message.Body, "img") || !strings.Contains(message.Body, "### Numéro privé") || !strings.Contains(message.Body, "12:42") {
		t.Fatalf("body = %q", message.Body)
	}
	var attachments []struct {
		URL, FileName, MimeType string
		FileSize                int64
		Width, Height           uint32
	}
	if err := json.Unmarshal([]byte(message.Attachments), &attachments); err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].URL != "mxc://example.org/media-id" || attachments[0].FileName != "litefeed.png" || attachments[0].Width != 640 || attachments[0].Height != 360 {
		t.Fatalf("attachments = %+v", attachments)
	}
}

func TestLegacyImageBodyGetsSafeNameAndUsefulCaption(t *testing.T) {
	p := NewProvider()
	raw := `{"msgtype":"m.image","body":"<h3>Caller</h3><p>At 12:42</p>","url":"mxc://example.org/old","info":{"mimetype":"image/png"}}`
	message, ok := p.eventToMessage("!room:example.org", matrixEvent{Type: "m.room.message", Content: json.RawMessage(raw)})
	if !ok || message.Body != "### Caller\n\nAt 12:42" {
		t.Fatalf("message = %+v", message)
	}
	if strings.Contains(message.Attachments, "<h3>") || !strings.Contains(message.Attachments, "attachment.png") {
		t.Fatalf("attachments = %s", message.Attachments)
	}
}

func TestNoticeWithoutImageHasNoAttachment(t *testing.T) {
	p := NewProvider()
	message, ok := p.eventToMessage("!room:example.org", matrixEvent{Type: "m.room.message", Content: json.RawMessage(`{"msgtype":"m.notice","body":"Image unavailable"}`)})
	if !ok || message.Body != "Image unavailable" || message.Attachments != "" {
		t.Fatalf("message = %+v", message)
	}
}

func TestFormattedMessagePreservesTableStructure(t *testing.T) {
	got := canonicalMessageBody("Test Appel appel entrant", "org.matrix.custom.html", `<table><tr><th>Test Appel</th><td>appel entrant</td></tr><tr><td>De</td><td><strong>Test Appel</strong></td></tr></table>`, false)
	want := "| Test Appel | appel entrant |\n| --- | --- |\n| De | **Test Appel** |"
	if got != want {
		t.Fatalf("table markdown = %q, want %q", got, want)
	}
}

func TestFormattedMessageFlattensLayoutTableAndPreservesNestedTable(t *testing.T) {
	source := `<table><tr><td rowspan="2">T</td><td><table><tr><th>Test Appel</th><td>appel entrant</td></tr></table><h3>Appel entrant</h3></td></tr><tr><td><strong>Historique:</strong><ul><li>Dernier appel: 14:30</li></ul></td></tr></table>`
	got := canonicalMessageBody("fallback", "org.matrix.custom.html", source, false)
	if strings.Contains(got, "| T |") {
		t.Fatalf("outer layout table was emitted as GFM: %q", got)
	}
	if !strings.Contains(got, "| Test Appel | appel entrant |\n| --- | --- |") {
		t.Fatalf("nested data table was not preserved: %q", got)
	}
	if !strings.Contains(got, "### Appel entrant") || !strings.Contains(got, "**Historique:**") {
		t.Fatalf("content lost while flattening: %q", got)
	}
}

func TestFormattedMessageSupportsLitefeedNestedTableTemplate(t *testing.T) {
	source := `<table><tbody><tr><td><img src="mxc://example.org/icon" width="48" height="48" alt="T" /></td><td><table><tbody><tr><td><strong>Test Appel</strong></td><td><span data-mx-bg-color="#334155" data-mx-color="#ffffff"> appel entrant </span></td></tr></tbody></table><div><h3>Appel entrant</h3><p><strong>De:</strong> Test Appel</p><hr><strong>Historique:</strong><ul><li><strong>Dernier appel:</strong> 01/06/2026 14:30</li></ul></div></td></tr></tbody></table>`
	got := canonicalMessageBody("fallback", "org.matrix.custom.html", source, true)
	if strings.Contains(got, "mxc://") || strings.Contains(got, "| T |") {
		t.Fatalf("layout media leaked into body: %q", got)
	}
	if !strings.HasPrefix(got, "| **Test Appel** | appel entrant |\n| --- | --- |") {
		t.Fatalf("nested header table was not preserved: %q", got)
	}
	if !strings.Contains(got, "### Appel entrant") || !strings.Contains(got, "**Dernier appel:** 01/06/2026 14:30") {
		t.Fatalf("description formatting was not preserved: %q", got)
	}
}

func TestEventToMessageCreatesCanonicalEventCardBeforeGFM(t *testing.T) {
	p := NewProvider()
	raw := `{"msgtype":"m.notice","body":"fallback text","format":"org.matrix.custom.html","formatted_body":"<table><tbody><tr><td><img src=\"mxc://example.org/icon\" alt=\"T\"></td><td><table><tbody><tr><td><strong>Test Appel</strong></td><td><span data-mx-bg-color=\"#334155\">appel entrant</span></td></tr></tbody></table><div><h3>Appel entrant</h3><p><strong>De:</strong> Test Appel</p></div></td></tr></tbody></table>","com.litefeed":{"event_id":"evt-1","session_id":"session-1"}}`
	message, ok := p.eventToMessage("!room:example.org", matrixEvent{Type: "m.room.message", EventID: "$card", Content: json.RawMessage(raw)})
	if !ok {
		t.Fatal("message rejected")
	}
	if message.Body != "" {
		t.Fatalf("card message retained duplicate body: %q", message.Body)
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(message.Attachments), &attachments); err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Type != "event-card" {
		t.Fatalf("attachments = %+v", attachments)
	}
	var card canonicalEventCard
	if err := json.Unmarshal([]byte(attachments[0].CardJSON), &card); err != nil {
		t.Fatal(err)
	}
	if card.Title != "Test Appel" || card.Badge != "appel entrant" || card.ImageURL != "mxc://example.org/icon" || !strings.Contains(card.Description, "**De:** Test Appel") {
		t.Fatalf("card = %+v", card)
	}
}

func TestEventToMessageKeepsTextWhenCardDataIsMissing(t *testing.T) {
	p := NewProvider()
	raw := `{"msgtype":"m.notice","body":"fallback text","format":"org.matrix.custom.html","formatted_body":"<p>Ordinary formatted message</p>"}`
	message, ok := p.eventToMessage("!room:example.org", matrixEvent{Type: "m.room.message", EventID: "$text", Content: json.RawMessage(raw)})
	if !ok || message.Body != "Ordinary formatted message" || message.Attachments != "" {
		t.Fatalf("message = %+v", message)
	}
}
