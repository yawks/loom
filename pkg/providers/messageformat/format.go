// Package messageformat converts Loom's small, common Markdown dialect to the
// syntax understood by each messaging provider.
package messageformat

import (
	"html"
	"regexp"
	"strconv"
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
	teamsLinkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)|https?://[^\s<>"'\x60*]+`)
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
	// Protect links before applying emphasis: URL contents must never become
	// formatting tags, and explicit Markdown links must not be linked twice.
	prefix := "LOOMLINKTOKEN"
	for strings.Contains(markdown, prefix) {
		prefix += "X"
	}
	var links []string
	markdown = teamsLinkPattern.ReplaceAllStringFunc(markdown, func(match string) string {
		label, target, suffix := match, match, ""
		if parts := linkPattern.FindStringSubmatch(match); parts != nil {
			label, target = parts[1], parts[2]
		} else {
			target = strings.TrimRight(target, ".,;:!?")
			for strings.HasSuffix(target, ")") && strings.Count(target, ")") > strings.Count(target, "(") {
				target = strings.TrimSuffix(target, ")")
			}
			for strings.HasSuffix(target, "]") && strings.Count(target, "]") > strings.Count(target, "[") {
				target = strings.TrimSuffix(target, "]")
			}
			label, suffix = target, match[len(target):]
		}
		token := prefix + strconv.Itoa(len(links)) + "END"
		links = append(links, `<a href="`+html.EscapeString(target)+`">`+html.EscapeString(label)+`</a>`)
		return token + suffix
	})
	const underlineOpen = "LOOMUNDERLINEOPEN"
	const underlineClose = "LOOMUNDERLINECLOSE"
	out := strings.ReplaceAll(markdown, "<u>", underlineOpen)
	out = strings.ReplaceAll(out, "</u>", underlineClose)
	out = html.EscapeString(out)
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
	out = result.String()
	for index, link := range links {
		out = strings.ReplaceAll(out, prefix+strconv.Itoa(index)+"END", link)
	}
	return out
}
