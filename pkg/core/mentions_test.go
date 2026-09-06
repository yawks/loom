package core

import "testing"

func TestFormatMentionsUsesBrowserUTF16Offsets(t *testing.T) {
	text := "🙂 hello @Zoë"
	got := FormatMentions(text, []Mention{{UserID: "user-1", DisplayName: "Zoë", Start: 9, Length: 4}}, func(m Mention) string {
		return "<" + m.UserID + ">"
	})
	if got != "🙂 hello <user-1>" {
		t.Fatalf("unexpected formatted mention: %q", got)
	}
}

func TestFormatMentionsReplacesFromRightToLeft(t *testing.T) {
	text := "@Amy and @Bob"
	mentions := []Mention{
		{UserID: "a", Start: 0, Length: 4},
		{UserID: "b", Start: 9, Length: 4},
	}
	got := FormatMentions(text, mentions, func(m Mention) string { return "<" + m.UserID + ">" })
	if got != "<a> and <b>" {
		t.Fatalf("unexpected formatted mentions: %q", got)
	}
}
