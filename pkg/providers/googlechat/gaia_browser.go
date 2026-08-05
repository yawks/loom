package googlechat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// apiCParam extracts the numeric value of the ?c= query parameter from a URL.
// Returns 0 if the parameter is absent or non-numeric.
func apiCParam(rawURL string) int {
	idx := strings.Index(rawURL, "?c=")
	if idx == -1 {
		return 0
	}
	s := rawURL[idx+3:]
	if end := strings.IndexByte(s, '&'); end != -1 {
		s = s[:end]
	}
	n, _ := strconv.Atoi(s)
	return n
}

const (
	googleChatSigninURL = "https://accounts.google.com/signin/v2/identifier?continue=https%3A%2F%2Fchat.google.com%2F"
	browserTimeout      = 5 * time.Minute
)

var requiredCookies = []string{"SID", "HSID", "SSID", "APISID", "SAPISID", "OSID"}

var cookieURLs = []string{
	"https://accounts.google.com",
	"https://chat.google.com",
}

func browserCookieParams(cookies map[string]string) []*network.CookieParam {
	params := make([]*network.CookieParam, 0, len(cookies))
	for name, value := range cookies {
		if name == "" || value == "" || strings.HasPrefix(strings.ToUpper(name), "X-") {
			continue
		}
		// URL lets Chromium infer a valid host/path. Supplying Domain manually is
		// rejected for __Host-* cookies and can also conflict with secure prefixes.
		params = append(params, &network.CookieParam{
			Name:   name,
			Value:  value,
			URL:    "https://chat.google.com/",
			Secure: true,
		})
	}
	return params
}

// SilentRefreshWebSession lets the authenticated web client reveal the current
// create_unsent_message URL. It is only used after that endpoint returns 404.
func SilentRefreshWebSession(parentCtx context.Context, existingCookies map[string]string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", true), chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	chromeCtx, cancelChrome := chromedp.NewContext(allocCtx)
	defer cancelChrome()

	var captured string
	var mu sync.Mutex
	chromedp.ListenTarget(chromeCtx, func(event interface{}) {
		req, ok := event.(*network.EventRequestWillBeSent)
		if !ok || !strings.Contains(req.Request.URL, "chat.google.com") || !strings.Contains(req.Request.URL, "/api/") {
			return
		}
		idx := strings.Index(req.Request.URL, "?c=")
		if idx < 0 {
			return
		}
		value := req.Request.URL[idx+3:]
		if end := strings.IndexByte(value, '&'); end >= 0 {
			value = value[:end]
		}
		if value == "" || value == "1" {
			return
		}
		account := "0"
		if u := strings.Index(req.Request.URL, "/u/"); u >= 0 {
			rest := req.Request.URL[u+3:]
			if slash := strings.IndexByte(rest, '/'); slash >= 0 {
				account = rest[:slash]
			}
		}
		candidate := "https://chat.google.com/u/" + account + "/api/create_unsent_message?c=" + value
		mu.Lock()
		if apiCParam(candidate) > apiCParam(captured) {
			captured = candidate
		}
		mu.Unlock()
	})

	params := browserCookieParams(existingCookies)
	if err := chromedp.Run(chromeCtx, network.Enable(), network.SetCookies(params), chromedp.Navigate("https://chat.google.com/u/0/")); err != nil {
		return nil, fmt.Errorf("silent refresh: %w", err)
	}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		mu.Lock()
		found := captured != ""
		mu.Unlock()
		if found {
			break
		}
	}
	mu.Lock()
	apiURLBase := captured
	mu.Unlock()
	if apiURLBase == "" {
		return nil, fmt.Errorf("no Google Chat API request captured; reconnect the provider")
	}
	result := map[string]string{"X-API-URL-Base": apiURLBase}
	var cookies []*network.Cookie
	if err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithURLs(cookieURLs).Do(ctx)
		return err
	})); err == nil {
		for _, cookie := range cookies {
			result[cookie.Name] = cookie.Value
		}
	}
	return result, nil
}

// FetchGoogleChatCookiesViaLogin opens a visible Chrome window on the Google Chat login
// page and polls until session cookies set after successful authentication are present
// and Google Chat has fully loaded.
func FetchGoogleChatCookiesViaLogin(parentCtx context.Context) (map[string]string, error) {
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

	var capturedXsrfToken string
	var capturedAPIURLBase string // e.g. "https://chat.google.com/u/0/api/create_unsent_message?c=55"
	var xsrfMu sync.Mutex

	chromedp.ListenTarget(chromeCtx, func(ev interface{}) {
		if req, ok := ev.(*network.EventRequestWillBeSent); ok {
			reqURL := req.Request.URL

			// Only capture tokens/URLs from chat.google.com (ignore dynamiteintegration, etc.)
			if !strings.Contains(reqURL, "chat.google.com") {
				return
			}

			// Capture the c= parameter from ANY chat.google.com API call.
			// Google Chat makes many /api/* calls on page load, all with the correct c= value.
			// We always update to get the LATEST value (early login requests may have wrong c=).
			if strings.Contains(reqURL, "/api/") {
				if idx := strings.Index(reqURL, "?c="); idx != -1 {
					end := strings.Index(reqURL[idx+3:], "&")
					var cParam string
					if end == -1 {
						cParam = reqURL[idx+3:]
					} else {
						cParam = reqURL[idx+3 : idx+3+end]
					}
					if cParam != "" && cParam != "1" { // skip c=1 from login redirects
						base := "https://chat.google.com/u/0/api/create_unsent_message?c=" + cParam
						if uIdx := strings.Index(reqURL, "/u/"); uIdx != -1 {
							rest := reqURL[uIdx+3:]
							if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
								accountIdx := rest[:slashIdx]
								base = "https://chat.google.com/u/" + accountIdx + "/api/create_unsent_message?c=" + cParam
							}
						}
						xsrfMu.Lock()
						// Keep the HIGHEST observed c= value. Endpoints like list_unsent_messages
						// use a low c= (e.g. 7) while create_unsent_message needs a higher one.
						// Taking the max ensures a low-version call doesn't overwrite a good value.
						if apiCParam(base) > apiCParam(capturedAPIURLBase) {
							capturedAPIURLBase = base
						}
						xsrfMu.Unlock()
					}
				}
			}

			// Capture X-Framework-Xsrf-Token from chat.google.com requests only.
			// The real token has format "hash:timestamp" (e.g. "AI0Pu_...:1785843000000")
			for headerName, headerVal := range req.Request.Headers {
				if strings.EqualFold(headerName, "X-Framework-Xsrf-Token") {
					if strVal, ok := headerVal.(string); ok && strings.Contains(strVal, ":") {
						xsrfMu.Lock()
						capturedXsrfToken = strVal
						xsrfMu.Unlock()
					}
				}
			}
		}
	})

	if err := chromedp.Run(chromeCtx, chromedp.Navigate(googleChatSigninURL)); err != nil {
		return nil, fmt.Errorf("browser login: navigate to Google Chat: %w", err)
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

		var currentURL string
		_ = chromedp.Run(chromeCtx, chromedp.Location(&currentURL))
		if !strings.Contains(currentURL, "chat.google.com") {
			// User is still on accounts.google.com or in mid-redirect
			continue
		}

		var rawCookies []*network.Cookie
		err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			rawCookies, err = network.GetCookies().WithURLs(cookieURLs).Do(ctx)
			return err
		}))
		if err != nil {
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
		if !allPresent {
			continue
		}

		xsrfMu.Lock()
		xsrfToken := capturedXsrfToken
		xsrfMu.Unlock()

		if xsrfToken == "" {
			_ = chromedp.Run(chromeCtx, chromedp.Evaluate(`
				(function() {
					if (window.WIZ_global_data) {
						if (window.WIZ_global_data.at && window.WIZ_global_data.at.includes(':')) {
							return window.WIZ_global_data.at;
						}
						for (var k in window.WIZ_global_data) {
							var v = window.WIZ_global_data[k];
							if (typeof v === 'string' && v.includes(':') && v.length > 15) return v;
						}
					}
					var scripts = document.querySelectorAll('script');
					for (var i = 0; i < scripts.length; i++) {
						var txt = scripts[i].textContent;
						var m = txt.match(/"at":"([^"]+:[0-9]+)"/) || txt.match(/"xsrf_token":"([^"]+:[0-9]+)"/);
						if (m) return m[1];
					}
					return "";
				})()
			`, &xsrfToken))
		}

		if xsrfToken == "" {
			// Google Chat JS environment has not finished initializing XSRF token
			continue
		}

		result["X-Framework-Xsrf-Token"] = xsrfToken

		// Try to capture the real create_unsent_message URL (account index + c= param)
		// by evaluating a request via JS or checking what was intercepted
		xsrfMu.Lock()
		apiURLBase := capturedAPIURLBase
		xsrfMu.Unlock()

		if apiURLBase == "" {
			var accountIdx, clientVersion string
			_ = chromedp.Run(chromeCtx, chromedp.Evaluate(`
				(function() {
					var m = window.location.href.match(/chat\.google\.com\/u\/(\d+)/);
					return m ? m[1] : "0";
				})()
			`, &accountIdx))
			_ = chromedp.Run(chromeCtx, chromedp.Evaluate(`
				(function() {
					// Try well-known JS global config objects
					try {
						if (window._DATASTORE && window._DATASTORE.getCv) return String(window._DATASTORE.getCv());
					} catch(e) {}
					// Try WIZ_global_data for client version hints
					if (window.WIZ_global_data) {
						var d = window.WIZ_global_data;
						for (var k in d) {
							if (k === 'cv' || k === 'clientVersion') return String(d[k]);
						}
					}
					// Scan inline scripts for the ?c= value in chat API URLs
					var scripts = document.querySelectorAll('script');
					for (var i = 0; i < scripts.length; i++) {
						var txt = scripts[i].textContent;
						// Match pattern: create_unsent_message?c=<number>
						var m = txt.match(/create_unsent_message\?c=(\d+)/);
						if (m) return m[1];
						// Match pattern: "cv":"<number>" or cv:<number>
						m = txt.match(/"cv":"?(\d+)"?/) || txt.match(/\bcv\s*:\s*(\d+)/);
						if (m) return m[1];
					}
					return "";
				})()
			`, &clientVersion))

			if accountIdx == "" {
				accountIdx = "0"
			}
			if clientVersion != "" && clientVersion != "1" {
				apiURLBase = "https://chat.google.com/u/" + accountIdx + "/api/create_unsent_message?c=" + clientVersion
			} else {
				apiURLBase = "https://chat.google.com/u/" + accountIdx + "/api/create_unsent_message?c=32"
			}
		}

		result["X-API-URL-Base"] = apiURLBase
		// Allow Google Chat web app to complete initial background session setup
		time.Sleep(2 * time.Second)
		return result, nil
	}
}
