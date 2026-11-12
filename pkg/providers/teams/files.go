package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func (p *Provider) sharePointUploadLocation(client *msteams.Client) (msteams.SharedFile, error) {
	if strings.TrimSpace(p.instance) == "" {
		return msteams.SharedFile{}, fmt.Errorf("%s: provider instance ID is unavailable", providerID)
	}
	if db.DB == nil {
		return msteams.SharedFile{}, fmt.Errorf("%s: message database is unavailable", providerID)
	}
	var messages []models.Message
	err := db.ForProvider(db.DB, p.instance).Messages().Where("attachments <> ''").
		Order("is_from_me DESC, timestamp DESC").Limit(100).Find(&messages).Error
	if err != nil {
		return msteams.SharedFile{}, err
	}
	for _, stored := range messages {
		remote, fetchErr := client.FetchMessage(context.Background(), core.StripConvID(stored.ProtocolConvID), stored.ProtocolMsgID)
		if fetchErr != nil {
			continue
		}
		for _, shared := range remote.SharedFiles {
			if shared.SiteURL == "" || shared.FileURL == "" {
				continue
			}
			// A sent or forwarded file can still belong to another participant.
			// Resolve the signed-in user's site instead of writing beside that file.
			return client.PersonalSharedFileLocation(context.Background(), shared.SiteURL)
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
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	p.fileMu.RLock()
	sharedFile, isSharedFile := p.sharedFiles[fileURL]
	_, isAttachment := p.attachmentURLs[fileURL]
	p.fileMu.RUnlock()
	if !isSharedFile && isSharePointURL(fileURL) {
		sharedFile, isSharedFile = p.restoreSharedFile(ctx, client, fileURL)
	}
	if !isSharedFile && !isAttachment && !isMicrosoftFileURL(fileURL) {
		return "", fmt.Errorf("%s: URL is not a Teams attachment", providerID)
	}

	var data []byte
	var contentType string
	if isSharedFile && sharedFile.ItemID != "" {
		data, contentType, err = downloadTeamsSharedFile(ctx, sharedFile, client.FetchSharedFile, client.FetchSharedLink)
	} else if isSharedFile {
		// Some Teams video shares omit the SharePoint item ID while still
		// providing an authenticated file/share URL. The UniqueId endpoint cannot
		// serve those files, so use SharePoint's direct download flow instead.
		sharedURL := firstNonEmptySharedFileURL(sharedFile.ShareURL, sharedFile.FileURL, fileURL)
		data, contentType, err = client.FetchSharedLink(ctx, sharedURL)
	} else if isSharePointURL(fileURL) {
		data, contentType, err = client.FetchSharedLink(ctx, fileURL)
	} else {
		// Legacy rows stored the AMS /imgo rendition as URL before the
		// canonical model distinguished preview and original sources. Upgrade
		// those opaque references at the provider boundary so existing messages
		// also open at source quality. Fall back when an older AMS object does
		// not expose the original view.
		fetchURL, _ := teamsImageAttachmentURLs(fileURL, "image")
		data, contentType, err = client.FetchAttachment(ctx, fetchURL)
		if err != nil && fetchURL != fileURL {
			data, contentType, err = client.FetchAttachment(ctx, fileURL)
		}
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

// Teams sometimes supplies a drive item ID where download.aspx expects a
// SharePoint UniqueId. Retry through the sharing URL when that endpoint fails.
func downloadTeamsSharedFile(ctx context.Context, file msteams.SharedFile,
	fetchFile func(context.Context, msteams.SharedFile) ([]byte, string, error),
	fetchLink func(context.Context, string) ([]byte, string, error),
) ([]byte, string, error) {
	data, contentType, err := fetchFile(ctx, file)
	if err == nil || ctx.Err() != nil {
		return data, contentType, err
	}
	if uniqueID, ok := teamsDriveItemUniqueID(file.ItemID); ok {
		converted := file
		converted.ItemID = uniqueID
		var convertedErr error
		data, contentType, convertedErr = fetchFile(ctx, converted)
		if convertedErr == nil {
			return data, contentType, nil
		}
		err = fmt.Errorf("%v; decoded UniqueId: %w", err, convertedErr)
	}
	if file.ShareURL == "" || ctx.Err() != nil {
		return nil, "", err
	}
	data, contentType, linkErr := fetchLink(ctx, file.ShareURL)
	if linkErr != nil {
		return nil, "", fmt.Errorf("direct download: %v; sharing link: %w", err, linkErr)
	}
	return data, contentType, nil
}

// OneDrive business item IDs encode a SharePoint GUID in the final 26
// base32 characters, preceded by two non-GUID bits. GUID fields are little-endian.
func teamsDriveItemUniqueID(itemID string) (string, bool) {
	if len(itemID) != 34 || !strings.HasPrefix(itemID, "01") {
		return "", false
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	var guid [16]byte
	for i, ch := range itemID[8:] {
		value := strings.IndexRune(alphabet, ch)
		if value < 0 {
			return "", false
		}
		for bit := 0; bit < 5; bit++ {
			position := i*5 + bit - 2
			if position >= 0 && position < 128 && value&(1<<(4-bit)) != 0 {
				guid[position/8] |= 1 << (7 - uint(position%8))
			}
		}
	}
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(guid[0:4]), binary.LittleEndian.Uint16(guid[4:6]), binary.LittleEndian.Uint16(guid[6:8]), guid[8:10], guid[10:]), true
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
func (p *Provider) restoreSharedFile(ctx context.Context, client *msteams.Client, fileURL string) (msteams.SharedFile, bool) {
	if db.DB == nil || strings.TrimSpace(p.instance) == "" {
		return msteams.SharedFile{}, false
	}
	var candidates []models.Message
	if err := db.ForProvider(db.DB, p.instance).Messages().WithContext(ctx).Where("attachments LIKE ?", "%"+fileURL+"%").
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
			ctx, core.StripConvID(stored.ProtocolConvID), stored.ProtocolMsgID,
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
