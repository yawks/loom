package slack

import (
	"encoding/json"
	"testing"

	"Loom/pkg/models"

	slackapi "github.com/slack-go/slack"
)

func TestConvertMessageIncludesBlockKitImages(t *testing.T) {
	p := &SlackProvider{}
	msg := slackapi.Message{Msg: slackapi.Msg{
		Timestamp: "1710000000.000001",
		Blocks: slackapi.Blocks{BlockSet: []slackapi.Block{
			slackapi.NewImageBlock("https://images.example.com/chart.png", "chart", "", slackapi.NewTextBlockObject("plain_text", "Weekly chart", false, false)),
			slackapi.NewSectionBlock(
				slackapi.NewTextBlockObject("mrkdwn", "Status", false, false),
				nil,
				slackapi.NewAccessory(slackapi.NewImageBlockElement("https://images.example.com/status.png", "status image")),
			),
		}},
	}}

	converted := p.convertMessage(msg, "C123")
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(converted.Attachments), &attachments); err != nil {
		t.Fatalf("unmarshal attachments: %v (raw=%q)", err, converted.Attachments)
	}
	if len(attachments) != 2 {
		t.Fatalf("got %d attachments, want 2: %#v", len(attachments), attachments)
	}
	if attachments[0].URL != "https://images.example.com/chart.png" || attachments[0].FileName != "Weekly chart" {
		t.Fatalf("unexpected image block attachment: %#v", attachments[0])
	}
	if attachments[1].URL != "https://images.example.com/status.png" || attachments[1].Type != "image" {
		t.Fatalf("unexpected section image attachment: %#v", attachments[1])
	}
}

func TestConvertMessageIncludesImagesFromAttachmentBlocks(t *testing.T) {
	p := &SlackProvider{}
	msg := slackapi.Message{Msg: slackapi.Msg{
		Timestamp: "1710000000.000002",
		Attachments: []slackapi.Attachment{{
			Blocks: slackapi.Blocks{BlockSet: []slackapi.Block{
				slackapi.NewImageBlockSlackFile(
					&slackapi.SlackFileObject{URL: "https://files.slack.com/files-pri/T-F/thread-image.png"},
					"thread image", "", nil,
				),
			}},
		}},
	}}

	converted := p.convertMessage(msg, "C123")
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(converted.Attachments), &attachments); err != nil {
		t.Fatalf("unmarshal attachments: %v (raw=%q)", err, converted.Attachments)
	}
	if len(attachments) != 1 || attachments[0].URL != "https://files.slack.com/files-pri/T-F/thread-image.png" {
		t.Fatalf("nested Slack image was not converted: %#v", attachments)
	}
}

func TestSlackFileDownloadURLPrefersDownloadVariant(t *testing.T) {
	got := slackFileDownloadURL(slackapi.File{
		URLPrivate:         "https://files.slack.com/files-pri/T-F/image.png",
		URLPrivateDownload: "https://files.slack.com/files-pri/T-F/download/image.png",
	})
	if got != "https://files.slack.com/files-pri/T-F/download/image.png" {
		t.Fatalf("download URL = %q", got)
	}
}
