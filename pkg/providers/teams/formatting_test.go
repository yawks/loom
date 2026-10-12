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
	for _, input := range []string{"A & B", "literal &amp;", "one\ntwo", "one\n\ntwo", "one\n\n\ntwo"} {
		for _, contentType := range []string{"html", "Html", "HTML"} {
			remote := msteams.Message{Content: msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(input)), ContentType: contentType}
			got := NewProvider().toModelMessage(client, remote, "room")
			if got.Body != input {
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
	} {
		if got := teamsHTMLToMarkdown(tc.input); got != tc.want {
			t.Errorf("teamsHTMLToMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
