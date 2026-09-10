package matrix

import (
	"encoding/json"
	"mime"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"Loom/pkg/models"
)

type canonicalEventCard struct {
	Title       string `json:"title"`
	Badge       string `json:"badge,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
}

func matrixHTMLToEventCard(source string) (models.Attachment, bool) {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return models.Attachment{}, false
	}
	outer := firstElement(root, "table")
	if outer == nil {
		return models.Attachment{}, false
	}
	outerRow := firstElement(outer, "tr")
	if outerRow == nil {
		return models.Attachment{}, false
	}
	outerCells := directCells(outerRow)
	if len(outerCells) < 2 {
		return models.Attachment{}, false
	}
	imageURL := ""
	if image := firstElement(outerCells[0], "img"); image != nil {
		imageURL = strings.TrimSpace(attribute(image, "src"))
	}
	details := outerCells[1]
	header := firstElement(details, "table")
	if header == nil {
		return models.Attachment{}, false
	}
	headerRow := firstElement(header, "tr")
	cells := directCells(headerRow)
	if len(cells) < 2 {
		return models.Attachment{}, false
	}
	title := strings.TrimSpace(plainNodeText(cells[0]))
	badge := strings.TrimSpace(plainNodeText(cells[1]))
	if title == "" {
		return models.Attachment{}, false
	}
	var description strings.Builder
	for child := details.FirstChild; child != nil; child = child.NextSibling {
		if child != header {
			description.WriteString(renderStandaloneNode(child))
		}
	}
	card := canonicalEventCard{Title: title, Badge: badge, Description: strings.TrimSpace(description.String()), ImageURL: imageURL}
	raw, err := json.Marshal(card)
	if err != nil {
		return models.Attachment{}, false
	}
	return models.Attachment{Type: "event-card", CardJSON: string(raw)}, true
}

func firstElement(node *html.Node, tag string) *html.Node {
	if node == nil {
		return nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, tag) {
			return child
		}
		if found := firstElement(child, tag); found != nil {
			return found
		}
	}
	return nil
}
func directCells(row *html.Node) []*html.Node {
	if row == nil {
		return nil
	}
	var cells []*html.Node
	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && (child.Data == "td" || child.Data == "th") {
			cells = append(cells, child)
		}
	}
	return cells
}
func plainNodeText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			out.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return out.String()
}
func renderStandaloneNode(node *html.Node) string {
	var wrapper strings.Builder
	html.Render(&wrapper, node)
	return matrixHTMLToMarkdown(wrapper.String(), true)
}

var htmlLikeBody = regexp.MustCompile(`(?is)<\s*/?\s*(?:p|br|div|h[1-6]|strong|em|b|i|ul|ol|li|a|img)\b`)

func canonicalMessageBody(plain, format, formatted string, hasAttachment bool) string {
	if format == "org.matrix.custom.html" && formatted != "" {
		if text := matrixHTMLToMarkdown(formatted, hasAttachment); text != "" {
			return text
		}
	}
	// Legacy compatibility: some integrations placed an HTML description in
	// body without declaring formatted_body. Detection is based on markup shape.
	if htmlLikeBody.MatchString(plain) {
		if text := matrixHTMLToMarkdown(plain, hasAttachment); text != "" {
			return text
		}
	}
	return strings.TrimSpace(plain)
}

func matrixHTMLToMarkdown(source string, dropImages bool) string {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	var render func(*html.Node) string
	render = func(node *html.Node) string {
		if node.Type == html.TextNode {
			return strings.ReplaceAll(node.Data, "\u00a0", " ")
		}
		if node.Type != html.ElementNode && node.Type != html.DocumentNode {
			return ""
		}
		tag := strings.ToLower(node.Data)
		if tag == "script" || tag == "style" || tag == "iframe" || tag == "object" {
			return ""
		}
		if tag == "table" {
			return renderHTMLTable(node, render)
		}
		var children strings.Builder
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			children.WriteString(render(child))
		}
		content := children.String()
		switch tag {
		case "strong", "b":
			return "**" + strings.TrimSpace(content) + "**"
		case "em", "i":
			return "*" + strings.TrimSpace(content) + "*"
		case "code":
			return "`" + strings.ReplaceAll(content, "`", "\\`") + "`"
		case "pre":
			return "\n```\n" + strings.TrimSpace(content) + "\n```\n"
		case "br":
			return "\n"
		case "hr":
			return "\n\n---\n\n"
		case "p", "div":
			return "\n\n" + strings.TrimSpace(content) + "\n\n"
		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(tag[1] - '0')
			return "\n\n" + strings.Repeat("#", level) + " " + strings.TrimSpace(content) + "\n\n"
		case "li":
			return "\n- " + strings.TrimSpace(content)
		case "a":
			href := safeHTMLLink(attribute(node, "href"))
			if href == "" {
				return content
			}
			return "[" + strings.TrimSpace(content) + "](" + href + ")"
		case "img":
			if dropImages {
				return ""
			}
			return strings.TrimSpace(attribute(node, "alt"))
		default:
			return content
		}
	}
	result := strings.TrimSpace(render(root))
	result = regexp.MustCompile(`\n[ \t]+`).ReplaceAllString(result, "\n")
	result = regexp.MustCompile(`\n{3,}`).ReplaceAllString(result, "\n\n")
	return result
}

func renderHTMLTable(table *html.Node, render func(*html.Node) string) string {
	var rowNodes []*html.Node
	var collectRows func(*html.Node)
	collectRows = func(node *html.Node) {
		// Rows belonging to a nested table must be handled by that table's own
		// renderer. Mixing them with the parent produces invalid GFM delimiters.
		if node != table && node.Type == html.ElementNode && strings.EqualFold(node.Data, "table") {
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "tr") {
			rowNodes = append(rowNodes, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectRows(child)
		}
	}
	collectRows(table)
	if tableNeedsFlattening(rowNodes) {
		var blocks []string
		for _, rowNode := range rowNodes {
			for cell := rowNode.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type != html.ElementNode || (cell.Data != "th" && cell.Data != "td") {
					continue
				}
				if value := strings.TrimSpace(render(cell)); value != "" {
					blocks = append(blocks, value)
				}
			}
		}
		return "\n\n" + strings.Join(blocks, "\n\n") + "\n\n"
	}
	rows := make([][]string, 0, len(rowNodes))
	maxColumns := 0
	for _, rowNode := range rowNodes {
		var cells []string
		for cell := rowNode.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.Type != html.ElementNode || (cell.Data != "th" && cell.Data != "td") {
				continue
			}
			value := strings.TrimSpace(render(cell))
			value = strings.ReplaceAll(value, "|", "\\|")
			value = regexp.MustCompile(`\s*\n\s*`).ReplaceAllString(value, "<br>")
			cells = append(cells, value)
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
			if len(cells) > maxColumns {
				maxColumns = len(cells)
			}
		}
	}
	if len(rows) == 0 || maxColumns == 0 {
		return ""
	}
	for index := range rows {
		for len(rows[index]) < maxColumns {
			rows[index] = append(rows[index], "")
		}
	}
	var out strings.Builder
	out.WriteString("\n\n| " + strings.Join(rows[0], " | ") + " |\n")
	out.WriteString("| " + strings.TrimSuffix(strings.Repeat("--- | ", maxColumns), " ") + "\n")
	for _, row := range rows[1:] {
		out.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	out.WriteByte('\n')
	return out.String()
}

func tableNeedsFlattening(rows []*html.Node) bool {
	for _, row := range rows {
		for cell := row.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.Type != html.ElementNode || (cell.Data != "th" && cell.Data != "td") {
				continue
			}
			if span := attribute(cell, "rowspan"); span != "" && span != "1" {
				return true
			}
			if span := attribute(cell, "colspan"); span != "" && span != "1" {
				return true
			}
			if hasDescendantElement(cell, "table") {
				return true
			}
		}
	}
	return false
}

func hasDescendantElement(node *html.Node, tag string) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, tag) {
			return true
		}
		if hasDescendantElement(child, tag) {
			return true
		}
	}
	return false
}

func attribute(node *html.Node, key string) string {
	for _, item := range node.Attr {
		if strings.EqualFold(item.Key, key) {
			return item.Val
		}
	}
	return ""
}
func safeHTMLLink(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func canonicalAttachmentName(filename, body, mimeType string) string {
	name := strings.TrimSpace(filename)
	if name == "" && isShortPlainFilename(body) && filepath.Ext(strings.TrimSpace(body)) != "" {
		name = strings.TrimSpace(body)
	}
	if !isShortPlainFilename(name) {
		ext := ".bin"
		if extensions, _ := mime.ExtensionsByType(mimeType); len(extensions) > 0 {
			ext = extensions[0]
		}
		if strings.HasPrefix(mimeType, "image/") && ext == ".bin" {
			ext = ".img"
		}
		name = "attachment" + ext
	}
	return filepath.Base(name)
}

func isShortPlainFilename(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 160 && !strings.ContainsAny(value, "\r\n<>") && filepath.Base(value) == value
}
