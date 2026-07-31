package googlechat

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLeaveGroupDeletesCurrentMembership(t *testing.T) {
	call := 0
	provider := NewGoogleChatProvider()
	provider.selfID = "self-123"
	provider.apiClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if req.Method != http.MethodGet || req.URL.String() != chatAPIBase+"/spaces/space-1" {
				t.Fatalf("unexpected space request: %s %s", req.Method, req.URL)
			}
			return jsonResponse(`{"name":"spaces/space-1","spaceType":"SPACE"}`), nil
		case 2:
			if req.Method != http.MethodGet || req.URL.Path != "/v1/spaces/space-1/members" {
				t.Fatalf("unexpected memberships request: %s %s", req.Method, req.URL)
			}
			if req.URL.Query().Get("pageSize") != "100" {
				t.Fatalf("pageSize = %q, want 100", req.URL.Query().Get("pageSize"))
			}
			return jsonResponse(`{"memberships":[{"name":"spaces/space-1/members/member-9","member":{"name":"users/self-123","type":"HUMAN"}}]}`), nil
		case 3:
			if req.Method != http.MethodDelete || req.URL.String() != chatAPIBase+"/spaces/space-1/members/member-9" {
				t.Fatalf("unexpected delete request: %s %s", req.Method, req.URL)
			}
			return jsonResponse(`{"name":"spaces/space-1/members/member-9","state":"NOT_A_MEMBER"}`), nil
		default:
			t.Fatalf("unexpected HTTP call %d: %s", call, req.URL)
			return nil, nil
		}
	})}

	if err := provider.LeaveGroup("googlechat-1::spaces/space-1"); err != nil {
		t.Fatalf("LeaveGroup: %v", err)
	}
	if call != 3 {
		t.Fatalf("HTTP call count = %d, want 3", call)
	}
}

func TestLeaveGroupRejectsDirectMessage(t *testing.T) {
	provider := NewGoogleChatProvider()
	provider.selfID = "self-123"
	provider.apiClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{"name":"spaces/dm-1","spaceType":"DIRECT_MESSAGE"}`), nil
	})}

	err := provider.LeaveGroup("spaces/dm-1")
	if err == nil || !strings.Contains(err.Error(), "not a group") {
		t.Fatalf("LeaveGroup error = %v, want not-a-group error", err)
	}
}

func TestSelfMembershipNamePaginates(t *testing.T) {
	call := 0
	provider := NewGoogleChatProvider()
	provider.selfID = "self-123"
	provider.apiClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call++
		if call == 1 {
			return jsonResponse(`{"memberships":[],"nextPageToken":"next-token"}`), nil
		}
		if req.URL.Query().Get("pageToken") != "next-token" {
			t.Fatalf("pageToken = %q, want next-token", req.URL.Query().Get("pageToken"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"memberships":[{"name":"spaces/space-1/members/me","member":{"name":"users/self-123"}}]}`,
			)),
		}, nil
	})}

	name, err := provider.selfMembershipName("spaces/space-1")
	if err != nil {
		t.Fatalf("selfMembershipName: %v", err)
	}
	if name != "spaces/space-1/members/me" || call != 2 {
		t.Fatalf("name = %q, calls = %d", name, call)
	}
}
