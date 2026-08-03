package slack

import (
	"strings"
	"testing"

	slackapi "github.com/slack-go/slack"
)

func TestExtractTextFromAttachmentBlocks(t *testing.T) {
	p := &SlackProvider{}
	msg := slackapi.Message{Msg: slackapi.Msg{
		Text: "digest",
		Attachments: []slackapi.Attachment{{
			Blocks: slackapi.Blocks{BlockSet: []slackapi.Block{
				slackapi.NewSectionBlock(
					slackapi.NewTextBlockObject("mrkdwn", "<https://tracker.example/issue/RDS-5988|RDS-5988> - RGPD request", false, false),
					nil, nil,
				),
				slackapi.NewContextBlock("", slackapi.NewTextBlockObject("mrkdwn", "From : user@example.com", false, false)),
			}},
		}},
	}}

	got := p.extractTextFromRichContent(msg)
	if !strings.Contains(got, "RDS-5988") || !strings.Contains(got, "From : user@example.com") {
		t.Fatalf("nested attachment blocks were not extracted: %q", got)
	}
}

func TestExtractTextKeepsAttachmentTitleLink(t *testing.T) {
	p := &SlackProvider{}
	msg := slackapi.Message{Msg: slackapi.Msg{Attachments: []slackapi.Attachment{{
		Title:     "RDS-5988",
		TitleLink: "https://tracker.example/issue/RDS-5988",
		Text:      "RGPD request",
	}}}}

	got := p.extractTextFromRichContent(msg)
	if !strings.Contains(got, "[RDS-5988](https://tracker.example/issue/RDS-5988)") {
		t.Fatalf("attachment title link was lost: %q", got)
	}
}

func TestExtractTextKeepsContextImageAltText(t *testing.T) {
	p := &SlackProvider{}
	msg := slackapi.Message{Msg: slackapi.Msg{Blocks: slackapi.Blocks{BlockSet: []slackapi.Block{
		slackapi.NewContextBlock("", slackapi.NewImageBlockElement("https://example.com/calendar.png", ":calendar:"), slackapi.NewTextBlockObject("mrkdwn", "*Today*", false, false)),
	}}}}

	got := p.extractTextFromRichContent(msg)
	if !strings.Contains(got, ":calendar:") || !strings.Contains(got, "*Today*") {
		t.Fatalf("context image or text was lost: %q", got)
	}
}
