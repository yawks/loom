package whatsapp

import "testing"

func TestIsWhatsAppImageMIME(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{mimeType: "image/jpeg", want: true},
		{mimeType: "IMAGE/PNG", want: true},
		{mimeType: " image/webp ", want: true},
		{mimeType: "image/heic", want: false},
		{mimeType: "image/heif", want: false},
		{mimeType: "application/octet-stream", want: false},
	}

	for _, test := range tests {
		t.Run(test.mimeType, func(t *testing.T) {
			if got := isWhatsAppImageMIME(test.mimeType); got != test.want {
				t.Fatalf("isWhatsAppImageMIME(%q) = %v, want %v", test.mimeType, got, test.want)
			}
		})
	}
}
