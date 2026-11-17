package messageformat

import (
	"strings"
	"testing"
)

func TestTeamsHTMLSendsRealTable(t *testing.T) {
	input := "Chiffres clés\n\n| Indicateur | S45-46 | vs S43-44 |\n| --- | --- | --- |\n" +
		"| 🔴 Critique | **1 compte** · 1,7 k€ MRR | = |\n| Churn | 0 | 1 · 1,33 k€ |\n\nCODIR : suivi."
	got := TeamsHTML(input)
	for _, want := range []string{"<table><thead><tr><th>Indicateur</th>", "<td><strong>1 compte</strong> · 1,7 k€ MRR</td>", "</tbody></table>", "CODIR : suivi."} {
		if !strings.Contains(got, want) {
			t.Fatalf("table HTML %q missing %q", got, want)
		}
	}
	if strings.Count(got, "<th>") != 3 || strings.Count(got, "<td>") != 6 || strings.Contains(got, "| --- |") {
		t.Fatalf("incorrect table dimensions: %s", got)
	}
}

func TestTeamsHTMLTablePreservesSafeInlineContent(t *testing.T) {
	input := "| A | B |\n| --- | --- |\n| `x|y` | [source](https://example.com) |\n" +
		"| a\\|b | <script>alert(1)</script> |"
	got := TeamsHTML(input)
	for _, want := range []string{"<td><code>x|y</code></td>", `<a href="https://example.com">source</a>`, "<td>a|b</td>", "&lt;script&gt;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table HTML %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "LOOMHTMLTOKEN") || strings.Contains(got, "<script>") {
		t.Fatalf("unsafe or unrestored table content: %s", got)
	}
	for _, literal := range []string{"```\n| A | B |\n| --- | --- |\n| x | y |\n```", "| A | B |\n| no | separator |"} {
		if got := TeamsHTML(literal); strings.Contains(got, "<table>") {
			t.Fatalf("literal became a table: %s", got)
		}
	}
}
