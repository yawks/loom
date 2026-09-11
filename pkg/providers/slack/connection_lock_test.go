package slack

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
)

type blockingAuthTransport struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (transport *blockingAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, "/auth.test") {
		transport.once.Do(func() { close(transport.started) })
		select {
		case <-transport.release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	}
	body := `{"ok":true}`
	if strings.HasSuffix(request.URL.Path, "/auth.test") {
		body = `{"ok":true,"user":"Tester","user_id":"U123","team":"Test","team_id":"T123"}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func TestConnectDoesNotHoldProviderLockDuringAuthRequest(t *testing.T) {
	transport := &blockingAuthTransport{started: make(chan struct{}), release: make(chan struct{})}
	provider := NewSlackProvider()
	provider.config = map[string]any{"_instance_id": "slack-1", "token": "xoxp-test", "slack_mode": "compatible"}
	provider.client = slackapi.New("xoxp-test",
		slackapi.OptionAPIURL("https://slack.test/"),
		slackapi.OptionHTTPClient(&http.Client{Transport: transport}),
	)

	connectDone := make(chan error, 1)
	go func() { connectDone <- provider.Connect() }()
	<-transport.started

	configRead := make(chan struct{})
	go func() {
		_ = provider.GetConfig()
		close(configRead)
	}()
	select {
	case <-configRead:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("GetConfig blocked behind the in-flight Slack auth request")
	}

	close(transport.release)
	if err := <-connectDone; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}
