package googlechat

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListAndUnpinMessagePins(t *testing.T) {
	provider := NewGoogleChatProvider()
	call := 0
	provider.apiClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if req.Method != http.MethodGet || req.URL.String() != chatAPIBase+"/spaces/space-1/messagePins?pageSize=100" {
				t.Fatalf("unexpected list request: %s %s", req.Method, req.URL)
			}
			return jsonResponse(`{"messagePins":[{"name":"spaces/space-1/messagePins/msg.1","message":"spaces/space-1/messages/msg.1"}]}`), nil
		case 2:
			if req.Method != http.MethodDelete || req.URL.Path != "/v1/spaces/space-1/messagePins/msg.1" {
				t.Fatalf("unexpected delete request: %s %s", req.Method, req.URL)
			}
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		default:
			t.Fatalf("unexpected extra request")
			return nil, nil
		}
	})}

	pins, err := provider.ListMessagePins("googlechat-1::spaces/space-1")
	if err != nil || len(pins) != 1 || pins[0].ProtocolMsgID != "spaces/space-1/messages/msg.1" {
		t.Fatalf("ListMessagePins() = %+v, %v", pins, err)
	}
	if err := provider.UnpinMessage("googlechat-1::spaces/space-1", pins[0].ProtocolMsgID); err != nil {
		t.Fatalf("UnpinMessage() error = %v", err)
	}
}
