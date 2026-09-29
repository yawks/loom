package teams

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"strconv"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestTeamsQuickPollCardBecomesCanonicalPoll(t *testing.T) {
	raw := `{"actions":[{"data":{"data":{"FormId":"form","Options":["Oui","Non"],"Type":"QuickPoll.SubmitVote"},"msteams":{"type":"invoke"}},"title":"Vote","type":"Action.Submit"}],"body":[{"items":[{"choices":[{"title":"Oui","value":"0"},{"title":"Non","value":"1"}],"id":"choice","isMultiSelect":true,"label":"Disponible ?","type":"Input.ChoiceSet","value":"1"}],"type":"Container"}],"type":"AdaptiveCard"}`
	poll, payload, title := teamsPollFromCard(raw)
	if poll == nil || poll.Question != "Disponible ?" || poll.MaxSelections != 2 || len(poll.Options) != 2 {
		t.Fatalf("unexpected poll: %+v", poll)
	}
	if !poll.Options[1].Selected || title != "Vote" || payload["data"] == nil {
		t.Fatalf("vote metadata was not retained: poll=%+v title=%q payload=%+v", poll, title, payload)
	}
}

func TestUpgradeStoredTeamsPoll(t *testing.T) {
	card := `{"actions":[{"data":{"data":{"Type":"QuickPoll.SubmitVote"},"msteams":{"type":"invoke"}},"title":"Vote","type":"Action.Submit"}],"body":[{"choices":[{"title":"Oui","value":"0"}],"id":"choice","label":"Question","type":"Input.ChoiceSet"}],"type":"AdaptiveCard"}`
	attachments := `[{"type":"adaptive_card","cardJson":` + strconv.Quote(card) + `},{"type":"image","url":"image.jpg"}]`
	message := models.Message{SenderID: "28:bot", Attachments: attachments}
	if !upgradeStoredTeamsPoll(&message) || message.Poll == nil || message.Poll.Question != "Question" {
		t.Fatalf("stored card was not upgraded: %+v", message)
	}
	if message.PollTransportSenderID != "28:bot" || strings.Contains(message.Attachments, "adaptive_card") || !strings.Contains(message.Attachments, "image.jpg") {
		t.Fatalf("attachments or transport metadata are incorrect: %+v", message)
	}
}

func TestStoreMessagesUpgradesPersistedAdaptiveCardToPoll(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.Reaction{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	conversation := models.Conversation{ProtocolConvID: "teams-1::chat"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	stored := models.Message{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "poll", ConversationID: conversation.ID, Attachments: `[{"type":"adaptive_card"}]`}
	if err := database.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}
	incoming := models.Message{
		ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "poll",
		Poll:                  &models.Poll{Question: "Question", Options: []models.PollOption{{ID: "0", Text: "Oui"}}, MaxSelections: 1},
		PollTransportSenderID: "28:bot", PollVotePayload: map[string]any{"choice": ""}, PollVoteActionTitle: "Vote",
	}
	if err := (&Provider{instance: "teams-1"}).storeMessages([]models.Message{incoming}); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&stored, stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Poll == nil || stored.Attachments != "" || stored.PollTransportSenderID != "28:bot" || stored.PollVoteActionTitle != "Vote" {
		t.Fatalf("persisted poll was not upgraded: %+v", stored)
	}
}
