package main

import (
	"context"
	"encoding/base64"
	"fmt"
	browserproto "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"
)

// Browser rendering is a bounded fallback, never one browser per message in
// parallel. Each invocation uses a disposable profile, with no user cookies or
// access to the Wails bridge. Chrome/Chromium must be installed; metadata and
// favicons remain available when it is not.
var previewBrowserSlot = make(chan struct{}, 1)

func renderLinkPreview(parent context.Context, rawURL string) (LinkPreview, error) {
	ctx, cancel := context.WithTimeout(parent, 18*time.Second)
	defer cancel()
	select {
	case previewBrowserSlot <- struct{}{}:
		defer func() { <-previewBrowserSlot }()
	case <-ctx.Done():
		return LinkPreview{}, ctx.Err()
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1200, 680), chromedp.Flag("mute-audio", true))
	alloc, stopAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	var responseMu sync.Mutex
	statuses := make(map[string]int64)
	chromedp.ListenTarget(browser, func(event any) {
		if response, ok := event.(*network.EventResponseReceived); ok && response.Type == network.ResourceTypeDocument {
			responseMu.Lock()
			statuses[strings.Split(response.Response.URL, "#")[0]] = response.Response.Status
			responseMu.Unlock()
		}
	})
	if err := chromedp.Run(browser, network.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, agent, _, err := browserproto.GetVersion().Do(ctx)
		if err != nil {
			return err
		}
		return emulation.SetUserAgentOverride(strings.ReplaceAll(agent, "HeadlessChrome", "Chrome")).Do(ctx)
	}), chromedp.EmulateViewport(1200, 680), chromedp.Navigate(rawURL)); err != nil {
		return LinkPreview{}, fmt.Errorf("preview navigation: %w", err)
	}

	// Let client-side redirects, fonts, and images settle. A URL plus visible
	// content must remain stable before capturing; transient blank SPA shells
	// must not become a cached thumbnail.
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	previous := ""
	stableSince := time.Now()
	var state struct {
		URL   string
		Text  string
		Ready bool
	}
	for {
		select {
		case <-ctx.Done():
			return LinkPreview{}, fmt.Errorf("preview stabilization (ready=%t, text length=%d): %w", state.Ready, len(state.Text), ctx.Err())
		case <-ticker.C:
		}
		err := chromedp.Run(browser, chromedp.Evaluate(`({url: location.href, text: (document.body?.innerText || '').slice(0, 4000), ready: document.readyState === 'complete' && (!document.fonts || document.fonts.status === 'loaded') && Array.from(document.images).every(i => i.complete)})`, &state))
		if err != nil {
			previous = ""
			stableSince = time.Now()
			continue
		}
		signature := state.URL + state.Text
		if !state.Ready || len(strings.TrimSpace(state.Text)) < 3 || signature != previous {
			previous = signature
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= 1600*time.Millisecond {
			break
		}
	}
	responseMu.Lock()
	status := statuses[strings.Split(state.URL, "#")[0]]
	responseMu.Unlock()
	if status >= 400 {
		return LinkPreview{}, fmt.Errorf("rendered page returned HTTP %d", status)
	}
	if !validPreviewURL(state.URL) {
		return LinkPreview{}, fmt.Errorf("unsupported rendered URL")
	}
	var markup string
	var screenshot []byte
	err := chromedp.Run(browser,
		chromedp.OuterHTML("html", &markup, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			screenshot, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(75).Do(ctx)
			return err
		}),
	)
	if err != nil {
		return LinkPreview{}, err
	}
	if len(screenshot) == 0 || len(screenshot) > 1024*1024 {
		return LinkPreview{}, fmt.Errorf("invalid screenshot size")
	}
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return LinkPreview{}, err
	}
	finalURL, _ := url.Parse(state.URL)
	preview := extractLinkPreview(doc, finalURL)
	preview.URL = rawURL
	preview.ImageURL = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(screenshot)
	return preview, nil
}
