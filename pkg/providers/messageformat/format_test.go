package messageformat

import (
	"strings"
	"testing"
)

const sample = "**bold** *italic* <u>under</u> ~~gone~~ [site](https://example.com)\n- one\n1. first"

func TestProviderFormatting(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want []string
	}{
		{"slack", Slack(sample), []string{"*bold*", "_italic_", "_under_", "~gone~", "<https://example.com|site>", "- one", "1. first"}},
		{"whatsapp", WhatsApp(sample), []string{"*bold*", "_italic_", "_under_", "~gone~", "site (https://example.com)"}},
		{"plain", PlainText(sample), []string{"bold", "italic", "_under_", "~gone~", "site (https://example.com)", "* one", "1. first"}},
		{"teams", TeamsHTML(sample), []string{"<strong>bold</strong>", "<em>italic</em>", "<u>under</u>", "<s>gone</s>", `<a href="https://example.com">site</a>`, "<ul><li>one</li></ul>", "<ol><li>first</li></ol>"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, want := range test.want {
				if !strings.Contains(test.got, want) {
					t.Fatalf("%q does not contain %q", test.got, want)
				}
			}
		})
	}
}
