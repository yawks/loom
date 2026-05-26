package googlechat

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/pkg/browser"
	"golang.org/x/oauth2"
)

// runOAuthFlow opens the browser for Google OAuth2 authorization.
// It starts a local callback server, waits for the authorization code,
// then exchanges it for tokens and starts the provider connection.
func (p *GoogleChatProvider) runOAuthFlow(ctx context.Context, oauthConf *oauth2.Config) {
	// Find a free port.
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		p.log("GoogleChatProvider.runOAuthFlow: failed to bind port: %v\n", err)
		return
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://localhost:%d/callback", port)

	// Clone config with dynamic redirect URI.
	conf := *oauthConf
	conf.RedirectURL = redirectURL

	codeCh := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("<html><body><h2>Authorization successful!</h2><p>You can close this window and return to Loom.</p></body></html>"))
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	authURL := conf.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	p.log("GoogleChatProvider.runOAuthFlow: opening browser for authorization\n")
	if err := browser.OpenURL(authURL); err != nil {
		p.log("GoogleChatProvider.runOAuthFlow: failed to open browser: %v — open manually: %s\n", err, authURL)
	}

	select {
	case code := <-codeCh:
		token, err := conf.Exchange(ctx, code)
		if err != nil {
			p.log("GoogleChatProvider.runOAuthFlow: token exchange failed: %v\n", err)
			return
		}
		if err := p.saveToken(token); err != nil {
			p.log("GoogleChatProvider.runOAuthFlow: failed to save token: %v\n", err)
			return
		}
		p.log("GoogleChatProvider.runOAuthFlow: authorization complete, connecting\n")
		_ = p.connectWithToken(ctx, &conf, token)

	case <-time.After(5 * time.Minute):
		p.log("GoogleChatProvider.runOAuthFlow: timed out waiting for authorization\n")

	case <-ctx.Done():
		p.log("GoogleChatProvider.runOAuthFlow: cancelled\n")
	}
}
