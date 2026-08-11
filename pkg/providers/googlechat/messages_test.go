package googlechat

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestListMessageReactionsIncludesUsersAndFollowsPages(t *testing.T) {
	provider := NewGoogleChatProvider()
	call := 0
	provider.apiClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call++
		if req.URL.Path != "/v1/spaces/space-1/messages/message-1/reactions" {
			t.Fatalf("unexpected reactions path: %s", req.URL.Path)
		}
		switch call {
		case 1:
			if got := req.URL.Query().Get("pageSize"); got != "100" {
				t.Fatalf("pageSize = %q, want 100", got)
			}
			return jsonResponse(`{"reactions":[{"user":{"name":"users/user-1"},"emoji":{"unicode":"👍"}}],"nextPageToken":"page-2"}`), nil
		case 2:
			if got := req.URL.Query().Get("pageToken"); got != "page-2" {
				t.Fatalf("pageToken = %q, want page-2", got)
			}
			return jsonResponse(`{"reactions":[{"user":{"name":"users/user-2"},"emoji":{"unicode":"❤️"}}]}`), nil
		default:
			t.Fatalf("unexpected HTTP call %d", call)
			return nil, nil
		}
	})}

	reactions, err := provider.listMessageReactions("spaces/space-1/messages/message-1")
	if err != nil {
		t.Fatalf("listMessageReactions returned an error: %v", err)
	}
	if len(reactions) != 2 || reactions[0].UserID != "user-1" || reactions[0].Emoji != "👍" || reactions[1].UserID != "user-2" {
		t.Fatalf("unexpected reactions: %#v", reactions)
	}
}

func TestStoreMessagesReconcilesReactionsForExistingMessage(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatalf("InitMockDatabase: %v", err)
	}
	defer func() {
		sqlDB, err := db.DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		db.DB = nil
	}()
	provider := NewGoogleChatProvider()
	provider.config = core.ProviderConfig{"_instance_id": "googlechat-reaction-test"}

	meta := models.MetaContact{DisplayName: "Reaction test"}
	if err := db.DB.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	account := models.LinkedAccount{MetaContactID: meta.ID, Protocol: "googlechat", ProviderInstanceID: "googlechat-reaction-test", UserID: "spaces/reaction-test"}
	if err := db.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	convID := "googlechat-reaction-test::spaces/reaction-test"
	conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: convID}
	if err := db.DB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	protocolMsgID := "spaces/reaction-test/messages/existing"
	message := models.Message{ConversationID: conversation.ID, ProtocolConvID: convID, ProtocolMsgID: protocolMsgID, Timestamp: time.Now()}
	if err := db.DB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&models.Reaction{MessageID: message.ID, UserID: "old-user", Emoji: "👎"}).Error; err != nil {
		t.Fatal(err)
	}

	snapshot := []models.Reaction{{UserID: "new-user", Emoji: "👍"}}
	provider.storeMessagesForConversation(convID, []models.Message{message}, map[string][]models.Reaction{protocolMsgID: snapshot})

	var reactions []models.Reaction
	if err := db.DB.Where("message_id = ?", message.ID).Find(&reactions).Error; err != nil {
		t.Fatal(err)
	}
	if len(reactions) != 1 || reactions[0].UserID != "new-user" || reactions[0].Emoji != "👍" {
		t.Fatalf("stored reactions were not reconciled: %#v", reactions)
	}
}

func TestGoogleChatLookbackUpperBoundIncludesLatestMessage(t *testing.T) {
	lastTS := time.Date(2026, 8, 10, 12, 57, 0, 948959000, time.UTC)
	upperBound := googleChatLookbackUpperBound(lastTS)
	if !lastTS.Before(upperBound) {
		t.Fatalf("latest message timestamp %s must satisfy createTime < %s", lastTS, upperBound)
	}
	if got := upperBound.Sub(lastTS); got != time.Nanosecond {
		t.Fatalf("lookback upper-bound offset = %s, want 1ns", got)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSendFileUploadsAndAttachesFile(t *testing.T) {
	const uploadToken = "opaque-upload-token"
	imageData := []byte("\x89PNG\r\n\x1a\nimage-data")
	call := 0

	provider := NewGoogleChatProvider()
	provider.apiClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if req.URL.String() != chatUploadBase+"/spaces/space-1/attachments:upload?uploadType=multipart" {
				t.Fatalf("unexpected upload URL: %s", req.URL)
			}
			mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse upload content type: %v", err)
			}
			if mediaType != "multipart/related" {
				t.Fatalf("upload content type = %q, want multipart/related", mediaType)
			}
			reader := multipart.NewReader(req.Body, params["boundary"])
			metadataPart, err := reader.NextPart()
			if err != nil {
				t.Fatalf("read metadata part: %v", err)
			}
			var metadata struct {
				Filename string `json:"filename"`
			}
			if err := json.NewDecoder(metadataPart).Decode(&metadata); err != nil {
				t.Fatalf("decode metadata: %v", err)
			}
			if metadata.Filename != "photo.png" {
				t.Fatalf("uploaded filename = %q, want photo.png", metadata.Filename)
			}
			mediaPart, err := reader.NextPart()
			if err != nil {
				t.Fatalf("read media part: %v", err)
			}
			if mediaPart.Header.Get("Content-Type") != "image/png" {
				t.Fatalf("uploaded MIME type = %q, want image/png", mediaPart.Header.Get("Content-Type"))
			}
			gotData, err := io.ReadAll(mediaPart)
			if err != nil {
				t.Fatalf("read uploaded bytes: %v", err)
			}
			if string(gotData) != string(imageData) {
				t.Fatal("uploaded image bytes do not match")
			}
			return jsonResponse(`{"attachmentDataRef":{"attachmentUploadToken":"` + uploadToken + `"}}`), nil

		case 2:
			if req.URL.String() != chatAPIBase+"/spaces/space-1/messages" {
				t.Fatalf("unexpected message URL: %s", req.URL)
			}
			var payload struct {
				Text       string           `json:"text"`
				Attachment []ChatAttachment `json:"attachment"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode message payload: %v", err)
			}
			if payload.Text != "" {
				t.Fatalf("file-only message text = %q, want empty", payload.Text)
			}
			if len(payload.Attachment) != 1 ||
				payload.Attachment[0].AttachmentDataRef == nil ||
				payload.Attachment[0].AttachmentDataRef.AttachmentUploadToken != uploadToken {
				t.Fatalf("message attachment payload = %#v", payload.Attachment)
			}
			return jsonResponse(`{"name":"spaces/space-1/messages/message-1","attachment":[{"contentName":"photo.png","contentType":"image/png","attachmentDataRef":{"resourceName":"spaces/space-1/messages/message-1/attachments/attachment-1"}}]}`), nil

		default:
			t.Fatalf("unexpected HTTP call %d: %s", call, req.URL)
			return nil, nil
		}
	})}

	_, err := provider.SendFile("instance::spaces/space-1", &core.Attachment{
		FileName: "photo.png",
		MimeType: "image/png",
		Data:     imageData,
		FileSize: len(imageData),
	}, nil)
	if err != nil {
		t.Fatalf("SendFile returned an error: %v", err)
	}
	if call != 2 {
		t.Fatalf("HTTP call count = %d, want 2", call)
	}
}
