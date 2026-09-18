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
	linkPattern               = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	underlinePattern          = regexp.MustCompile(`(?s)<u>(.*?)</u>`)
	boldPattern               = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	strikePattern             = regexp.MustCompile(`~~([^~\n]+)~~`)
	italicPattern             = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*`)
	bulletPattern             = regexp.MustCompile(`(?m)^[ \t]*[-+*][ \t]+`)
	orderedPattern            = regexp.MustCompile(`(?m)^[ \t]*([0-9]+)[.)][ \t]+`)
	teamsLinkPattern          = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)|https?://[^\s<>"'\x60*]+`)
	fencedCodePattern         = regexp.MustCompile("(?s)```(?:[^\\n`]*)\\n(.*?)\\n?```")
	inlineCodePattern         = regexp.MustCompile("`([^`\\n]+)`")
	loomStyleOpenPattern      = regexp.MustCompile(`(?i)<loom-style\b([^<>]*)>`)
	loomStyleClosePattern     = regexp.MustCompile(`(?i)</loom-style\s*>`)
	loomStyleAttributePattern = regexp.MustCompile(`(?i)(color|background|size)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

// Slack returns Slack mrkdwn while keeping lists readable as ASCII.
func Slack(markdown string) string {
	out := stripCanonicalStyles(markdown)
	out = linkPattern.ReplaceAllString(out, `<$2|$1>`)
	out = underlinePattern.ReplaceAllString(out, `_${1}_`)
	out = italicPattern.ReplaceAllString(out, `${1}_${2}_`)
	out = boldPattern.ReplaceAllString(out, `*${1}*`)
	out = strikePattern.ReplaceAllString(out, `~${1}~`)
	return out
}

// WhatsApp returns the lightweight formatting syntax accepted by WhatsApp.
func WhatsApp(markdown string) string {
	out := stripCanonicalStyles(markdown)
	out = linkPattern.ReplaceAllString(out, `$1 ($2)`)
	out = underlinePattern.ReplaceAllString(out, `_${1}_`)
	out = italicPattern.ReplaceAllString(out, `${1}_${2}_`)
	out = boldPattern.ReplaceAllString(out, `*${1}*`)
	out = strikePattern.ReplaceAllString(out, `~${1}~`)
	return out
}

// GoogleChat returns the formatting syntax accepted in Google Chat messages.
func GoogleChat(markdown string) string {
	// Google Chat's MARKUP_SYNTAX_MARKDOWN accepts Loom's canonical Markdown
	// directly. Drop only Loom extensions that Google Chat cannot represent.
	out := stripCanonicalStyles(markdown)
	out = underlinePattern.ReplaceAllString(out, `$1`)
	return out
}

// PlainText degrades formatting to readable ASCII for providers without rich
// text support. Unsupported underline and strike remain visible as _x_ / ~x~.
func PlainText(markdown string) string {
	out := stripCanonicalStyles(markdown)
	out = linkPattern.ReplaceAllString(out, `$1 ($2)`)
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
	// Protect already-rendered fragments before applying emphasis. Their content
	// must not be interpreted as Markdown a second time.
	prefix := "LOOMHTMLTOKEN"
	for strings.Contains(markdown, prefix) {
		prefix += "X"
	}
	var fragments []string
	protect := func(fragment string) string {
		token := prefix + strconv.Itoa(len(fragments)) + "END"
		fragments = append(fragments, fragment)
		return token
	}
	markdown = fencedCodePattern.ReplaceAllStringFunc(markdown, func(match string) string {
		parts := fencedCodePattern.FindStringSubmatch(match)
		return protect("<pre><code>" + html.EscapeString(parts[1]) + "</code></pre>")
	})
	markdown = inlineCodePattern.ReplaceAllStringFunc(markdown, func(match string) string {
		parts := inlineCodePattern.FindStringSubmatch(match)
		return protect("<code>" + html.EscapeString(parts[1]) + "</code>")
	})
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
		return protect(`<a href="`+html.EscapeString(target)+`">`+html.EscapeString(label)+`</a>`) + suffix
	})
	markdown = loomStyleOpenPattern.ReplaceAllStringFunc(markdown, func(match string) string {
		attributes := loomStyleAttributePattern.FindAllStringSubmatch(match, -1)
		styles := make([]string, 0, len(attributes))
		for _, attribute := range attributes {
			value := strings.TrimSpace(firstNonEmpty(attribute[2], attribute[3]))
			switch strings.ToLower(attribute[1]) {
			case "color":
				if safeRichColor.MatchString(value) {
					styles = append(styles, "color:"+value)
				}
			case "background":
				if safeRichColor.MatchString(value) {
					styles = append(styles, "background-color:"+value)
				}
			case "size":
				if normalized := normalizeRichFontSize(value); normalized != "" {
					styles = append(styles, "font-size:"+normalized)
				}
			}
		}
		if len(styles) == 0 {
			return ""
		}
		return protect(`<span style="` + html.EscapeString(strings.Join(styles, ";")) + `">`)
	})
	markdown = loomStyleClosePattern.ReplaceAllStringFunc(markdown, func(string) string { return protect("</span>") })
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
	for index, fragment := range fragments {
		out = strings.ReplaceAll(out, prefix+strconv.Itoa(index)+"END", fragment)
	}
	return out
}

var safeRichColor = regexp.MustCompile(`(?i)^(?:#[0-9a-f]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,% ]+\)|[a-z]+)$`)
var safeRichFontSize = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)(px|pt|em|rem|%)$`)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stripCanonicalStyles(markdown string) string {
	out := loomStyleOpenPattern.ReplaceAllString(markdown, "")
	return loomStyleClosePattern.ReplaceAllString(out, "")
}

func normalizeRichFontSize(value string) string {
	parts := safeRichFontSize.FindStringSubmatch(value)
	if parts == nil {
		return ""
	}
	number, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return ""
	}
	unit := strings.ToLower(parts[2])
	limits := map[string][2]float64{"px": {8, 48}, "pt": {6, 36}, "em": {0.5, 3}, "rem": {0.5, 3}, "%": {50, 300}}
	limit, ok := limits[unit]
	if !ok {
		return ""
	}
	if number < limit[0] {
		number = limit[0]
	}
	if number > limit[1] {
		number = limit[1]
	}
	return strconv.FormatFloat(number, 'f', -1, 64) + unit
}
