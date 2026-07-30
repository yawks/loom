// Package messageformat converts Loom's small, common Markdown dialect to the
// syntax understood by each messaging provider.
package messageformat

import (
	"html"
	"regexp"
	"strings"
)

var (
	linkPattern      = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	underlinePattern = regexp.MustCompile(`(?s)<u>(.*?)</u>`)
	boldPattern      = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	strikePattern    = regexp.MustCompile(`~~([^~\n]+)~~`)
	italicPattern    = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*`)
	bulletPattern    = regexp.MustCompile(`(?m)^[ \t]*[-+*][ \t]+`)
	orderedPattern   = regexp.MustCompile(`(?m)^[ \t]*([0-9]+)[.)][ \t]+`)
)

// Slack returns Slack mrkdwn while keeping lists readable as ASCII.
func Slack(markdown string) string {
	out := linkPattern.ReplaceAllString(markdown, `<$2|$1>`)
	out = underlinePattern.ReplaceAllString(out, `_${1}_`)
	out = italicPattern.ReplaceAllString(out, `${1}_${2}_`)
	out = boldPattern.ReplaceAllString(out, `*${1}*`)
	out = strikePattern.ReplaceAllString(out, `~${1}~`)
	return out
}

// WhatsApp returns the lightweight formatting syntax accepted by WhatsApp.
func WhatsApp(markdown string) string {
	out := linkPattern.ReplaceAllString(markdown, `$1 ($2)`)
	out = underlinePattern.ReplaceAllString(out, `_${1}_`)
	out = italicPattern.ReplaceAllString(out, `${1}_${2}_`)
	out = boldPattern.ReplaceAllString(out, `*${1}*`)
	out = strikePattern.ReplaceAllString(out, `~${1}~`)
	return out
}

// GoogleChat returns the formatting syntax accepted in Google Chat messages.
func GoogleChat(markdown string) string {
	return WhatsApp(markdown)
}

// PlainText degrades formatting to readable ASCII for providers without rich
// text support. Unsupported underline and strike remain visible as _x_ / ~x~.
func PlainText(markdown string) string {
	out := linkPattern.ReplaceAllString(markdown, `$1 ($2)`)
	out = underlinePattern.ReplaceAllString(out, `_${1}_`)
	out = italicPattern.ReplaceAllString(out, `${1}${2}`)
	out = boldPattern.ReplaceAllString(out, `$1`)
	out = strikePattern.ReplaceAllString(out, `~${1}~`)
	out = bulletPattern.ReplaceAllString(out, "* ")
	out = orderedPattern.ReplaceAllString(out, "$1. ")
	return out
}

// TeamsHTML converts the common dialect to the small HTML subset supported by
// Teams. Input text is escaped before formatting tags are introduced.
func TeamsHTML(markdown string) string {
	const underlineOpen = "LOOMUNDERLINEOPEN"
	const underlineClose = "LOOMUNDERLINECLOSE"
	out := strings.ReplaceAll(markdown, "<u>", underlineOpen)
	out = strings.ReplaceAll(out, "</u>", underlineClose)
	out = html.EscapeString(out)
	out = linkPattern.ReplaceAllString(out, `<a href="$2">$1</a>`)
	out = strings.ReplaceAll(out, underlineOpen, "<u>")
	out = strings.ReplaceAll(out, underlineClose, "</u>")
	out = italicPattern.ReplaceAllString(out, `${1}<em>$2</em>`)
	out = boldPattern.ReplaceAllString(out, `<strong>$1</strong>`)
	out = strikePattern.ReplaceAllString(out, `<s>$1</s>`)

	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	var result strings.Builder
	listType := ""
	closeList := func() {
		if listType != "" {
			result.WriteString("</" + listType + ">")
			listType = ""
		}
	}
	for index, line := range lines {
		isBullet := bulletPattern.MatchString(line)
		isOrdered := orderedPattern.MatchString(line)
		if isBullet || isOrdered {
			wanted := "ul"
			content := bulletPattern.ReplaceAllString(line, "")
			if isOrdered {
				wanted = "ol"
				content = orderedPattern.ReplaceAllString(line, "")
			}
			if listType != wanted {
				closeList()
				result.WriteString("<" + wanted + ">")
				listType = wanted
			}
			result.WriteString("<li>" + content + "</li>")
			continue
		}
		closeList()
		if index > 0 {
			result.WriteString("<br>")
		}
		result.WriteString(line)
	}
	closeList()
	return result.String()
}
