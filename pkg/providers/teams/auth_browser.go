package teams

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"go.mau.fi/mautrix-teams/pkg/msteams"
)

const teamsBrowserLoginTimeout = 10 * time.Minute

// LoginWithBrowser starts Microsoft's device-code flow using the first-party
// Teams client registration. Chrome handles credentials, MFA and Conditional
// Access; Loom never sees the password or browser cookies.
func (p *Provider) LoginWithBrowser(parent context.Context, tenant string) error {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		tenant = "organizations"
	}
	ctx, cancel := context.WithTimeout(parent, teamsBrowserLoginTimeout)
	defer cancel()

	device, err := msteams.StartDeviceCode(ctx, http.DefaultClient, tenant)
	if err != nil {
		return fmt.Errorf("%s: start Microsoft login: %w", providerID, err)
	}

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

	loginURL := device.VerificationURI
	separator := "?"
	if strings.Contains(loginURL, "?") {
		separator = "&"
	}
	// Microsoft's device login page accepts the otc query parameter and
	// pre-populates the code. If a tenant disables it, the page still displays
	// the normal code entry form and the user can enter device.UserCode.
	loginURL += separator + "otc=" + url.QueryEscape(device.UserCode)
	if err := chromedp.Run(chromeCtx, chromedp.Navigate(loginURL)); err != nil {
		return fmt.Errorf("%s: open Microsoft login browser: %w", providerID, err)
	}
	if err := submitMicrosoftDeviceCode(chromeCtx, device.UserCode); err != nil {
		return fmt.Errorf("%s: enter Microsoft device code %q in the browser: %w", providerID, device.UserCode, err)
	}

	token, err := msteams.PollDeviceCode(
		ctx, http.DefaultClient, tenant, device.DeviceCode,
		time.Duration(device.Interval)*time.Second,
	)
	if err != nil {
		return fmt.Errorf("%s: Microsoft login: %w", providerID, err)
	}
	claims, err := msteams.ParseIDToken(token.IDToken)
	if err != nil {
		return fmt.Errorf("%s: parse Microsoft identity: %w", providerID, err)
	}
	if claims.TenantID == "" || claims.ObjectID == "" || token.RefreshToken == "" {
		return fmt.Errorf("%s: Microsoft login returned an incomplete session", providerID)
	}
	displayName := firstNonEmpty(claims.DisplayName, claims.PreferredUN, claims.UPN, claims.Email)
	mri := "8:orgid:" + claims.ObjectID

	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID: claims.TenantID, UserMRI: mri,
		AuthToken: token.AccessToken, RefreshToken: token.RefreshToken,
	})
	if err != nil {
		return fmt.Errorf("%s: create authenticated client: %w", providerID, err)
	}
	if err := client.RefreshSkypeToken(ctx); err != nil {
		_ = client.Close()
		return fmt.Errorf("%s: acquire Teams token: %w", providerID, err)
	}
	auth, skype := client.SnapshotTokens()
	stored := &session{
		TenantID: claims.TenantID, UserMRI: mri, DisplayName: displayName,
		RefreshToken: client.SnapshotRefresh(), ChatSvcBase: client.ChatSvcBase(),
	}
	if auth != nil {
		stored.AuthToken = auth.Value
	}
	if skype != nil {
		stored.SkypeToken = skype.Value
	}
	_ = client.Close()

	p.mu.Lock()
	p.session = stored
	err = p.saveSessionLocked()
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: save Microsoft session: %w", providerID, err)
	}
	return p.Connect()
}

// submitMicrosoftDeviceCode handles both current Microsoft device-login page
// variants. The otc query parameter normally pre-fills the code, but some
// enterprise tenants discard it during redirects and render an empty form.
func submitMicrosoftDeviceCode(ctx context.Context, code string) error {
	const script = `(code => {
		const input = document.querySelector(
			'input[name="otc"], input#otc, input[name="deviceCode"], input[type="text"]'
		);
		if (!input) return false;
		if (!input.value) {
			const setter = Object.getOwnPropertyDescriptor(
				HTMLInputElement.prototype, "value"
			).set;
			setter.call(input, code);
			input.dispatchEvent(new Event("input", { bubbles: true }));
			input.dispatchEvent(new Event("change", { bubbles: true }));
		}
		const button = document.querySelector(
			'button[type="submit"], input[type="submit"], #idSIButton9'
		);
		if (!button || !input.value) return false;
		button.click();
		return true;
	})`

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var submitted bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(script+"("+fmt.Sprintf("%q", code)+")", &submitted)); err == nil && submitted {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("Microsoft device-code form was not detected")
}
