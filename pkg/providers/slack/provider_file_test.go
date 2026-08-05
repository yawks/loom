package slack

import "testing"

func TestNormalizeSlackDownloadURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "private file",
			in:   "https://files.slack.com/files-pri/T0-F0/screenshot.png",
			want: "https://files.slack.com/files-pri/T0-F0/download/screenshot.png",
		},
		{
			name: "already download URL",
			in:   "https://files.slack.com/files-pri/T0-F0/download/screenshot.png",
			want: "https://files.slack.com/files-pri/T0-F0/download/screenshot.png",
		},
		{
			name: "thumbnail unchanged",
			in:   "https://files.slack.com/files-tmb/T0-F0/thumb.png",
			want: "https://files.slack.com/files-tmb/T0-F0/thumb.png",
		},
		{
			name: "external host unchanged",
			in:   "https://example.com/files-pri/T0-F0/screenshot.png",
			want: "https://example.com/files-pri/T0-F0/screenshot.png",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeSlackDownloadURL(test.in); got != test.want {
				t.Fatalf("normalizeSlackDownloadURL(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
