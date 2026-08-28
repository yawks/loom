package whatsapp

import "testing"

func TestCanonicalReactionUserIDUsesKnownLIDMapping(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.lidToJIDMap["165575468785791@lid"] = "33662258100@s.whatsapp.net"

	if got := provider.canonicalReactionUserID("165575468785791@lid"); got != "33662258100@s.whatsapp.net" {
		t.Fatalf("canonical reaction user ID = %q", got)
	}
	if got := provider.canonicalReactionUserID("33650401244@s.whatsapp.net"); got != "33650401244@s.whatsapp.net" {
		t.Fatalf("phone-number JID changed to %q", got)
	}
}

func TestNormalizeParticipantIDRemovesDeviceAndResolvesKnownLID(t *testing.T) {
	provider := &WhatsAppProvider{
		lidToJIDMap: map[string]string{
			"165575468785791@lid": "33662258100@s.whatsapp.net",
		},
	}

	for input, want := range map[string]string{
		"33650401244:7@s.whatsapp.net": "33650401244@s.whatsapp.net",
		"165575468785791:4@lid":        "33662258100@s.whatsapp.net",
		"not-a-jid":                    "not-a-jid",
	} {
		if got := provider.NormalizeParticipantID(input); got != want {
			t.Errorf("NormalizeParticipantID(%q) = %q, want %q", input, got, want)
		}
	}
}
