package matrix

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

const maxDownloadedMediaBytes int64 = 64 << 20

// GetAttachmentData resolves Matrix media only through this provider's
// configured homeserver and credentials. In particular, the server name in an
// MXC URI is a path component, not an authenticated HTTP destination.
func (p *Provider) GetAttachmentData(ctx context.Context, reference string) ([]byte, string, error) {
	serverName, mediaID, err := p.parseMediaReference(reference)
	if err != nil {
		return nil, "", err
	}
	p.mu.RLock()
	base, token := p.homeserver, p.accessToken
	p.mu.RUnlock()
	endpoint := base + "/_matrix/client/v1/media/download/" + url.PathEscape(serverName) + "/" + escapeMediaID(mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := *p.client
	previousRedirectPolicy := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 0 && !sameOrigin(next.URL, via[0].URL) {
			next.Header.Del("Authorization")
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(next, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("matrix: too many media redirects")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("matrix: download media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("matrix: download media HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDownloadedMediaBytes {
		return nil, "", fmt.Errorf("matrix: media exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedMediaBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("matrix: read media: %w", err)
	}
	if int64(len(data)) > maxDownloadedMediaBytes {
		return nil, "", fmt.Errorf("matrix: media exceeds size limit")
	}
	detected := http.DetectContentType(data)
	declared, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(detected, "text/html") || strings.Contains(detected, "json") || strings.HasPrefix(declared, "text/html") || strings.Contains(declared, "json") {
		return nil, "", fmt.Errorf("matrix: server returned non-media content (%s)", detected)
	}
	// DetectContentType identifies SVG as text/XML. Preserve its image type
	// after checking the root element so WebView img elements can decode it.
	if strings.HasPrefix(detected, "text/plain") || strings.HasPrefix(detected, "text/xml") {
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			if root, ok := token.(xml.StartElement); ok {
				if root.Name.Local == "svg" && root.Name.Space == "http://www.w3.org/2000/svg" {
					detected = "image/svg+xml"
				}
				break
			}
		}
	}
	return data, detected, nil
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func (p *Provider) parseMediaReference(reference string) (string, string, error) {
	if strings.HasPrefix(reference, "mxc://") {
		parts := strings.SplitN(strings.TrimPrefix(reference, "mxc://"), "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("matrix: invalid MXC reference")
		}
		return parts[0], parts[1], nil
	}
	// Legacy persisted URLs are accepted only when they point at this instance's
	// configured homeserver and match a Matrix media download path.
	legacy, err := url.Parse(reference)
	if err != nil {
		return "", "", fmt.Errorf("matrix: invalid legacy media URL")
	}
	p.mu.RLock()
	homeserver := p.homeserver
	p.mu.RUnlock()
	base, err := url.Parse(homeserver)
	if err != nil || !strings.EqualFold(legacy.Scheme, base.Scheme) || !strings.EqualFold(legacy.Host, base.Host) {
		return "", "", fmt.Errorf("matrix: legacy media URL has unexpected origin")
	}
	const marker = "/download/"
	index := strings.Index(legacy.EscapedPath(), marker)
	if index < 0 {
		return "", "", fmt.Errorf("matrix: unsupported legacy media URL")
	}
	parts := strings.SplitN(strings.TrimPrefix(legacy.EscapedPath()[index+len(marker):], "/"), "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("matrix: invalid legacy media path")
	}
	serverName, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("matrix: invalid legacy server name")
	}
	mediaID, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("matrix: invalid legacy media ID")
	}
	return serverName, mediaID, nil
}

func escapeMediaID(id string) string {
	parts := strings.Split(filepath.ToSlash(id), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
