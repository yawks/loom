package googlechat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const chatAPIBase = "https://chat.googleapis.com/v1"
const chatUploadBase = "https://chat.googleapis.com/upload/v1"

// --- API types ---

type Space struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	DisplayName string `json:"displayName"`
	SpaceType   string `json:"spaceType"`
	SpaceUri    string `json:"spaceUri"` // e.g. "https://mail.google.com/chat/r/spaces/WEB_CLIENT_ID"
}

type SpaceListResponse struct {
	Spaces        []Space `json:"spaces"`
	NextPageToken string  `json:"nextPageToken"`
}

type ChatUser struct {
	Name        string `json:"name"` // "users/12345"
	DisplayName string `json:"displayName"`
	AvatarUrl   string `json:"avatarUrl"`
	Email       string `json:"email"`
	Type        string `json:"type"` // HUMAN, BOT
}

type ChatThread struct {
	Name string `json:"name"` // "spaces/AAA/threads/BBB"
}

type AttachmentDataRef struct {
	ResourceName          string `json:"resourceName,omitempty"`
	AttachmentUploadToken string `json:"attachmentUploadToken,omitempty"`
}

type ChatAttachment struct {
	Name              string             `json:"name,omitempty"`
	ContentName       string             `json:"contentName,omitempty"`
	ContentType       string             `json:"contentType,omitempty"`
	DownloadUri       string             `json:"downloadUri,omitempty"`
	AttachmentDataRef *AttachmentDataRef `json:"attachmentDataRef,omitempty"`
}

type EmojiReactionSummary struct {
	Emoji *struct {
		Unicode string `json:"unicode"`
	} `json:"emoji"`
	ReactionCount int `json:"reactionCount"`
}

type ChatMessage struct {
	Name                   string                 `json:"name"` // "spaces/AAA/messages/BBB"
	Sender                 *ChatUser              `json:"sender"`
	CreateTime             time.Time              `json:"createTime"`
	LastUpdateTime         time.Time              `json:"lastUpdateTime"`
	DeleteTime             *time.Time             `json:"deleteTime"`
	Text                   string                 `json:"text"`
	FormattedText          string                 `json:"formattedText"`
	Thread                 *ChatThread            `json:"thread"`
	ThreadReply            bool                   `json:"threadReply"`
	Attachment             []ChatAttachment       `json:"attachment"`
	EmojiReactionSummaries []EmojiReactionSummary `json:"emojiReactionSummaries"`
}

type MessageListResponse struct {
	Messages      []ChatMessage `json:"messages"`
	NextPageToken string        `json:"nextPageToken"`
}

type Membership struct {
	Name   string    `json:"name"`
	State  string    `json:"state"`
	Role   string    `json:"role"`
	Member *ChatUser `json:"member"`
}

type MemberListResponse struct {
	Memberships   []Membership `json:"memberships"`
	NextPageToken string       `json:"nextPageToken"`
}

type UserInfo struct {
	Sub     string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

type ChatReaction struct {
	Name       string    `json:"name"`
	User       *ChatUser `json:"user"`
	CreateTime time.Time `json:"createTime"`
	Emoji      *struct {
		Unicode string `json:"unicode"`
	} `json:"emoji"`
}

type ReactionListResponse struct {
	Reactions     []ChatReaction `json:"reactions"`
	NextPageToken string         `json:"nextPageToken"`
}

type ChatMessagePin struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

type MessagePinListResponse struct {
	MessagePins   []ChatMessagePin `json:"messagePins"`
	NextPageToken string           `json:"nextPageToken"`
}

// --- HTTP helpers ---

func (p *GoogleChatProvider) getHTTPClient() *http.Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiClient
}

func (p *GoogleChatProvider) apiGet(path string, params url.Values, out interface{}) error {
	client := p.getHTTPClient()
	if client == nil {
		return fmt.Errorf("not connected")
	}
	urlStr := chatAPIBase + path
	if len(params) > 0 {
		urlStr += "?" + params.Encode()
	}
	resp, err := client.Get(urlStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

func (p *GoogleChatProvider) apiPost(path string, payload interface{}, out interface{}) error {
	return p.apiMethod(http.MethodPost, path, "", payload, out)
}

func (p *GoogleChatProvider) apiUploadAttachment(spaceName, filename, mimeType string, data []byte) (*ChatAttachment, error) {
	client := p.getHTTPClient()
	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	filename = filepath.Base(filename)
	if filename == "." || filename == "" {
		return nil, fmt.Errorf("googlechat: attachment filename is required")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("googlechat: attachment is empty")
	}
	if len(data) > 200*1024*1024 {
		return nil, fmt.Errorf("googlechat: attachment exceeds the 200 MB limit")
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metadataPart, err := writer.CreatePart(metadataHeader)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(metadataPart).Encode(struct {
		Filename string `json:"filename"`
	}{Filename: filename}); err != nil {
		return nil, err
	}

	mediaHeader := make(textproto.MIMEHeader)
	mediaHeader.Set("Content-Type", mimeType)
	mediaPart, err := writer.CreatePart(mediaHeader)
	if err != nil {
		return nil, err
	}
	if _, err := mediaPart.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	uploadURL := chatUploadBase + "/" + strings.TrimPrefix(spaceName, "/") +
		"/attachments:upload?uploadType=multipart"
	req, err := http.NewRequest(http.MethodPost, uploadURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", fmt.Sprintf("multipart/related; boundary=%q", writer.Boundary()))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("googlechat: attachment upload HTTP %d: %s", resp.StatusCode, string(responseBody))
	}

	var uploaded ChatAttachment
	if err := json.Unmarshal(responseBody, &uploaded); err != nil {
		return nil, err
	}
	if uploaded.AttachmentDataRef == nil || uploaded.AttachmentDataRef.AttachmentUploadToken == "" {
		return nil, fmt.Errorf("googlechat: attachment upload returned no upload token")
	}
	return &uploaded, nil
}

func (p *GoogleChatProvider) apiPatch(path string, updateMask string, payload interface{}, out interface{}) error {
	return p.apiMethod(http.MethodPatch, path, updateMask, payload, out)
}

func (p *GoogleChatProvider) apiDelete(path string) error {
	client := p.getHTTPClient()
	if client == nil {
		return fmt.Errorf("not connected")
	}
	req, err := http.NewRequest(http.MethodDelete, chatAPIBase+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (p *GoogleChatProvider) apiMethod(method, path, updateMask string, payload interface{}, out interface{}) error {
	client := p.getHTTPClient()
	if client == nil {
		return fmt.Errorf("not connected")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	urlStr := chatAPIBase + path
	if updateMask != "" {
		urlStr += "?updateMask=" + url.QueryEscape(updateMask)
	}
	req, err := http.NewRequest(method, urlStr, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

func (p *GoogleChatProvider) getUserInfo() (*UserInfo, error) {
	client := p.getHTTPClient()
	if client == nil {
		return nil, fmt.Errorf("not connected")
	}
	resp, err := client.Get("https://www.googleapis.com/userinfo/v2/me")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}
