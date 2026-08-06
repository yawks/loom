package slack

import "testing"

func TestIsSlackHuddleSubtype(t *testing.T) {
	for _, subtype := range []string{"sh_room_created", "huddle_thread"} {
		if !isSlackHuddleSubtype(subtype) {
			t.Errorf("isSlackHuddleSubtype(%q) = false, want true", subtype)
		}
	}
	for _, subtype := range []string{"", "message_changed", "channel_join"} {
		if isSlackHuddleSubtype(subtype) {
			t.Errorf("isSlackHuddleSubtype(%q) = true, want false", subtype)
		}
	}
}

func TestHuddleLinkPrefersJoinURL(t *testing.T) {
	provider := &SlackProvider{teamID: "T123"}
	joinURL := "https://acme.slack.com/huddle/C123/abc"

	gotURL, gotAction := provider.huddleLink("<"+joinURL+"|Join huddle>", "C123")
	if gotURL != joinURL || gotAction != "join" {
		t.Fatalf("huddleLink() = (%q, %q), want (%q, %q)", gotURL, gotAction, joinURL, "join")
	}
}

func TestHuddleLinkFallsBackToWebConversation(t *testing.T) {
	provider := &SlackProvider{teamID: "T123"}

	gotURL, gotAction := provider.huddleLink("", "C456")
	if gotURL != "https://app.slack.com/client/T123/C456" || gotAction != "open" {
		t.Fatalf("huddleLink() = (%q, %q), want Slack conversation link with open action", gotURL, gotAction)
	}
}

func TestHuddleLinkWithoutWorkspace(t *testing.T) {
	provider := &SlackProvider{}

	gotURL, gotAction := provider.huddleLink("", "C456")
	if gotURL != "" || gotAction != "" {
		t.Fatalf("huddleLink() = (%q, %q), want empty values", gotURL, gotAction)
	}
}
