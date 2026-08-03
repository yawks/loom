package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"testing"
)

func TestMessagePinCapabilities(t *testing.T) {
	provider := NewSlackProvider()
	var _ core.MessagePinProvider = provider

	capabilities := provider.GetCapabilities()
	if !capabilities.SupportsPinMessage || !capabilities.SupportsListMessagePins {
		t.Fatalf("expected Slack message pin capabilities, got %+v", capabilities)
	}
	if capabilities.MessagePinScope != string(models.MessagePinScopeShared) {
		t.Fatalf("expected shared Slack pins, got %q", capabilities.MessagePinScope)
	}
}
