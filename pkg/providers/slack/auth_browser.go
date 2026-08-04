package slack

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const slackBrowserTimeout = 10 * time.Minute

// LoginWithBrowser opens Chrome on the Slack login page and waits for the user
// to authenticate. Once logged in, the d cookie (xoxd-) and xoxc client token
// are extracted from the browser and persisted in the session file. Existing
// conversations in the database are unaffected.
func (p *SlackProvider) LoginWithBrowser(parentCtx context.Context, workspaceURL string) error {
	ctx, cancel := context.WithTimeout(parentCtx, slackBrowserTimeout)
	defer cancel()

	startURL := normalizeSlackWorkspaceURL(workspaceURL)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	chromeCtx, cancelChrome := chromedp.NewContext(allocCtx)
	defer cancelChrome()

	if err := chromedp.Run(chromeCtx, chromedp.Navigate(startURL)); err != nil {
		return fmt.Errorf("slack: open browser: %w", err)
	}

	deadline := time.Now().Add(slackBrowserTimeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("slack: browser login timed out — please log in within 10 minutes")
		}

		var rawCookies []*network.Cookie
		err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(c context.Context) error {
			var e error
			rawCookies, e = network.GetCookies().
				WithURLs([]string{"https://slack.com", "https://app.slack.com"}).Do(c)
			return e
		}))
		if err != nil {
			continue
		}

		var dCookie string
		for _, c := range rawCookies {
			if c.Name == "d" && strings.HasPrefix(c.Value, "xoxd-") {
				dCookie = c.Value
				break
			}
		}
		if dCookie == "" {
			continue
		}

		var token string
		_ = chromedp.Run(chromeCtx, chromedp.Evaluate(`(function(){
			try {
				if (window.TS && window.TS.boot_data && window.TS.boot_data.api_token) {
					var t = window.TS.boot_data.api_token;
					if (t.indexOf('xoxc-') === 0) return t;
				}
				var lc = localStorage.getItem('localConfig_v2');
				if (lc) {
					var cfg = JSON.parse(lc);
					var teams = cfg && cfg.teams;
					if (teams) {
						for (var id in teams) {
							var tok = teams[id] && teams[id].token;
							if (tok && tok.indexOf('xoxc-') === 0) return tok;
						}
					}
				}
			} catch(e) {}
			return '';
		})()`, &token))

		if !strings.HasPrefix(token, "xoxc-") {
			continue
		}

		p.mu.Lock()
		p.session = &slackSession{Token: token, DCookie: dCookie}
		saveErr := p.saveSessionLocked()
		// Merge into config so Connect() and GetCapabilities() read the new values.
		p.config["token"] = token
		p.config["d_cookie"] = dCookie
		p.mu.Unlock()

		if saveErr != nil {
			return fmt.Errorf("slack: save session: %w", saveErr)
		}

		// Rebuild the Slack client with the new credentials.
		if err := p.SetConfig(p.config); err != nil {
			return fmt.Errorf("slack: apply session: %w", err)
		}
		return p.Connect()
	}
}

// normalizeSlackWorkspaceURL turns a user-supplied workspace identifier into a
// full HTTPS URL that Chrome can navigate to.
func normalizeSlackWorkspaceURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return "https://slack.com/signin"
	}
	if strings.HasPrefix(input, "https://") || strings.HasPrefix(input, "http://") {
		return input
	}
	if strings.Contains(input, ".") {
		return "https://" + input
	}
	return "https://" + input + ".slack.com"
}
