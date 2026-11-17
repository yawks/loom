package teams

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"Loom/pkg/models"
	"Loom/pkg/providers/messageformat"
	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func TestTeamsTextRoundTrip(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"A & B", "literal &amp;amp;", "one\ntwo", "one\n\ntwo", "one\n\n\ntwo",
		"- item\n\nnext", "title\n\n- item\n\nnext",
		"title\n- first\n- second\nnext",
		"*Section*\n\n- **First**\n- Second\n\n\n\n  \n\n*Next section*\n\n- Third",
	} {
		for _, contentType := range []string{"html", "Html", "HTML"} {
			remote := msteams.Message{Content: msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(input)), ContentType: contentType}
			got := NewProvider().toModelMessage(client, remote, "room")
			want := input
			if input == "literal &amp;amp;" {
				want = "literal &amp;"
			}
			if got.Body != want {
				t.Errorf("round trip (%s) %q = %q", contentType, input, got.Body)
			}
		}
	}
}

func TestTeamsSourceCRLFDoesNotDoublePlanningLineBreaks(t *testing.T) {
	// Same source shape as the planning message sent on November 3 at 09:36:
	// every visual <br> is followed by a source CRLF, including empty lines.
	input := "Je vous partage le planning <strong>MIA Matmut</strong>.  <br>\r\n" +
		"<br>\r\n<strong>📍 Où on en est</strong><br>\r\n" +
		"• En production.<br>\r\n• Recette le <strong>2/12</strong>.<br>\r\n" +
		"  <br>\r\n<strong>🗓️ Planning prévisionnel</strong><br>\r\n• Correctifs."
	want := "Je vous partage le planning **MIA Matmut**.  \n\n**📍 Où on en est**\n" +
		"• En production.\n• Recette le **2/12**.\n\n**🗓️ Planning prévisionnel**\n• Correctifs."
	if got := teamsHTMLToMarkdown(input); got != want {
		t.Fatalf("planning = %q, want %q", got, want)
	}
}

func TestTeamsStoredDoubledBreaksAreRepairedFromRemoteHTMLOnly(t *testing.T) {
	database := deletionTestDB(t)
	broken := "Planning **MIA Matmut**.  \n\n\n\n**📍 Où on en est**\n\n• En production.\n\n• Recette le **2/12**."
	want := "Planning **MIA Matmut**.  \n\n**📍 Où on en est**\n• En production.\n• Recette le **2/12**."
	remoteHTML := "Planning <strong>MIA Matmut</strong>.  <br>\r\n<br>\r\n<strong>📍 Où on en est</strong><br>\r\n• En production.<br>\r\n• Recette le <strong>2/12</strong>."
	messages := []models.Message{
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "planning", Body: broken},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "changed", Body: broken},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "failed", Body: broken},
		{ProtocolConvID: "teams-2::room", ProtocolMsgID: "other", Body: broken},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "intentional", Body: broken},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requested := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		mu.Lock()
		requested[id]++
		mu.Unlock()
		content := remoteHTML
		switch id {
		case "planning":
		case "changed":
			content = "<p>New remote edit</p>"
		case "intentional":
			content = strings.ReplaceAll(broken, "\n", "<br>")
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "content": content, "contenttype": "html", "messagetype": "RichText/Html"})
	}))
	defer server.Close()
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self", AuthToken: "auth", SkypeToken: "skype", Endpoints: msteams.Endpoints{ChatSvcBase: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	provider := NewProvider()
	provider.instance = "teams-1"
	repaired, err := provider.repairStoredHTMLFormatting(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 || len(repaired["room"]) != 1 || repaired["room"][0].ProtocolMsgID != "planning" {
		t.Fatalf("unexpected repaired messages: %+v", repaired)
	}
	for index, original := range messages {
		var stored models.Message
		if err := database.First(&stored, original.ID).Error; err != nil {
			t.Fatal(err)
		}
		expected := broken
		if index == 0 {
			expected = want
		}
		if stored.Body != expected {
			t.Errorf("%s body = %q, want %q", stored.ProtocolMsgID, stored.Body, expected)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requested) != 4 || requested["other"] != 0 {
		t.Fatalf("unexpected remote work: %+v", requested)
	}
}

func TestTeamsHTMLVisualLineBreaks(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"<p>one<br></p><p>two</p>", "one\ntwo"},
		{"<p>one</p><p><br></p><p>two</p>", "one\n\ntwo"},
		{"one<br><br><br>two", "one\n\n\ntwo"},
		{"<p>A &amp; B<br>two</p>", "A & B\ntwo"},
		{"<p>one</p>\r\n<p>two</p>", "one\ntwo"},
		{"<p>one</p>\r\n<p>\u00a0</p>\r\n<p>two</p>", "one\n \ntwo"},
		{"<div>\n  <p>one</p>\n  <p>two</p>\n</div>", "one\ntwo"},
		{"<p>one<br>\ntwo</p>", "one\ntwo"},
		{"<p>one <strong>two</strong>\n <em>three</em></p>", "one **two** *three*"},
		{"<div><div>one</div></div><div>two</div>", "one\ntwo"},
		{"<ul><li><p>one</p></li><li><p>two</p></li></ul>", "- one\n- two"},
		{"<p>one<br><br></p><p>two</p>", "one\n\ntwo"},
		{"<p>one\r\ncontinued</p>", "one continued"},
		{"one\n\ntwo", "one\n\ntwo"},
	} {
		if got := teamsHTMLToMarkdown(tc.input); got != tc.want {
			t.Errorf("teamsHTMLToMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTeamsTableBoundaryAfterInlineText(t *testing.T) {
	input := `<p>PI</p>EUREXO-WB-PB : deux décrochages successifs` +
		`<table><tr><th>Période</th><th>Sans humain</th></tr>` +
		`<tr><td>Octobre</td><td>8 %</td></tr></table><p><strong>Conclusion :</strong> baisse.</p>`
	want := "PI\nEUREXO-WB-PB : deux décrochages successifs\n\n| Période | Sans humain |\n| --- | --- |\n| Octobre | 8 % |\n\n**Conclusion :** baisse."
	if got := teamsHTMLToMarkdown(input); got != want {
		t.Fatalf("table boundary = %q, want %q", got, want)
	}
}

func TestTeamsReportingTableAfterListAndCaption(t *testing.T) {
	input := "<p><strong>En bref</strong></p><ul><li>Trois sorties d'alerte.</li>" +
		"<li>Dydu : +23,4 k€.</li></ul><p><strong>Chiffres clés</strong></p>" +
		"<table><tr><th>Indicateur</th><th>S45-46</th><th>vs S43-44</th></tr>" +
		"<tr><td>🔴 Critique</td><td>1 compte · 1,7 k€ MRR</td><td>=</td></tr>" +
		"<tr><td>Churn</td><td>0</td><td>1 · 1,33 k€</td></tr></table>" +
		"<p><strong>🙋 CODIR</strong> : aucune nouvelle décision.</p>"
	want := "**En bref**\n- Trois sorties d'alerte.\n- Dydu : +23,4 k€.\n**Chiffres clés**\n\n" +
		"| Indicateur | S45-46 | vs S43-44 |\n| --- | --- | --- |\n" +
		"| 🔴 Critique | 1 compte · 1,7 k€ MRR | = |\n| Churn | 0 | 1 · 1,33 k€ |\n\n" +
		"**🙋 CODIR** : aucune nouvelle décision."
	if got := teamsHTMLToMarkdown(input); got != want {
		t.Fatalf("reporting = %q, want %q", got, want)
	}
}

func TestTeamsOutgoingReportingTableRoundTrip(t *testing.T) {
	input := "Chiffres clés\n\n| Indicateur | S45-46 | vs S43-44 |\n| --- | --- | --- |\n" +
		"| 🔴 Critique | 1 compte · 1,7 k€ MRR | = |\n| Churn | 0 | 1 · 1,33 k€ |\n\nCODIR : suivi."
	remote := msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(input))
	if !strings.Contains(remote, "<table>") {
		t.Fatalf("outgoing table was flattened: %s", remote)
	}
	got := teamsHTMLToMarkdown(remote)
	for _, want := range []string{
		"| Indicateur | S45-46 | vs S43-44 |", "| --- | --- | --- |",
		"| 🔴 Critique | 1 compte · 1,7 k€ MRR | = |", "| Churn | 0 | 1 · 1,33 k€ |", "CODIR : suivi.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("round trip %q missing %q", got, want)
		}
	}
}

func TestTeamsAnalysisPreservesHeadingsRulesAndTableEmphasis(t *testing.T) {
	// Same structure as the November 10, 09:51 analysis: numbered h3 sections,
	// HTML rules, tables with an empty first header and bold numeric cells.
	input := "<p>Analyse</p>\r\n<ul><li>Trois ruptures.</li></ul>\r\n<hr>\r\n" +
		"<h3>1. FREEPRO-WB-IDENT : qualification</h3>\r\n" +
		"<table><thead><tr><th>&nbsp;</th><th>1–15 oct.</th><th>3 nov.</th></tr></thead>" +
		"<tbody><tr><td>Appels / jour</td><td>1 752</td><td><strong>3 213</strong></td></tr>" +
		"<tr><td>Qualification</td><td>92 %</td><td><strong>80 %</strong></td></tr></tbody></table>\r\n" +
		"<p><strong>Lecture</strong></p><ul><li><strong>Le volume augmente.</strong></li></ul>\r\n<hr>\r\n" +
		"<h3>2. MERCER-WB-SVI : afflux</h3>\r\n" +
		"<table><tr><th>&nbsp;</th><th>2 nov.</th></tr><tr><td>Automatisation</td><td><b>9 %</b></td></tr></table>\r\n" +
		"<ul><li>Conclusion.</li></ul>\r\n<hr>\r\n" +
		"<h3>3. QPARK-WB : DMC</h3>\r\n" +
		"<table><tr><th>&nbsp;</th><th>2 nov.</th></tr><tr><td>DMC</td><td><strong>45 s</strong></td></tr></table>"
	want := "Analyse\n- Trois ruptures.\n\n---\n\n### 1. FREEPRO-WB-IDENT : qualification\n\n" +
		"|  | 1–15 oct. | 3 nov. |\n| --- | --- | --- |\n| Appels / jour | 1 752 | **3 213** |\n| Qualification | 92 % | **80 %** |\n\n" +
		"**Lecture**\n- **Le volume augmente.**\n\n---\n\n### 2. MERCER-WB-SVI : afflux\n\n" +
		"|  | 2 nov. |\n| --- | --- |\n| Automatisation | **9 %** |\n\n" +
		"- Conclusion.\n\n---\n\n### 3. QPARK-WB : DMC\n\n" +
		"|  | 2 nov. |\n| --- | --- |\n| DMC | **45 s** |"
	if got := teamsHTMLToMarkdown(input); got != want {
		t.Fatalf("analysis markdown:\n%s\nwant:\n%s", got, want)
	}
}

func TestTeamsTableCellsUseCanonicalInlineFormatting(t *testing.T) {
	input := `<table><tr><th>Label</th><th>Value</th></tr><tr>` +
		`<td><em>First</em><br>Second | value</td>` +
		`<td><a href="https://example.com">Source</a> <code>x</code> <u>underlined</u> <s>old</s></td></tr></table>`
	want := "| Label | Value |\n| --- | --- |\n" +
		"| *First*<br>Second \\| value | [Source](https://example.com) `x` <u>underlined</u> ~~old~~ |"
	if got := teamsHTMLToMarkdown(input); got != want {
		t.Fatalf("formatted cells = %q, want %q", got, want)
	}
}

func TestTeamsHTMLHeadingLevels(t *testing.T) {
	for level := 1; level <= 6; level++ {
		input := fmt.Sprintf("<p>Before</p><h%d>1. <strong>Heading</strong></h%d><p>After</p>", level, level)
		want := "Before\n" + strings.Repeat("#", level) + " 1. **Heading**\nAfter"
		if got := teamsHTMLToMarkdown(input); got != want {
			t.Errorf("h%d = %q, want %q", level, got, want)
		}
	}
}

func TestTeamsRepairLegacyGluedTableHeader(t *testing.T) {
	input := "PI\nEUREXO-WB-PB : deux décrochages successifs| Période | Automatisation | Automatisation totale | Sans humain | \n" +
		"| --- | --- | --- | --- |\n| 1er au 14 octobre | 19 à 22 % | 64 à 75 % | 13 à 15 % |"
	want := "PI\nEUREXO-WB-PB : deux décrochages successifs\n| Période | Automatisation | Automatisation totale | Sans humain |\n" +
		"| --- | --- | --- | --- |\n| 1er au 14 octobre | 19 à 22 % | 64 à 75 % | 13 à 15 % |"
	if got := normalizeTeamsEscapedTable(input); got != want {
		t.Fatalf("legacy table = %q, want %q", got, want)
	}
	if got := normalizeTeamsEscapedTable(want); got != want {
		t.Fatalf("repair is not idempotent: %q", got)
	}
}

func TestTeamsStoredFormattingRepairIsProviderScoped(t *testing.T) {
	database := deletionTestDB(t)
	broken := "Introduction| Période | Taux |\n| --- | --- |\n| Octobre | 8 % |"
	messages := []models.Message{
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "table", Body: broken},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "html", Body: "<p>one<br>\ntwo</p>"},
		{ProtocolConvID: "teams-2::room", ProtocolMsgID: "other-table", Body: broken},
		{ProtocolConvID: "teams-2::room", ProtocolMsgID: "other-html", Body: "<p>one<br>\ntwo</p>"},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	provider := NewProvider()
	provider.instance = "teams-1"
	repaired, err := provider.repairStoredHTMLFormatting(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 || len(repaired["room"]) != 2 {
		t.Fatalf("unexpected repaired work: %+v", repaired)
	}
	for index, original := range messages {
		var stored models.Message
		if err := database.First(&stored, original.ID).Error; err != nil {
			t.Fatal(err)
		}
		want := original.Body
		if index == 0 {
			want = "Introduction\n| Période | Taux |\n| --- | --- |\n| Octobre | 8 % |"
		} else if index == 1 {
			want = "one\ntwo"
		}
		if stored.Body != want {
			t.Errorf("message %d = %q, want %q", index, stored.Body, want)
		}
	}
	repaired, err = provider.repairStoredHTMLFormatting(nil)
	if err != nil || len(repaired) != 0 {
		t.Fatalf("second repair = %+v, %v", repaired, err)
	}
	provider.instance = ""
	if _, err := provider.repairStoredHTMLFormatting(nil); err == nil {
		t.Fatal("expected empty provider instance to fail closed")
	}
}
