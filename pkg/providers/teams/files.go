package teams

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func (p *Provider) rememberAttachmentURL(fileURL string) {
	if fileURL == "" {
		return
	}
	p.fileMu.Lock()
	p.attachmentURLs[fileURL] = struct{}{}
	p.fileMu.Unlock()
}

func (p *Provider) rememberSharedFile(fileURL string, file msteams.SharedFile) {
	if fileURL == "" {
		return
	}
	p.fileMu.Lock()
	p.sharedFiles[fileURL] = file
	p.fileMu.Unlock()
}

// GetTeamsFileData downloads a Teams attachment with the credentials held by
// the connected provider and returns the data URL expected by the frontend.
func (p *Provider) GetTeamsFileData(fileURL string) (string, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return "", err
	}

	p.fileMu.RLock()
	sharedFile, isSharedFile := p.sharedFiles[fileURL]
	_, isAttachment := p.attachmentURLs[fileURL]
	p.fileMu.RUnlock()
	if !isSharedFile && !isAttachment && !isMicrosoftFileURL(fileURL) {
		return "", fmt.Errorf("%s: URL is not a Teams attachment", providerID)
	}

	var data []byte
	var contentType string
	if isSharedFile {
		data, contentType, err = client.FetchSharedFile(context.Background(), sharedFile)
	} else {
		data, contentType, err = client.FetchAttachment(context.Background(), fileURL)
	}
	if err != nil {
		return "", fmt.Errorf("%s: download attachment: %w", providerID, err)
	}
	if contentType == "" || strings.HasPrefix(contentType, "application/octet-stream") {
		contentType = http.DetectContentType(data)
	}
	if strings.HasPrefix(contentType, "text/html") {
		return "", fmt.Errorf("%s: server returned HTML instead of file data", providerID)
	}
	return fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(data)), nil
}

func isMicrosoftFileURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "teams.microsoft.com" ||
		strings.HasSuffix(host, ".teams.microsoft.com") ||
		strings.HasSuffix(host, ".skype.com") ||
		strings.HasSuffix(host, ".sharepoint.com") ||
		strings.HasSuffix(host, ".sharepoint-df.com")
}
