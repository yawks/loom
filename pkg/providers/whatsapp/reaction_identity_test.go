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
