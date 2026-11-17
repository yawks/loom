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

var teamsCardPreviewPattern = regexp.MustCompile(`(?i)[<\[]URIObject\s+[^>\]]*\btype\s*=\s*["']SWIFT\.1["']`)

// teamsReplyPreview also accepts legacy previews whose URIObject wrapper was
// escaped as Markdown or HTML before being persisted.
func teamsReplyPreview(body string) string {
	decoded := stdhtml.UnescapeString(body)
	if teamsCardPreviewPattern.MatchString(decoded) {
		if card := teamsSwiftCardsToMarkdown(decoded); card != "" {
			return card
		}
		return "Carte"
	}
	if looksLikeTeamsHTML(body) {
		body = teamsHTMLToMarkdown(msteams.StripAMSAttachments(msteams.StripReplyBlockquote(body)))
	}
	if isTeamsUnsupportedCardPlaceholder(body) {
		return "Carte"
	}
	return normalizeTeamsEscapedTable(body)
}

// teamsHTMLToMarkdown preserves the formatting understood by Loom's Markdown
// renderer while discarding Teams-specific HTML wrappers and attributes.
func teamsHTMLToMarkdown(input string) string {
	input = msteams.ReplaceInlineEmojis(input)
	input = normalizeTeamsMarkdown(input)
	canonicalTable := teamsMarkdownTableSeparator.MatchString(input)
	htmlSource := looksLikeTeamsHTML(input) && !canonicalTable
	root, err := html.Parse(strings.NewReader("<body>" + input + "</body>"))
	if err != nil {
		return input
	}
	out := &strings.Builder{}
	blockBoundary := func() {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
	}
	var render func(*html.Node)
	render = func(node *html.Node) {
		if node.Type == html.TextNode {
			// HTML source indentation between blocks is not a visible line break.
			if strings.TrimSpace(node.Data) == "" && strings.ContainsAny(node.Data, "\r\n") &&
				(teamsHTMLBlockElement(node.PrevSibling) || teamsHTMLBlockElement(node.NextSibling)) {
				return
			}
			text := node.Data
			if htmlSource && strings.ContainsAny(text, "\r\n") {
				// Source newlines collapse to spaces in HTML; only <br> and block
				// boundaries introduce visible line breaks. Do not apply this to
				// already-canonical Markdown or preformatted code.
				text = teamsHTMLSourceWhitespace.ReplaceAllString(text, " ")
				if teamsHTMLLineBoundary(node.PrevSibling) {
					text = strings.TrimLeft(text, " ")
				}
				if teamsHTMLBlockElement(node.NextSibling) {
					text = strings.TrimRight(text, " ")
				}
			}
			out.WriteString(text)
			return
		}
		tag := strings.ToLower(node.Data)
		if tag == "p" && strings.EqualFold(htmlAttribute(node, "itemtype"), "http://schema.skype.com/CodeBlockEditor") {
			return
		}
		if tag == "table" {
			blockBoundary()
			// A table cannot interrupt a list item's lazy continuation.
			// Separate it from preceding prose/list content before its header.
			if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
				out.WriteByte('\n')
			}
			table := tableToMarkdown(node, func(cell *html.Node) string {
				previousOut := out
				out = &strings.Builder{}
				for child := cell.FirstChild; child != nil; child = child.NextSibling {
					render(child)
				}
				value := strings.TrimSpace(out.String())
				out = previousOut
				return strings.ReplaceAll(value, "\n", "<br>")
			})
			out.WriteString(table)
			// GFM otherwise treats the following prose (e.g. "Lecture") as
			// another table row, even when it contains no pipe at all.
			out.WriteString("\n\n")
			return
		}
		if tag == "hr" {
			blockBoundary()
			// Blank lines prevent a rule from becoming a setext heading or
			// being swallowed by the preceding list.
			if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
				out.WriteByte('\n')
			}
			out.WriteString("---\n\n")
			return
		}
		heading := len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
		if heading {
			blockBoundary()
			out.WriteString(strings.Repeat("#", int(tag[1]-'0')) + " ")
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
		// rich-text elements. CommonMark requires emphasis delimiters to touch
		// non-whitespace; moving the spaces also keeps Loom's underline extension
		// and strikethrough scoped to the intended text.
		if tag == "strong" || tag == "b" || tag == "em" || tag == "i" ||
			tag == "u" || tag == "s" || tag == "strike" || tag == "del" {
			previousOut := out
			formatted := &strings.Builder{}
			out = formatted
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				render(child)
			}
			out = previousOut
			body := formatted.String()
			trimmed := strings.Trim(body, " \t\u00a0")
			if trimmed == "" {
				out.WriteString(body)
				if style != "" {
					out.WriteString("</loom-style>")
				}
				return
			}
			leadingLength := len(body) - len(strings.TrimLeft(body, " \t\u00a0"))
			trailingLength := len(body) - len(strings.TrimRight(body, " \t\u00a0"))
			out.WriteString(body[:leadingLength])
			openDelimiter := "*"
			closeDelimiter := "*"
			if tag == "strong" || tag == "b" {
				openDelimiter = "**"
				closeDelimiter = "**"
			} else if tag == "u" {
				openDelimiter = "<u>"
				closeDelimiter = "</u>"
			} else if tag == "s" || tag == "strike" || tag == "del" {
				openDelimiter = "~~"
				closeDelimiter = "~~"
			}
			out.WriteString(openDelimiter)
			out.WriteString(trimmed)
			out.WriteString(closeDelimiter)
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
			if canonicalTable {
				out.WriteString("<br>")
			} else {
				out.WriteByte('\n')
			}
			return
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
		case "code":
			out.WriteByte('`')
		case "p", "div", "li":
			// A trailing <br> already supplies the boundary. Likewise, a
			// wrapper around blocks must not add another visible blank line.
			last := node.LastChild
			for last != nil && last.Type == html.TextNode && strings.TrimSpace(last.Data) == "" {
				last = last.PrevSibling
			}
			if !teamsHTMLLineBoundary(last) {
				out.WriteByte('\n')
			}
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
		if heading {
			out.WriteByte('\n')
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

var teamsHTMLSourceWhitespace = regexp.MustCompile(`[ \t\r\n]+`)

var legacyTeamsNewlineRun = regexp.MustCompile(`\n(?:[ \t]*\n)+`)

// This is only a candidate detector for re-reading the authoritative HTML.
// Never collapse canonical Markdown based on this heuristic alone.
func looksLikeLegacyDoubledLineBreaks(body string) bool {
	if looksLikeTeamsHTML(body) || strings.Contains(body, "```") || strings.Contains(body, "~~~") {
		return false
	}
	runs := legacyTeamsNewlineRun.FindAllString(body, -1)
	if len(runs) < 3 {
		return false
	}
	for _, run := range runs {
		if strings.Count(run, "\n") >= 4 {
			return true
		}
	}
	return false
}

func teamsHTMLLineBoundary(node *html.Node) bool {
	return teamsHTMLBlockElement(node) || (node != nil && node.Type == html.ElementNode && node.Data == "br")
}

func teamsHTMLBlockElement(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	switch node.Data {
	case "p", "div", "li", "ul", "ol", "table", "pre", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
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
	// Legacy conversions omitted the boundary before an HTML table, gluing
	// its pipe-prefixed header to the preceding prose. Only split when the
	// following separator confirms a complete header of the expected width.
	for index := 1; index < len(lines); index++ {
		if !teamsMarkdownTableSeparator.MatchString(lines[index]) {
			continue
		}
		header := lines[index-1]
		pipe := strings.IndexByte(header, '|')
		if pipe <= 0 || strings.TrimSpace(header[:pipe]) == "" || strings.HasPrefix(strings.TrimSpace(header), `\|`) ||
			!strings.HasSuffix(strings.TrimSpace(header), "|") ||
			len(markdownTableCells(header[pipe:])) != len(markdownTableCells(lines[index])) {
			continue
		}
		prefix := strings.TrimRight(header[:pipe], " \t")
		lines[index-1] = header[pipe:]
		lines = append(lines[:index-1], append([]string{prefix}, lines[index-1:]...)...)
		index++
	}
	separatorIndex := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if teamsMarkdownTableSeparator.MatchString(line) {
			separatorIndex = index
		}
		if !strings.HasPrefix(trimmed, `\|`) && !strings.HasPrefix(trimmed, "|") {
			continue
		}
		// A leading escaped pipe identifies a pasted/escaped table row.
		// Native canonical rows may contain escaped literal pipes inside
		// cells: unescaping those would silently introduce extra columns.
		if strings.HasPrefix(trimmed, `\|`) {
			line = strings.ReplaceAll(line, `\|`, "|")
		}
		line = strings.TrimSuffix(strings.TrimRight(line, " \t"), `\`)
		trimmed = strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "|") && !strings.HasSuffix(trimmed, "|") {
			line = strings.TrimRight(line, " \t") + " |"
		}
		lines[index] = line
	}
	// Legacy Office pastes can split the last header cell into a separate
	// paragraph. Only join fragments when they reconstruct the separator width.
	if separatorIndex >= 2 {
		width := len(markdownTableCells(lines[separatorIndex]))
		for start := separatorIndex - 2; start >= 0 && separatorIndex-start <= 8; start-- {
			header := strings.TrimSpace(lines[start])
			if header == "" {
				break
			}
			if !strings.HasPrefix(header, "|") || header == "|" {
				continue
			}
			if teamsNumberedFirstTableRow.MatchString(header) {
				break
			}
			if len(markdownTableCells(header)) >= width {
				break
			}
			fragments := []string{}
			for _, fragment := range lines[start+1 : separatorIndex] {
				fragment = strings.TrimSpace(fragment)
				if fragment != "" && fragment != "|" {
					fragments = append(fragments, fragment)
				}
			}
			joined := strings.TrimSpace(strings.TrimSuffix(header, "|")) + "<br>" + strings.Join(fragments, "<br>")
			if len(fragments) > 0 && len(markdownTableCells(joined)) == width {
				lines[start] = joined
				lines = append(lines[:start+1], lines[separatorIndex:]...)
				separatorIndex = start + 1
			}
			break
		}
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

func tableToMarkdown(table *html.Node, renderCell func(*html.Node) string) string {
	rows := make([][]string, 0)
	var walkRows func(*html.Node)
	walkRows = func(node *html.Node) {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "tr") {
			cells := make([]string, 0)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode &&
					(strings.EqualFold(child.Data, "th") || strings.EqualFold(child.Data, "td")) {
					value := strings.ReplaceAll(strings.TrimSpace(renderCell(child)), "|", `\|`)
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
