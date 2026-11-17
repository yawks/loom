package messageformat

import (
	"strings"
	"testing"
)

func TestSlackRichProposalPreservesSpacingBulletsAndCode(t *testing.T) {
	input := "**Proposition d’accompagnement**\n\nNous proposons deux phases :\n\n" +
		"- **Phase 1** : faisabilité.\n- **Phase 2** : intégration.\n\n**Périmètre**\n\n" +
		"- Planification :\n  - Recherche de produits\n  - Comparaison des fiches\n\n" +
		"```\nPoste\tCharge\tMontant\nExpertise technique\t10 JH\t10 000 € HT\n- literal **code**\n```"
	got := Slack(input)
	for _, want := range []string{
		"*Proposition d’accompagnement*\n\nNous proposons deux phases :\n\n",
		"• *Phase 1* : faisabilité.\n• *Phase 2* : intégration.\n\n",
		"• Planification :\n  • Recherche de produits\n  • Comparaison des fiches\n\n",
		"```\nPoste\tCharge\tMontant\nExpertise technique\t10 JH\t10 000 € HT\n- literal **code**\n```",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Slack output %q missing %q", got, want)
		}
	}
	if strings.Count(got, "\n\n") != strings.Count(input, "\n\n") {
		t.Fatalf("blank-line count changed: %q", got)
	}
}
