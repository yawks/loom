package slack

import "testing"

func TestNormalizeSlackReactionName(t *testing.T) {
	tests := map[string]string{
		"bicep":              "muscle",
		"flexed_biceps":      "muscle",
		"muscle":             "muscle",
		"man_raising_hand":   "man-raising-hand",
		"woman_raising_hand": "woman-raising-hand",
		"raising_hand":       "raising_hand",
		"custom_emoji":       "custom_emoji",
	}

	for input, want := range tests {
		if got := normalizeSlackReactionName(input); got != want {
			t.Errorf("normalizeSlackReactionName(%q) = %q, want %q", input, got, want)
		}
	}
}
