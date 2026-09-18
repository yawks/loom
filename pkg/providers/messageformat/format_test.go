package messageformat

import (
	"strings"
	"testing"
)

const sample = "**bold** *italic* <u>under</u> ~~gone~~ [site](https://example.com)\n- one\n1. first"

func TestTeamsHTMLLinks(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"bare URL", "https://example.com", `<a href="https://example.com">https://example.com</a>`},
		{"query escaping", "https://example.com/?a=1&b=2", `<a href="https://example.com/?a=1&amp;b=2">https://example.com/?a=1&amp;b=2</a>`},
		{"punctuation", "Voir (https://example.com/page).", `Voir (<a href="https://example.com/page">https://example.com/page</a>).`},
		{"balanced parentheses", "https://example.com/Page_(topic)", `<a href="https://example.com/Page_(topic)">https://example.com/Page_(topic)</a>`},
		{"explicit link", "[site](https://example.com)", `<a href="https://example.com">site</a>`},
		{"emphasis", "**https://example.com**", `<strong><a href="https://example.com">https://example.com</a></strong>`},
		{"URL formatting preserved", "https://example.com/~~path~~", `<a href="https://example.com/~~path~~">https://example.com/~~path~~</a>`},
		{"multiple links", "http://one.test\nhttps://two.test", `<a href="http://one.test">http://one.test</a><br><a href="https://two.test">https://two.test</a>`},
		{"HTML escaped", `<script> https://example.com/"onclick="x`, `&lt;script&gt; <a href="https://example.com/">https://example.com/</a>&#34;onclick=&#34;x`},
		{"unsafe scheme", "[bad](javascript:alert(1))", "[bad](javascript:alert(1))"},
		{"token collision", "LOOMLINKTOKEN0END https://example.com", `LOOMLINKTOKEN0END <a href="https://example.com">https://example.com</a>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TeamsHTML(test.input); got != test.want {
				t.Fatalf("TeamsHTML(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestProviderFormatting(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want []string
	}{
		{"slack", Slack(sample), []string{"*bold*", "_italic_", "_under_", "~gone~", "<https://example.com|site>", "- one", "1. first"}},
		{"whatsapp", WhatsApp(sample), []string{"*bold*", "_italic_", "_under_", "~gone~", "site (https://example.com)"}},
		{"google chat", GoogleChat(sample), []string{"**bold**", "*italic*", "under", "~~gone~~", "[site](https://example.com)", "- one", "1. first"}},
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

func TestTeamsHTMLCanonicalRichStylesAndCode(t *testing.T) {
	got := TeamsHTML("<loom-style color=\"#c4314b\" background=\"rgb(255, 240, 200)\" size=\"18px\">**Important**</loom-style> and `literal **code**`\n```go\nreturn \"<safe>\"\n```")
	for _, want := range []string{
		`<span style="color:#c4314b;background-color:rgb(255, 240, 200);font-size:18px"><strong>Important</strong></span>`,
		`<code>literal **code**</code>`,
		`<pre><code>return &#34;&lt;safe&gt;&#34;</code></pre>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("TeamsHTML rich output %q does not contain %q", got, want)
		}
	}
}

func TestUnsupportedCanonicalStylesDoNotLeak(t *testing.T) {
	input := `<loom-style color="red" size="18px"><u>hello</u></loom-style>`
	if got := GoogleChat(input); got != "hello" {
		t.Fatalf("GoogleChat(%q) = %q, want plain unsupported styles", input, got)
	}
	if got := PlainText(input); got != "_hello_" {
		t.Fatalf("PlainText(%q) = %q, want readable fallback", input, got)
	}
}
