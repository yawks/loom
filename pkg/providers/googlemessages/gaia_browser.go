package googlemessages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	googleSigninURL = "https://accounts.google.com/signin/v2/identifier?continue=https%3A%2F%2Fmessages.google.com%2Fweb%2Fconfig"
	messagesHost    = "messages.google.com"
	browserTimeout  = 3 * time.Minute
)

// FetchGoogleCookiesViaLogin opens a visible Chrome window, fills the provided
// credentials, then waits (up to 3 minutes) for the user to reach
// messages.google.com — giving time to complete 2FA or account challenges.
// Returns the session cookies required for Gaia pairing.
func FetchGoogleCookiesViaLogin(parentCtx context.Context, email, password string) (map[string]string, error) {
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

	if err := chromedp.Run(chromeCtx,
		chromedp.Navigate(googleSigninURL),
		chromedp.WaitVisible(`input[type="email"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="email"]`, email, chromedp.ByQuery),
		chromedp.Click(`#identifierNext`, chromedp.ByID),
		chromedp.WaitVisible(`input[type="password"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="password"]`, password, chromedp.ByQuery),
		chromedp.Click(`#passwordNext`, chromedp.ByID),
	); err != nil {
		return nil, fmt.Errorf("browser login: fill credentials: %w", err)
	}

	// Poll until the browser reaches messages.google.com. The user can complete
	// 2FA or any account challenge in the visible window during this wait.
	deadline := time.Now().Add(browserTimeout)
	for {
		select {
		case <-parentCtx.Done():
			return nil, parentCtx.Err()
		case <-time.After(2 * time.Second):
		}
		var currentURL string
		if err := chromedp.Run(chromeCtx, chromedp.Location(&currentURL)); err != nil {
			return nil, fmt.Errorf("browser login: get current URL: %w", err)
		}
		if strings.Contains(currentURL, messagesHost) {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("browser login: timed out — complete 2FA in the browser window and try again")
		}
	}

	// GetCookies without URLs returns all cookies applicable to the current
	// page — including .google.com domain cookies (SID, HSID, …) since
	// messages.google.com is a subdomain of google.com.
	var rawCookies []*network.Cookie
	if err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		rawCookies, err = network.GetCookies().Do(ctx)
		return err
	})); err != nil {
		return nil, fmt.Errorf("browser login: extract cookies: %w", err)
	}

	result := make(map[string]string, len(rawCookies))
	for _, c := range rawCookies {
		result[c.Name] = c.Value
	}

	for _, name := range []string{"SID", "HSID", "SSID", "OSID", "APISID", "SAPISID"} {
		if result[name] == "" {
			return nil, fmt.Errorf("browser login: missing cookie %q — login did not complete", name)
		}
	}

	return result, nil
}
