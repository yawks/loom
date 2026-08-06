package teams

import (
	"fmt"
	stdhtml "html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"go.mau.fi/mautrix-teams/pkg/msteams"
	"golang.org/x/net/html"
)

// teamsHTMLToMarkdown preserves the formatting understood by Loom's Markdown
// renderer while discarding Teams-specific HTML wrappers and attributes.
func teamsHTMLToMarkdown(input string) string {
	input = msteams.ReplaceInlineEmojis(input)
	input = normalizeTeamsMarkdown(input)
	root, err := html.Parse(strings.NewReader("<body>" + input + "</body>"))
	if err != nil {
		return input
	}
	out := &strings.Builder{}
	var render func(*html.Node)
	render = func(node *html.Node) {
		if node.Type == html.TextNode {
			out.WriteString(node.Data)
			return
		}
		tag := strings.ToLower(node.Data)
		if tag == "table" {
			out.WriteString(tableToMarkdown(node))
			out.WriteByte('\n')
			return
		}
		style := teamsNodeStyle(node)
		if style != "" {
			out.WriteString(style)
		}
		// Teams commonly includes the spaces surrounding formatted text inside
		// the <strong>/<em> element. CommonMark requires emphasis delimiters to
		// touch non-whitespace, so keep those spaces but move them outside the
		// Markdown markers.
		if tag == "strong" || tag == "b" || tag == "em" || tag == "i" {
			previousOut := out
			formatted := &strings.Builder{}
			out = formatted
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				render(child)
			}
			out = previousOut
			body := formatted.String()
			trimmed := strings.Trim(body, " \t")
			if trimmed == "" {
				out.WriteString(body)
				if style != "" {
					out.WriteString("</loom-style>")
				}
				return
			}
			leadingLength := len(body) - len(strings.TrimLeft(body, " \t"))
			trailingLength := len(body) - len(strings.TrimRight(body, " \t"))
			out.WriteString(body[:leadingLength])
			delimiter := "*"
			if tag == "strong" || tag == "b" {
				delimiter = "**"
			}
			out.WriteString(delimiter)
			out.WriteString(trimmed)
			out.WriteString(delimiter)
			if trailingLength > 0 {
				out.WriteString(body[len(body)-trailingLength:])
			}
			if style != "" {
				out.WriteString("</loom-style>")
			}
			return
		}
		switch tag {
		case "br":
			out.WriteByte('\n')
			return
		case "u":
			out.WriteString("<u>")
		case "code":
			out.WriteByte('`')
		case "li":
			out.WriteString("- ")
		case "a":
			if htmlAttribute(node, "href") != "" {
				out.WriteByte('[')
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			render(child)
		}
		switch tag {
		case "u":
			out.WriteString("</u>")
		case "code":
			out.WriteByte('`')
		case "p", "div", "li":
			out.WriteByte('\n')
		case "a":
			if href := htmlAttribute(node, "href"); href != "" {
				out.WriteString("](")
				out.WriteString(href)
				out.WriteByte(')')
			}
		}
		if style != "" {
			out.WriteString("</loom-style>")
		}
	}
	render(root)
	return strings.TrimSpace(strings.ReplaceAll(out.String(), "\u00a0", " "))
}

var teamsStrongTrailingSpace = regexp.MustCompile(`\*\*([^*\n]*\S)([ \t]+)\*\*`)
var teamsEmptyStrong = regexp.MustCompile(`\*\*[ \t]+\*\*`)

// normalizeTeamsMarkdown repairs Markdown-like text embedded literally in a
// Teams HTML message. This happens when Teams mixes rich-text elements with
// already-serialized Markdown in the same payload.
func normalizeTeamsMarkdown(input string) string {
	input = teamsStrongTrailingSpace.ReplaceAllString(input, `**$1**$2`)
	return teamsEmptyStrong.ReplaceAllString(input, "")
}

var safeCSSValue = regexp.MustCompile(`(?i)^(?:#[0-9a-f]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,% ]+\)|[a-z]+)$`)
var safeFontSize = regexp.MustCompile(`(?i)^[0-9]+(?:\.[0-9]+)?(?:px|pt|em|rem|%)$`)

// teamsNodeStyle serializes Teams presentation attributes into Loom's generic
// rich-text extension. Only inert, validated CSS values cross the provider
// boundary; arbitrary HTML and CSS are never passed to the frontend.
func teamsNodeStyle(node *html.Node) string {
	if node.Type != html.ElementNode {
		return ""
	}
	styles := parseInlineStyle(rawHTMLAttribute(node, "style"))
	color := firstNonEmptyStyle(styles["color"], rawHTMLAttribute(node, "color"))
	background := firstNonEmptyStyle(styles["background-color"], styles["background"], rawHTMLAttribute(node, "bgcolor"))
	size := styles["font-size"]
	if size == "" && strings.EqualFold(node.Data, "font") {
		size = htmlFontSize(rawHTMLAttribute(node, "size"))
	}
	underline := strings.Contains(strings.ToLower(styles["text-decoration"]), "underline") ||
		strings.Contains(strings.ToLower(styles["text-decoration-line"]), "underline")
	attributes := make([]string, 0, 4)
	if safeCSSValue.MatchString(strings.TrimSpace(color)) {
		attributes = append(attributes, `color="`+stdhtml.EscapeString(strings.TrimSpace(color))+`"`)
	}
	if safeCSSValue.MatchString(strings.TrimSpace(background)) {
		attributes = append(attributes, `background="`+stdhtml.EscapeString(strings.TrimSpace(background))+`"`)
	}
	if safeFontSize.MatchString(strings.TrimSpace(size)) {
		attributes = append(attributes, `size="`+stdhtml.EscapeString(strings.TrimSpace(size))+`"`)
	}
	if underline {
		attributes = append(attributes, `underline="true"`)
	}
	if len(attributes) == 0 {
		return ""
	}
	return "<loom-style " + strings.Join(attributes, " ") + ">"
}

func parseInlineStyle(value string) map[string]string {
	out := make(map[string]string)
	for _, declaration := range strings.Split(value, ";") {
		parts := strings.SplitN(declaration, ":", 2)
		if len(parts) == 2 {
			out[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}
	return out
}

func rawHTMLAttribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func firstNonEmptyStyle(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func htmlFontSize(value string) string {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > 7 {
		return ""
	}
	return fmt.Sprintf("%dpx", []int{10, 13, 16, 18, 24, 32, 48}[n-1])
}

func tableToMarkdown(table *html.Node) string {
	rows := make([][]string, 0)
	var walkRows func(*html.Node)
	walkRows = func(node *html.Node) {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "tr") {
			cells := make([]string, 0)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode &&
					(strings.EqualFold(child.Data, "th") || strings.EqualFold(child.Data, "td")) {
					value := strings.ReplaceAll(strings.TrimSpace(tableCellText(child)), "|", `\|`)
					cells = append(cells, value)
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walkRows(child)
		}
	}
	walkRows(table)
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, row := range rows {
		width = max(width, len(row))
	}
	var out strings.Builder
	writeRow := func(row []string) {
		out.WriteString("| ")
		for index := 0; index < width; index++ {
			if index < len(row) {
				out.WriteString(row[index])
			}
			out.WriteString(" | ")
		}
		out.WriteByte('\n')
	}
	writeRow(rows[0])
	separator := make([]string, width)
	for index := range separator {
		separator[index] = "---"
	}
	writeRow(separator)
	for _, row := range rows[1:] {
		writeRow(row)
	}
	return strings.TrimSpace(out.String())
}

func tableCellText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			out.WriteString(current.Data)
			return
		}
		if current.Type == html.ElementNode && strings.EqualFold(current.Data, "br") {
			out.WriteString("<br>")
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.TrimSpace(out.String())
}

func htmlAttribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			if parsed, err := url.Parse(attr.Val); err == nil &&
				(parsed.Scheme == "http" || parsed.Scheme == "https") {
				return attr.Val
			}
		}
	}
	return ""
}
