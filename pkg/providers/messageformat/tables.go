package messageformat

import (
	"regexp"
	"strings"
)

var tableDelimiterCell = regexp.MustCompile(`^:?-+:?$`)

// Code is already protected by TeamsHTML before reaching this helper. Parse
// only explicit pipe tables, and pass cell content through the same escaped,
// allowlisted inline formatter as ordinary messages.
func protectTeamsTables(markdown string, protect func(string) string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var result []string
	for index := 0; index < len(lines); index++ {
		if index+1 >= len(lines) {
			result = append(result, lines[index])
			continue
		}
		header, ok := tableCells(lines[index])
		separators, valid := tableCells(lines[index+1])
		if !ok || !valid || len(header) != len(separators) {
			result = append(result, lines[index])
			continue
		}
		for _, cell := range separators {
			if !tableDelimiterCell.MatchString(cell) {
				valid = false
			}
		}
		if !valid {
			result = append(result, lines[index])
			continue
		}
		var table strings.Builder
		writeRow := func(cells []string, tag string) {
			table.WriteString("<tr>")
			for _, cell := range cells {
				table.WriteString("<" + tag + ">")
				table.WriteString(TeamsHTML(cell))
				table.WriteString("</" + tag + ">")
			}
			table.WriteString("</tr>")
		}
		table.WriteString("<table><thead>")
		writeRow(header, "th")
		table.WriteString("</thead><tbody>")
		index++
		for index+1 < len(lines) {
			cells, ok := tableCells(lines[index+1])
			if !ok || len(cells) != len(header) {
				break
			}
			writeRow(cells, "td")
			index++
		}
		table.WriteString("</tbody></table>")
		result = append(result, protect(table.String()))
	}
	return strings.Join(result, "\n")
}

func tableCells(line string) ([]string, bool) {
	if len(line)-len(strings.TrimLeft(line, " ")) > 3 || strings.HasPrefix(line, "\t") {
		return nil, false
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") || len(line) < 2 {
		return nil, false
	}
	line = line[1 : len(line)-1]
	var cells []string
	var cell strings.Builder
	for index := 0; index < len(line); index++ {
		switch {
		case line[index] == '\\' && index+1 < len(line):
			if line[index+1] != '|' {
				cell.WriteByte(line[index])
			}
			index++
			cell.WriteByte(line[index])
		case line[index] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[index])
		}
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	return cells, true
}
