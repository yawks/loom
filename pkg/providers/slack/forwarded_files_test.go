package slack

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"Loom/pkg/models"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestHydrateForwardedFilesFromHistory(t *testing.T) {
	client := &http.Client{Transport: &cookieTransport{Transport: huddleRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.Form.Get("channel") != "D1" || req.Form.Get("oldest") != "123.456" || req.Form.Get("latest") != "123.456" || req.Form.Get("inclusive") != "1" {
			t.Fatalf("unexpected history window: %v", req.Form)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Fatal("missing network deadline")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"messages":[{"ts":"123.456","attachments":[{"files":[{"id":"F1","name":"forward.docx","url_private":"https://files.slack.com/forward.docx"}]}]}]}`)), Header: make(http.Header)}, nil
	})}}
	p := &SlackProvider{client: slack.New("test", slack.OptionHTTPClient(client))}
	msg := slack.Msg{Timestamp: "123.456", Attachments: []slack.Attachment{{Footer: "Slack Conversation", FromURL: "https://example.com/message"}}}
	p.hydrateForwardedFiles(&msg, "D1")
	if len(msg.Files) != 1 || msg.Files[0].Name != "forward.docx" {
		t.Fatalf("missing forwarded RTM file: %+v", msg.Files)
	}
}

func TestForwardedFilesSurviveSlackDecoding(t *testing.T) {
	const message = `{"type":"message","ts":"1791819825.114419","text":"Document transféré","files":[{"id":"F1","name":"spec.docx","mimetype":"application/vnd.openxmlformats-officedocument.wordprocessingml.document","url_private_download":"https://files.slack.com/spec.docx"}],"attachments":[{"text":"Version complétée","files":[{"id":"F1","name":"spec.docx","size":123,"mimetype":"application/vnd.openxmlformats-officedocument.wordprocessingml.document","url_private_download":"https://files.slack.com/spec.docx"},{"id":"F2","name":"other.docx","url_private":"https://files.slack.com/other.docx"}]}]}`
	for _, envelope := range []string{`{"ok":true,"messages":[` + message + `]}`, `{"event":` + message + `}`} {
		normalized := normalizeSlackResponse([]byte(envelope))
		var response struct {
			Messages []slack.Message          `json:"messages"`
			Event    slackevents.MessageEvent `json:"event"`
		}
		if err := json.Unmarshal(normalized, &response); err != nil {
			t.Fatal(err)
		}
		var msg slack.Message
		if len(response.Messages) > 0 {
			msg = response.Messages[0]
		} else {
			if response.Event.Message == nil {
				t.Fatal("missing socket message")
			}
			msg.Msg = *response.Event.Message
		}
		converted := (&SlackProvider{}).convertMessage(msg, "C1")
		var attachments []models.Attachment
		if err := json.Unmarshal([]byte(converted.Attachments), &attachments); err != nil {
			t.Fatal(err)
		}
		if len(attachments) != 2 || attachments[0].URL != "https://files.slack.com/spec.docx" || attachments[1].FileName != "other.docx" {
			t.Fatalf("forwarded files missing or duplicated: %+v", attachments)
		}
		if !strings.Contains(converted.Body, "Version complétée") {
			t.Fatalf("forwarded text lost: %q", converted.Body)
		}
	}
}
