package main

import (
	"Loom/pkg/db"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/sync/singleflight"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// metaAttrs extracts property, name, and content from a <meta> node's attributes.
func metaAttrs(n *html.Node) (property, name, content string) {
	for _, a := range n.Attr {
		switch a.Key {
		case "property":
			property = strings.ToLower(strings.TrimSpace(a.Val))
		case "name":
			name = strings.ToLower(strings.TrimSpace(a.Val))
		case "content":
			content = strings.TrimSpace(a.Val)
		}
	}
	return
}

// applyMetaToPreview updates preview fields from a single <meta> node.
func applyMetaToPreview(n *html.Node, p *LinkPreview) {
	property, name, content := metaAttrs(n)
	if content == "" {
		return
	}
	if property == "" {
		property = name
	}
	switch property {
	case "og:title":
		if p.Title == "" {
			p.Title = content
		}
	case "og:description":
		if p.Description == "" {
			p.Description = content
		}
	case "og:image", "og:image:url", "og:image:secure_url":
		if p.ImageURL == "" {
			p.ImageURL = content
		}
	}

}

// walkHTMLForPreview traverses the HTML tree and extracts OG/meta/title data into p.
func walkHTMLForPreview(n *html.Node, p *LinkPreview, title *string) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "title":
			if n.FirstChild != nil && *title == "" {
				*title = strings.TrimSpace(n.FirstChild.Data)
			}
		case "meta":
			applyMetaToPreview(n, p)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTMLForPreview(c, p, title)
	}
}

// jsonLDString returns the first useful string representation of a JSON-LD
// value. Schema.org allows image and URL fields to be strings, objects, or
// arrays of either.
func jsonLDString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, item := range value {
			if result := jsonLDString(item); result != "" {
				return result
			}
		}
	case map[string]any:
		for _, key := range []string{"url", "contentUrl", "@id"} {
			if result := jsonLDString(value[key]); result != "" {
				return result
			}
		}
	}
	return ""
}

// applyJSONLDToPreview walks a JSON-LD document and fills metadata missing
// from the standard Open Graph tags. Nested @graph entries are common.
func applyJSONLDToPreview(value any, p *LinkPreview) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			applyJSONLDToPreview(item, p)
		}
	case map[string]any:
		if p.Title == "" {
			for _, key := range []string{"headline", "name"} {
				if p.Title = jsonLDString(value[key]); p.Title != "" {
					break
				}
			}
		}
		if p.Description == "" {
			p.Description = jsonLDString(value["description"])
		}
		if p.ImageURL == "" {
			p.ImageURL = jsonLDString(value["image"])
		}
		if nested, ok := value["@graph"]; ok {
			applyJSONLDToPreview(nested, p)
		}
	}
}

func walkHTMLForJSONLD(n *html.Node, p *LinkPreview) {
	if n.Type == html.ElementNode && n.Data == "script" {
		var scriptType string
		for _, attr := range n.Attr {
			if attr.Key == "type" {
				scriptType = strings.ToLower(strings.TrimSpace(strings.Split(attr.Val, ";")[0]))
				break
			}
		}
		if scriptType == "application/ld+json" && n.FirstChild != nil {
			var value any
			if json.Unmarshal([]byte(n.FirstChild.Data), &value) == nil {
				applyJSONLDToPreview(value, p)
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkHTMLForJSONLD(child, p)
	}
}

// FetchLinkPreview fetches and parses Open Graph metadata for a given URL.
// Results are persisted for seven days to avoid repeated rendering.
var previewRequests singleflight.Group

func (a *App) FetchLinkPreview(url string) (LinkPreview, error) {
	result, err, _ := previewRequests.Do(fmt.Sprintf("%p:%s", a, url), func() (any, error) {
		return a.fetchLinkPreview(url)
	})
	if err != nil {
		return LinkPreview{}, err
	}
	return result.(LinkPreview), nil
}

func (a *App) fetchLinkPreview(url string) (LinkPreview, error) {
	if !validPreviewURL(url) {
		return LinkPreview{}, fmt.Errorf("invalid URL: %s", url)
	}

	// Serve from cache when available.
	a.linkPreviewCacheMu.RLock()
	if a.linkPreviewCache != nil {
		if entry, ok := a.linkPreviewCache[url]; ok && time.Now().Before(entry.expiresAt) {
			a.linkPreviewCacheMu.RUnlock()
			return entry.preview, nil
		}
	}
	a.linkPreviewCacheMu.RUnlock()

	if db.DB != nil {
		if row, err := db.LoadLinkPreview(db.DB, url, time.Now()); err == nil {
			var cached LinkPreview
			if json.Unmarshal([]byte(row.Payload), &cached) == nil {
				return cached, nil
			}
		}
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	select {
	case previewSlots <- struct{}{}:
		defer func() { <-previewSlots }()
	case <-ctx.Done():
		return LinkPreview{}, ctx.Err()
	}
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return LinkPreview{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Loom/1.0; +https://github.com/loom)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return LinkPreview{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return LinkPreview{}, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	// Decode legacy encodings before parsing, while bounding the HTML response.
	body, err := charset.NewReader(io.LimitReader(resp.Body, 2*1024*1024), resp.Header.Get("Content-Type"))
	if err != nil {
		return LinkPreview{}, err
	}
	doc, err := html.Parse(body)
	if err != nil {
		return LinkPreview{}, fmt.Errorf("HTML parse error: %w", err)
	}
	preview := extractLinkPreview(doc, resp.Request.URL)
	// Image retrieval is optional and has its own short deadline.
	if preview.ImageURL != "" {
		imageCtx, imageCancel := context.WithTimeout(ctx, 3*time.Second)
		if data := fetchPreviewImage(imageCtx, client, preview.ImageURL, resp.Request.URL.String()); data != "" {
			preview.ImageURL = data
		}
		imageCancel()
	}

	if preview.ImageURL == "" {
		if rendered, err := renderLinkPreview(ctx, url); err == nil {
			preview.ImageURL = rendered.ImageURL
			if rendered.FaviconURL != "" {
				preview.FaviconURL = rendered.FaviconURL
			}
			if preview.Title == "" {
				preview.Title = rendered.Title
			}
			if preview.Description == "" {
				preview.Description = rendered.Description
			}
		}
	}

	a.linkPreviewCacheMu.Lock()
	if a.linkPreviewCache == nil {
		a.linkPreviewCache = make(map[string]linkPreviewEntry)
	}
	// Bound retained image data and discard expired entries.
	for key, entry := range a.linkPreviewCache {
		if time.Now().After(entry.expiresAt) {
			delete(a.linkPreviewCache, key)
		}
	}
	if len(a.linkPreviewCache) >= 64 {
		for key := range a.linkPreviewCache {
			delete(a.linkPreviewCache, key)
			break
		}
	}
	a.linkPreviewCache[url] = linkPreviewEntry{preview: preview, expiresAt: time.Now().Add(7 * 24 * time.Hour)}
	a.linkPreviewCacheMu.Unlock()

	if db.DB != nil {
		if payload, err := json.Marshal(preview); err == nil {
			if err := db.SaveLinkPreview(db.DB, url, string(payload), time.Now().Add(7*24*time.Hour)); err != nil {
				log.Printf("[link preview] failed to persist cache: %v", err)
			}
		}
	}
	return preview, nil
}

// Limit concurrent preview network work across message prefetches.
var previewSlots = make(chan struct{}, 4)

func previewAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func previewWalk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		previewWalk(c, visit)
	}
}

func previewText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "nav", "header", "footer", "aside":
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func previewURL(base *url.URL, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u = base.ResolveReference(u)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

func extractLinkPreview(doc *html.Node, pageURL *url.URL) LinkPreview {
	p := LinkPreview{URL: pageURL.String()}
	base := pageURL
	baseFound := false
	previewWalk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "base" && !baseFound {
			if resolved := previewURL(pageURL, previewAttr(n, "href")); resolved != "" {
				base, _ = url.Parse(resolved)
				baseFound = true
			}
		}
	})
	previewWalk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "link" {
			return
		}
		for _, rel := range strings.Fields(strings.ToLower(previewAttr(n, "rel"))) {
			if rel == "icon" || rel == "apple-touch-icon" {
				if candidate := previewURL(base, previewAttr(n, "href")); candidate != "" && p.FaviconURL == "" {
					p.FaviconURL = candidate
				}
			}
		}
	})
	if p.FaviconURL == "" {
		p.FaviconURL = previewURL(pageURL, "/favicon.ico")
	}
	var title string
	walkHTMLForPreview(doc, &p, &title)
	// Metadata priority is independent of document order: Open Graph, Twitter,
	// structured data, then visible article content.
	meta := make(map[string]string)
	previewWalk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "meta" {
			return
		}
		property, name, content := metaAttrs(n)
		if property == "" {
			property = name
		}
		if property == "" {
			property = strings.ToLower(previewAttr(n, "itemprop"))
		}
		if meta[property] == "" {
			meta[property] = content
		}
	})
	fill := func(dst *string, keys ...string) {
		for _, key := range keys {
			if *dst == "" {
				*dst = meta[key]
			}
		}
	}
	fill(&p.Title, "twitter:title", "headline", "name")
	fill(&p.Description, "twitter:description", "description")
	fill(&p.ImageURL, "twitter:image", "twitter:image:src", "image", "thumbnailurl")
	walkHTMLForJSONLD(doc, &p)
	if p.Title == "" {
		p.Title = title
	}
	var main *html.Node
	previewWalk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "article" || n.Data == "main") && main == nil {
			main = n
		}
		if n.Type == html.ElementNode && n.Data == "link" && p.ImageURL == "" && strings.EqualFold(previewAttr(n, "rel"), "image_src") {
			p.ImageURL = previewAttr(n, "href")
		}
	})
	if main != nil {
		previewWalk(main, func(n *html.Node) {
			if n.Type != html.ElementNode {
				return
			}
			if p.Title == "" && n.Data == "h1" {
				p.Title = previewText(n)
			}
			if p.Description == "" && n.Data == "p" {
				if text := previewText(n); len([]rune(text)) >= 60 {
					p.Description = text
				}
			}
			if p.ImageURL == "" && n.Data == "img" {
				// Avoid obvious tracking pixels and decorative icons.
				if previewAttr(n, "width") == "1" || previewAttr(n, "height") == "1" || previewAttr(n, "aria-hidden") == "true" {
					return
				}
				for _, attr := range []string{"data-src", "src"} {
					if candidate := previewURL(base, previewAttr(n, attr)); candidate != "" {
						p.ImageURL = candidate
						break
					}
				}
			}
		})
	}
	p.ImageURL = previewURL(base, p.ImageURL)
	clean := func(s string, max int) string {
		r := []rune(strings.Join(strings.Fields(s), " "))
		if len(r) > max {
			return string(r[:max]) + "…"
		}
		return string(r)
	}
	p.Title = clean(p.Title, 300)
	p.Description = clean(p.Description, 600)
	return p
}

func fetchPreviewImage(ctx context.Context, client *http.Client, imageURL, referer string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Loom/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	const limit = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return ""
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func validPreviewURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
