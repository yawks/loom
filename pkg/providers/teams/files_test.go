package teams

import "testing"

func TestFirstNonEmptySharedFileURLFallsBackToStoredAttachmentURL(t *testing.T) {
	const storedURL = "https://tenant.sharepoint.com/personal/user/Documents/video.mov"
	if got := firstNonEmptySharedFileURL("", "", storedURL); got != storedURL {
		t.Fatalf("unexpected fallback URL: %q", got)
	}
}

func TestFirstNonEmptySharedFileURLPrefersShareURL(t *testing.T) {
	const shareURL = "https://tenant.sharepoint.com/:v:/g/shared"
	if got := firstNonEmptySharedFileURL(shareURL, "https://tenant.sharepoint.com/file", "stored"); got != shareURL {
		t.Fatalf("unexpected preferred URL: %q", got)
	}
}
