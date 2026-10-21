package teams

import (
	"testing"

	"Loom/pkg/providers/messageformat"
	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func TestTeamsTextRoundTrip(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"A & B", "literal &amp;amp;", "one\ntwo", "one\n\ntwo", "one\n\n\ntwo",
		"- item\n\nnext", "title\n\n- item\n\nnext",
		"title\n- first\n- second\nnext",
		"*Section*\n\n- **First**\n- Second\n\n\n\n  \n\n*Next section*\n\n- Third",
	} {
		for _, contentType := range []string{"html", "Html", "HTML"} {
			remote := msteams.Message{Content: msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(input)), ContentType: contentType}
			got := NewProvider().toModelMessage(client, remote, "room")
			want := input
			if input == "literal &amp;amp;" {
				want = "literal &amp;"
			}
			if got.Body != want {
				t.Errorf("round trip (%s) %q = %q", contentType, input, got.Body)
			}
		}
	}
}

func TestTeamsHTMLVisualLineBreaks(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"<p>one<br></p><p>two</p>", "one\n\ntwo"},
		{"<p>one</p><p><br></p><p>two</p>", "one\n\n\ntwo"},
		{"one<br><br><br>two", "one\n\n\ntwo"},
		{"<p>A &amp; B<br>two</p>", "A & B\ntwo"},
		{"<p>one</p>\r\n<p>two</p>", "one\ntwo"},
		{"<p>one</p>\r\n<p>\u00a0</p>\r\n<p>two</p>", "one\n \ntwo"},
		{"<div>\n  <p>one</p>\n  <p>two</p>\n</div>", "one\ntwo"},
		{"<p>one<br>\ntwo</p>", "one\n\ntwo"},
		{"<p>one <strong>two</strong>\n <em>three</em></p>", "one **two**\n *three*"},
	} {
		if got := teamsHTMLToMarkdown(tc.input); got != tc.want {
			t.Errorf("teamsHTMLToMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
