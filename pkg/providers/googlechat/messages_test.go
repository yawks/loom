package googlechat

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"Loom/pkg/core"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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
