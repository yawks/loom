package googlemessages

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	googleSigninURL = "https://accounts.google.com/signin/v2/identifier?continue=https%3A%2F%2Fmessages.google.com%2Fweb%2Fconfig"
	browserTimeout  = 5 * time.Minute
)

var requiredCookies = []string{"SID", "HSID", "SSID", "OSID", "APISID", "SAPISID"}

// cookieURLs lists Google domains whose cookies are requested on every poll.
// Specifying URLs explicitly ensures we get .google.com cookies regardless of
// which page the browser is currently on (login, 2FA, redirect…).
var cookieURLs = []string{
	"https://accounts.google.com",
	"https://messages.google.com",
}

// FetchGoogleCookiesViaLogin opens a visible Chrome window on the Google login
// page and polls every 2 seconds until the session cookies set by Google after
// a successful authentication are present. The user authenticates entirely
// inside the browser (credentials, 2FA, account selection…).
func FetchGoogleCookiesViaLogin(parentCtx context.Context) (map[string]string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parentCtx, opts...)
	defer cancelAlloc()

	chromeCtx, cancelChrome := chromedp.NewContext(allocCtx)
	defer cancelChrome()

	if err := chromedp.Run(chromeCtx, chromedp.Navigate(googleSigninURL)); err != nil {
		return nil, fmt.Errorf("browser login: navigate to Google: %w", err)
	}

	deadline := time.Now().Add(browserTimeout)
	for {
		select {
		case <-parentCtx.Done():
			return nil, parentCtx.Err()
		case <-time.After(2 * time.Second):
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("browser login: timed out — please log in within 5 minutes")
		}

		var rawCookies []*network.Cookie
		err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			rawCookies, err = network.GetCookies().WithURLs(cookieURLs).Do(ctx)
			return err
		}))
		if err != nil {
			// Browser might not be ready yet; keep polling.
			continue
		}

		result := make(map[string]string, len(rawCookies))
		for _, c := range rawCookies {
			result[c.Name] = c.Value
		}

		allPresent := true
		for _, name := range requiredCookies {
			if result[name] == "" {
				allPresent = false
				break
			}
		}
		if allPresent {
			return result, nil
		}
	}
}
