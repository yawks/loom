package slack

import (
	_ "embed"
	"encoding/json"
)

// Unicode to Slack shortcodes, generated from iamcal/emoji-data emoji.json
// (2026-09-11). See emoji-data.LICENSE. Variation selectors are optional.
//
//go:embed emoji_names.json
var slackEmojiJSON []byte

var slackUnicodeNames = func() map[string]string {
	names := make(map[string]string)
	if err := json.Unmarshal(slackEmojiJSON, &names); err != nil {
		panic(err)
	}
	return names
}()
