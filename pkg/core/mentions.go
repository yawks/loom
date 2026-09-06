package core

import (
	"sort"
	"unicode/utf16"
)

// FormatMentions replaces canonical browser textarea ranges from right to left.
func FormatMentions(text string, mentions []Mention, format func(Mention) string) string {
	type replacement struct {
		start, end int
		mention    Mention
	}
	runes := []rune(text)
	utf16Offset := func(target int) int {
		if target <= 0 {
			return 0
		}
		units := 0
		for i, r := range runes {
			width := len(utf16.Encode([]rune{r}))
			if units+width > target {
				return i
			}
			units += width
			if units == target {
				return i + 1
			}
		}
		return len(runes)
	}
	replacements := make([]replacement, 0, len(mentions))
	for _, mention := range mentions {
		if mention.UserID == "" || mention.Start < 0 || mention.Length <= 0 {
			continue
		}
		start, end := utf16Offset(mention.Start), utf16Offset(mention.Start+mention.Length)
		if start <= end && end <= len(runes) {
			replacements = append(replacements, replacement{start, end, mention})
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, replacement := range replacements {
		runes = append(append(append([]rune{}, runes[:replacement.start]...), []rune(format(replacement.mention))...), runes[replacement.end:]...)
	}
	return string(runes)
}
