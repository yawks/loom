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
		if tag == "p" && strings.EqualFold(htmlAttribute(node, "itemtype"), "http://schema.skype.com/CodeBlockEditor") {
			return
		}
		if tag == "table" {
			out.WriteString(tableToMarkdown(node))
			out.WriteByte('\n')
			return
		}
		if tag == "pre" {
			// A Teams CodeBlockEditor stores syntax colouring in nested spans.
			// Those spans are presentation metadata, not separate code fragments:
			// flatten them into one fenced Markdown block so Loom exposes one copy
			// action for the complete snippet.
			code := teamsPreformattedText(node)
			fence := markdownCodeFence(code)
			out.WriteByte('\n')
			out.WriteString(fence)
			out.WriteByte('\n')
			out.WriteString(code)
			if !strings.HasSuffix(code, "\n") {
				out.WriteByte('\n')
			}
			out.WriteString(fence)
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
	normalized := strings.TrimSpace(strings.ReplaceAll(out.String(), "\u00a0", " "))
	normalized = normalizeTeamsEscapedTable(normalized)
	normalized = normalizeDuplicatedTeamsLinks(normalized)
	if repaired, ok := normalizeLegacyFragmentedCode(normalized); ok {
		return repaired
	}
	return normalized
}

func teamsPreformattedText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			out.WriteString(current.Data)
			return
		}
		if current.Type == html.ElementNode && strings.EqualFold(current.Data, "br") {
			out.WriteByte('\n')
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.ReplaceAll(out.String(), "\r\n", "\n")
}

func markdownCodeFence(code string) string {
	longest := 0
	for _, run := range regexp.MustCompile("`+").FindAllString(code, -1) {
		if len(run) > longest {
			longest = len(run)
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

var loomStyleTag = regexp.MustCompile(`(?i)</?loom-style\b[^>]*>`)
var blankCodeLine = regexp.MustCompile(`\n[ \t]*\n`)

func legacyFragmentedCode(body string) bool {
	if !strings.Contains(strings.ToLower(body), "<loom-style") {
		return false
	}
	indentedFragments := 0
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			indentedFragments++
		}
	}
	return indentedFragments >= 3
}

// normalizeLegacyFragmentedCode is a compatibility path for canonical rows
// written before Teams CodeBlockEditor boundaries were retained. Such rows
// contain syntax-colour tags plus a sequence of indented fragments. Recover the
// boundary generically, discard the obsolete colours, and emit one code fence.
func normalizeLegacyFragmentedCode(body string) (string, bool) {
	if !legacyFragmentedCode(body) {
		return body, false
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	firstIndented := -1
	for index, line := range lines {
		if strings.TrimSpace(line) != "" && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			firstIndented = index
			break
		}
	}
	if firstIndented < 0 {
		return body, false
	}
	codeStart := firstIndented
	for index := firstIndented - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			codeStart = index
			break
		}
	}
	if codeStart == firstIndented {
		return body, false
	}
	intro := strings.TrimSpace(strings.Join(lines[:codeStart], "\n"))
	code := loomStyleTag.ReplaceAllString(strings.Join(lines[codeStart:], "\n"), "")
	// Teams doubled each visual line break in this legacy representation. One
	// pass removes the artificial gap while preserving intentional blank lines,
	// which arrived as two consecutive gaps.
	code = strings.TrimSpace(blankCodeLine.ReplaceAllString(code, "\n"))
	fence := markdownCodeFence(code)
	if intro == "" {
		return fence + "\n" + code + "\n" + fence, true
	}
	return intro + "\n\n" + fence + "\n" + code + "\n" + fence, true
}

var teamsStrongLeadingSpace = regexp.MustCompile(`\*\*([ \t\x{00a0}]+)(\S[^*\n]*)\*\*`)
var teamsStrongTrailingSpace = regexp.MustCompile(`\*\*([^*\n]*\S)([ \t\x{00a0}]+)\*\*`)
var teamsEmphasisLeadingSpace = regexp.MustCompile(`(^|[^*])\*([ \t\x{00a0}]+)(\S[^*\n]*)\*`)
var teamsEmphasisTrailingSpace = regexp.MustCompile(`(^|[^*])\*([^*\n]*\S)([ \t\x{00a0}]+)\*`)
var teamsStrikeLeadingSpace = regexp.MustCompile(`~~([ \t\x{00a0}]+)(\S[^~\n]*)~~`)
var teamsStrikeTrailingSpace = regexp.MustCompile(`~~([^~\n]*\S)([ \t\x{00a0}]+)~~`)
var teamsEmptyStrong = regexp.MustCompile(`\*\*[ \t\x{00a0}]+\*\*`)
var teamsEscapedStrong = regexp.MustCompile(`\\\*\\\*([^\n]*\S)[ \t]*\\\*\\\*`)
var duplicatedTeamsLink = regexp.MustCompile(`\[\[([^\]\n]+)\]\((https?://[^\s)]+)\)\]\\\(\[([^\]\n]+)\]\((https?://[^\s)]+)\)\)`)

// normalizeTeamsMarkdown repairs Markdown-like text embedded literally in a
// Teams HTML message. This happens when Teams mixes rich-text elements with
// already-serialized Markdown in the same payload.
func normalizeTeamsMarkdown(input string) string {
	input = teamsEscapedStrong.ReplaceAllString(input, `**$1**`)
	input = teamsStrongLeadingSpace.ReplaceAllString(input, `$1**$2**`)
	input = teamsStrongTrailingSpace.ReplaceAllString(input, `**$1**$2`)
	input = teamsEmptyStrong.ReplaceAllString(input, "")
	input = teamsEmphasisLeadingSpace.ReplaceAllString(input, `$1$2*$3*`)
	input = teamsEmphasisTrailingSpace.ReplaceAllString(input, `$1*$2*$3`)
	input = teamsStrikeLeadingSpace.ReplaceAllString(input, `$1~~$2~~`)
	input = teamsStrikeTrailingSpace.ReplaceAllString(input, `~~$1~~$2`)
	return normalizeTeamsEscapedTable(input)
}

var teamsMarkdownTableSeparator = regexp.MustCompile(`(?m)^\s*\\?\|\s*:?-+\s*(?:\|\s*:?-+\s*)*\|?\s*\\?\s*$`)
var teamsNumberedFirstTableRow = regexp.MustCompile(`^\|\s*1[.)]\s+`)
var teamsNumberedTableRow = regexp.MustCompile(`^\|?\s*\d+[.)]\s+`)

// normalizeTeamsEscapedTable repairs tables pasted into the Teams composer as
// Markdown. Teams may escape every pipe and append a backslash to each visual
// line, leaving Loom's provider-neutral Markdown renderer with literal `|`
// characters instead of a GFM table. Only table-shaped messages containing a
// separator row are touched, so ordinary escaped pipes keep their meaning.
func normalizeTeamsEscapedTable(input string) string {
	if !teamsMarkdownTableSeparator.MatchString(input) {
		return input
	}
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	separatorIndex := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if teamsMarkdownTableSeparator.MatchString(line) {
			separatorIndex = index
		}
		if !strings.HasPrefix(trimmed, `\|`) && !strings.HasPrefix(trimmed, "|") {
			continue
		}
		line = strings.ReplaceAll(line, `\|`, "|")
		line = strings.TrimSuffix(strings.TrimRight(line, " \t"), `\`)
		trimmed = strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "|") && !strings.HasSuffix(trimmed, "|") {
			line = strings.TrimRight(line, " \t") + " |"
		}
		lines[index] = line
	}
	// Some one-column tables arrive with an empty `|` paragraph between the
	// first numbered row and its description, while Teams leaves the separator
	// after that description. Move it behind row 1 so GFM recognizes row 1 too.
	firstRowIndex := -1
	for index := separatorIndex - 1; index >= 0; index-- {
		trimmed := strings.TrimSpace(lines[index])
		if teamsNumberedFirstTableRow.MatchString(trimmed) {
			firstRowIndex = index
			break
		}
		// Do not reach through unrelated prose or another numbered item. Teams
		// only inserts a few blank/orphan/description lines in this gap.
		if separatorIndex-index > 8 || teamsNumberedTableRow.MatchString(trimmed) {
			break
		}
	}
	if firstRowIndex >= 0 && firstRowIndex < separatorIndex-1 {
		rebuilt := append([]string{}, lines[:firstRowIndex+1]...)
		rebuilt = append(rebuilt, lines[separatorIndex])
		for _, line := range lines[firstRowIndex+1 : separatorIndex] {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && trimmed != "|" {
				if !strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "|") {
					line = "| " + trimmed
				}
				if strings.HasPrefix(strings.TrimSpace(line), "|") && !strings.HasSuffix(strings.TrimSpace(line), "|") {
					line = strings.TrimRight(line, " \t") + " |"
				}
				rebuilt = append(rebuilt, line)
			}
		}
		rebuilt = append(rebuilt, lines[separatorIndex+1:]...)
		lines = rebuilt
		separatorIndex = firstRowIndex + 1
	}
	// A paragraph split inside a pasted cell must not terminate the GFM table.
	// Fold text found between two post-separator rows into the preceding cell.
	for index := separatorIndex + 1; index < len(lines); index++ {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "|") {
			continue
		}
		nextRow := index + 1
		for nextRow < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[nextRow]), "|") {
			nextRow++
		}
		if nextRow >= len(lines) || index == 0 {
			break
		}
		continuation := make([]string, 0, nextRow-index)
		for _, value := range lines[index:nextRow] {
			if value = strings.TrimSpace(value); value != "" {
				continuation = append(continuation, value)
			}
		}
		if len(continuation) > 0 {
			previous := strings.TrimSuffix(strings.TrimRight(lines[index-1], " \t"), "|")
			lines[index-1] = strings.TrimRight(previous, " \t") + "<br>" + strings.Join(continuation, "<br>") + " |"
		}
		lines = append(lines[:index], lines[nextRow:]...)
		index--
	}
	lines = canonicalizeNumberedTeamsTable(lines)
	return strings.Join(lines, "\n")
}

// canonicalizeNumberedTeamsTable turns the irregular rows produced by a
// Teams/Office table paste into a stable two-column GFM table. Continuation
// rows supply a KPI description and/or its target value.
func canonicalizeNumberedTeamsTable(lines []string) []string {
	first := -1
	for index, line := range lines {
		if teamsNumberedFirstTableRow.MatchString(strings.TrimSpace(line)) {
			first = index
			break
		}
	}
	if first < 0 {
		return lines
	}
	type tableRow struct{ label, target string }
	rows := make([]tableRow, 0)
	end := first
	for index := first; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "" || trimmed == "|" || teamsMarkdownTableSeparator.MatchString(trimmed) {
			end = index + 1
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			if len(rows) == 0 || !strings.Contains(trimmed, "|") {
				break
			}
			trimmed = "| " + trimmed
		}
		cells := markdownTableCells(trimmed)
		if len(cells) == 0 {
			break
		}
		if teamsNumberedTableRow.MatchString("| " + cells[0]) {
			row := tableRow{label: cells[0]}
			if len(cells) > 1 {
				row.target = cells[1]
			}
			rows = append(rows, row)
		} else if len(rows) > 0 {
			current := &rows[len(rows)-1]
			if cells[0] != "" {
				current.label = strings.TrimSpace(current.label + "<br>" + cells[0])
			}
			for _, cell := range cells[1:] {
				if cell != "" && current.target == "" {
					current.target = cell
					break
				}
			}
		}
		end = index + 1
	}
	if len(rows) < 2 {
		return lines
	}
	out := append([]string{}, lines[:first]...)
	for index, row := range rows {
		out = append(out, "| "+row.label+" | "+row.target+" |")
		if index == 0 {
			out = append(out, "| --- | --- |")
		}
	}
	return append(out, lines[end:]...)
}

func markdownTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// normalizeDuplicatedTeamsLinks repairs the representation produced by Teams
// when a pasted Markdown link is wrapped in a rich-text link and repeated as
// escaped text. The equality check makes the rewrite safe for normal bracketed
// prose that happens to contain links.
func normalizeDuplicatedTeamsLinks(input string) string {
	return duplicatedTeamsLink.ReplaceAllStringFunc(input, func(match string) string {
		parts := duplicatedTeamsLink.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		normalize := func(value string) string {
			value = strings.ReplaceAll(value, `\_`, "_")
			value = strings.ReplaceAll(value, `\&`, "&")
			return stdhtml.UnescapeString(value)
		}
		want := normalize(parts[1])
		for _, part := range parts[2:] {
			if normalize(part) != want {
				return match
			}
		}
		return "[" + parts[3] + "](" + parts[4] + ")"
	})
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
