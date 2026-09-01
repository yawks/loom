package whatsapp

import (
	"errors"
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestIsExpectedAvatarAbsence(t *testing.T) {
	for _, err := range []error{
		whatsmeow.ErrProfilePictureNotSet,
		fmt.Errorf("wrapped: %w", whatsmeow.ErrProfilePictureUnauthorized),
		errors.New("that user or group does not have a profile picture"),
		errors.New("info query returned status 401: not-authorized"),
	} {
		if !isExpectedAvatarAbsence(err) {
			t.Errorf("expected avatar absence for %q", err)
		}
	}
	if isExpectedAvatarAbsence(errors.New("network timeout")) {
		t.Fatal("transient network error classified as avatar absence")
	}
}
