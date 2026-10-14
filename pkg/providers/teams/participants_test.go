package teams

import (
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"testing"
)

func TestGroupParticipantsRetainMemberNamesWithoutDirectory(t *testing.T) {
	const userID = "8:orgid:e775d689-4822-4319-81d1-2c128e187ea8"
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.CacheDisplayName("8:orgid:known", "Known Name")
	participants := teamsGroupParticipants(client, []msteams.Member{
		{MRI: userID, DisplayName: " Alice Martin ", Role: "Admin"},
		{MRI: "8:orgid:self", DisplayName: "Me"},
		{MRI: "8:orgid:known", DisplayName: "8:orgid:known"},
		{MRI: "8:orgid:unknown"},
		{DisplayName: "Missing ID"},
	})
	if len(participants) != 4 || participants[0].UserID != userID || !participants[0].IsAdmin || !participants[1].IsSelf {
		t.Fatalf("unexpected participants: %+v", participants)
	}
	if name := client.CachedDisplayName(userID); name != "Alice Martin" {
		t.Fatalf("participant name = %q", name)
	}
	if name := client.CachedDisplayName("8:orgid:known"); name != "Known Name" {
		t.Fatalf("technical member label replaced known name: %q", name)
	}
}
