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

func TestScheduledMessageCapabilities(t *testing.T) {
	provider := NewSlackProvider()
	var _ core.ScheduledMessageProvider = provider
	capabilities := provider.GetCapabilities()
	if !capabilities.SupportsScheduledMessages || !capabilities.SupportsListScheduledMessages {
		t.Fatalf("expected Slack scheduled message capabilities, got %+v", capabilities)
	}
}
