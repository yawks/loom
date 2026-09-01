package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func (p *Provider) sharePointUploadLocation(client *msteams.Client) (msteams.SharedFile, error) {
	if db.DB == nil {
		return msteams.SharedFile{}, fmt.Errorf("%s: message database is unavailable", providerID)
	}
	var messages []models.Message
	err := db.DB.Where("protocol_conv_id LIKE ? AND attachments <> ''", p.instance+"::%").
		Order("is_from_me DESC, timestamp DESC").Limit(100).Find(&messages).Error
	if err != nil {
		return msteams.SharedFile{}, err
	}
	var fallback msteams.SharedFile
	for _, stored := range messages {
		remote, fetchErr := client.FetchMessage(context.Background(), core.StripConvID(stored.ProtocolConvID), stored.ProtocolMsgID)
		if fetchErr != nil {
			continue
		}
		for _, shared := range remote.SharedFiles {
			if shared.SiteURL == "" || shared.FileURL == "" {
				continue
			}
			if fallback.SiteURL == "" {
				fallback = shared
			}
			if stored.IsFromMe || strings.EqualFold(remote.From, client.UserMRI()) {
				return shared, nil
			}
		}
	}
	// If Loom has only seen files owned by another participant, their URL still
	// reveals the tenant's OneDrive host. Derive the signed-in user's personal
	// site and use the standard Documents library as the upload location.
	if fallback.SiteURL != "" {
		profile, profileErr := client.GetUser(context.Background(), client.UserMRI())
		if profileErr == nil && profile != nil && profile.Email != "" {
			u, parseErr := url.Parse(fallback.SiteURL)
			if parseErr == nil && u.Host != "" {
				account := strings.NewReplacer("@", "_", ".", "_", "-", "_").Replace(strings.ToLower(profile.Email))
				siteURL := u.Scheme + "://" + u.Host + "/personal/" + account
				return msteams.SharedFile{SiteURL: siteURL, FileURL: siteURL + "/Documents/location"}, nil
			}
		}
	}
	return msteams.SharedFile{}, fmt.Errorf("%s: no SharePoint chat-file location could be discovered; send one document once from Teams, then retry", providerID)
}

func (p *Provider) sharePointRecipients(client *msteams.Client, conversationID string) ([]string, error) {
	chat, err := client.GetChat(context.Background(), conversationID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(chat.Members))
	for _, member := range chat.Members {
		if strings.EqualFold(member.MRI, client.UserMRI()) {
			continue
		}
		email := strings.TrimSpace(member.Email)
		if email == "" {
			if profile, profileErr := client.GetUser(context.Background(), member.MRI); profileErr == nil && profile != nil {
				email = strings.TrimSpace(profile.Email)
			}
		}
		key := strings.ToLower(email)
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, email)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s: no participant email address is available for SharePoint sharing", providerID)
	}
	return result, nil
}

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
	if !isSharedFile && isSharePointURL(fileURL) {
		sharedFile, isSharedFile = p.restoreSharedFile(client, fileURL)
	}
	if !isSharedFile && !isAttachment && !isMicrosoftFileURL(fileURL) {
		return "", fmt.Errorf("%s: URL is not a Teams attachment", providerID)
	}

	var data []byte
	var contentType string
	if isSharedFile && sharedFile.ItemID != "" {
		data, contentType, err = client.FetchSharedFile(context.Background(), sharedFile)
	} else if isSharedFile {
		// Some Teams video shares omit the SharePoint item ID while still
		// providing an authenticated file/share URL. The UniqueId endpoint cannot
		// serve those files, so use SharePoint's direct download flow instead.
		sharedURL := firstNonEmptySharedFileURL(sharedFile.ShareURL, sharedFile.FileURL, fileURL)
		data, contentType, err = client.FetchSharedLink(context.Background(), sharedURL)
	} else if isSharePointURL(fileURL) {
		data, contentType, err = client.FetchSharedLink(context.Background(), fileURL)
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

func firstNonEmptySharedFileURL(urls ...string) string {
	for _, candidate := range urls {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

// ForwardAttachment downloads the source with the Teams credentials, then
// sends it through SendFile. SendFile selects AMS for inline media and the
// native SharePoint chat-file path for documents.
func (p *Provider) ForwardAttachment(conversationID, fileURL, filename, mimeType string) error {
	dataURL, err := p.GetTeamsFileData(fileURL)
	if err != nil {
		return err
	}
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		return fmt.Errorf("%s: invalid attachment data", providerID)
	}
	data, err := base64.StdEncoding.DecodeString(dataURL[comma+1:])
	if err != nil {
		return err
	}
	_, err = p.SendFile(conversationID, &core.Attachment{
		FileName: filename, MimeType: mimeType, FileSize: len(data), Data: data,
	}, nil)
	return err
}

// restoreSharedFile reloads the non-public SharePoint identifiers that Teams
// supplied with the original message. Attachment JSON intentionally stores
// only display fields, so these credentials must be recovered after restart.
func (p *Provider) restoreSharedFile(client *msteams.Client, fileURL string) (msteams.SharedFile, bool) {
	if db.DB == nil {
		return msteams.SharedFile{}, false
	}
	var candidates []models.Message
	if err := db.DB.Where("attachments LIKE ?", "%"+fileURL+"%").
		Order("timestamp DESC").Limit(20).Find(&candidates).Error; err != nil {
		return msteams.SharedFile{}, false
	}
	for _, stored := range candidates {
		var attachments []models.Attachment
		if json.Unmarshal([]byte(stored.Attachments), &attachments) != nil {
			continue
		}
		matches := false
		for _, attachment := range attachments {
			if attachment.URL == fileURL {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		remote, err := client.FetchMessage(
			context.Background(), core.StripConvID(stored.ProtocolConvID), stored.ProtocolMsgID,
		)
		if err != nil {
			continue
		}
		for _, shared := range remote.SharedFiles {
			if shared.ShareURL == fileURL || shared.FileURL == fileURL {
				p.rememberSharedFile(fileURL, shared)
				return shared, true
			}
		}
	}
	return msteams.SharedFile{}, false
}

func isSharePointURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.HasSuffix(host, ".sharepoint.com") || strings.HasSuffix(host, ".sharepoint-df.com")
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
