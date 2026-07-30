package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

func mockProviderInfos() []core.ProviderInfo {
	return []core.ProviderInfo{
		mockProviderInfo("slack", "Slack", "Acme Studio", true),
		mockProviderInfo("whatsapp", "WhatsApp", "Personal", false),
		mockProviderInfo("teams", "Microsoft Teams", "Northstar Inc.", false),
		mockProviderInfo("googlechat", "Google Chat", "Product Workspace", false),
		mockProviderInfo("googlemessages", "Google Messages", "Pixel 9", false),
	}
}

func mockProviderInfo(id, name, instanceName string, active bool) core.ProviderInfo {
	return core.ProviderInfo{
		ID: id, InstanceID: id + "-mock", InstanceName: instanceName,
		Name: name, Description: "Demo account for screenshots", IsActive: active,
		Config: core.ProviderConfig{},
	}
}

type mockConversation struct {
	name, protocol, userID, status, avatar string
	group                                  bool
	messages                               []models.Message
}

func seedMockData() error {
	now := time.Now().Truncate(time.Minute)
	alexAvatar := mockAvatar("Alex Morgan", "#2563EB", "#DBEAFE")
	emmaAvatar := mockAvatar("Emma Wilson", "#DB2777", "#FCE7F3")
	jamesAvatar := mockAvatar("James Carter", "#059669", "#D1FAE5")
	oliviaAvatar := mockAvatar("Olivia Brown", "#7C3AED", "#EDE9FE")
	noahAvatar := mockAvatar("Noah Williams", "#EA580C", "#FFEDD5")

	imageAttachment := mockAttachment(models.Attachment{
		Type: "image", URL: mockProductImage(), FileName: "mobile-dashboard.svg",
		FileSize: 184320, MimeType: "image/svg+xml",
	})
	voiceAttachment := mockAttachment(models.Attachment{
		Type: "voice", URL: mockVoiceNote(), FileName: "voice-note.wav",
		FileSize: 288044, MimeType: "audio/wav", Duration: 18,
	})

	threadID := "mock-launch-08"
	quotedID := "mock-launch-05"
	quotedBody := "The onboarding completion rate is up by 18% in the latest test."
	quotedSenderID := "emma"
	launchMessages := []models.Message{
		mockMessage("mock-launch-01", "emma", "Emma Wilson", emmaAvatar, "Good morning! I pulled the overnight numbers into the launch dashboard.", now.Add(-4*time.Hour-32*time.Minute), false),
		mockMessage("mock-launch-02", "james", "James Carter", jamesAvatar, "Nice. Are the mobile conversion events included now?", now.Add(-4*time.Hour-25*time.Minute), false),
		mockMessage("mock-launch-03", "me", "Alex Morgan", alexAvatar, "Yes — web and mobile are using the same funnel definition as of this morning.", now.Add(-4*time.Hour-18*time.Minute), true),
		mockMessage("mock-launch-04", "olivia", "Olivia Brown", oliviaAvatar, "I also finished the updated dashboard layout. Sharing a preview below.", now.Add(-4*time.Hour-7*time.Minute), false),
		mockMessageWithAttachment("mock-launch-image", "olivia", "Olivia Brown", oliviaAvatar, "Here is the polished mobile view.", imageAttachment, now.Add(-3*time.Hour-58*time.Minute), false),
		mockMessage(quotedID, "emma", "Emma Wilson", emmaAvatar, quotedBody, now.Add(-3*time.Hour-42*time.Minute), false),
		mockReply("mock-launch-06", "me", "Alex Morgan", alexAvatar, "That is a great signal. Let’s feature it in tomorrow’s stakeholder update.", quotedID, quotedSenderID, "Emma Wilson", quotedBody, now.Add(-3*time.Hour-34*time.Minute), true),
		mockMessage("mock-launch-07", "james", "James Carter", jamesAvatar, "Agreed. I can add a short comparison against the previous release.", now.Add(-3*time.Hour-12*time.Minute), false),
		mockMessage(threadID, "emma", "Emma Wilson", emmaAvatar, "One question: should we move the rollout from 20% to 35% this afternoon?", now.Add(-2*time.Hour-54*time.Minute), false),
		mockThreadMessage("mock-thread-01", threadID, "james", "James Carter", jamesAvatar, "Error rates are flat, so I’m comfortable with 35%.", now.Add(-2*time.Hour-47*time.Minute), false),
		mockThreadMessage("mock-thread-02", threadID, "olivia", "Olivia Brown", oliviaAvatar, "No design blockers from my side.", now.Add(-2*time.Hour-41*time.Minute), false),
		mockThreadMessage("mock-thread-03", threadID, "me", "Alex Morgan", alexAvatar, "Let’s do it at 2 PM and watch the dashboard together.", now.Add(-2*time.Hour-36*time.Minute), true),
		mockThreadMessage("mock-thread-04", threadID, "emma", "Emma Wilson", emmaAvatar, "Perfect, I’ll schedule the checkpoint.", now.Add(-2*time.Hour-29*time.Minute), false),
		mockMessage("mock-launch-09", "noah", "Noah Williams", noahAvatar, "Support macros are updated and the team has the new escalation guide.", now.Add(-2*time.Hour-8*time.Minute), false),
		mockMessage("mock-launch-10", "me", "Alex Morgan", alexAvatar, "Thanks, Noah. That was the last item on the readiness checklist.", now.Add(-1*time.Hour-56*time.Minute), true),
		mockMessageWithAttachment("mock-launch-voice", "emma", "Emma Wilson", emmaAvatar, "", voiceAttachment, now.Add(-1*time.Hour-31*time.Minute), false),
		mockMessage("mock-launch-12", "james", "James Carter", jamesAvatar, "The production build is signed and ready to ship.", now.Add(-58*time.Minute), false),
		mockMessage("mock-launch-13", "olivia", "Olivia Brown", oliviaAvatar, "Everything looks sharp on the final device pass ✨", now.Add(-36*time.Minute), false),
		mockMessage("mock-launch-14", "me", "Alex Morgan", alexAvatar, "Amazing work, everyone. See you at the 2 PM rollout checkpoint!", now.Add(-14*time.Minute), true),
	}

	conversations := []mockConversation{
		{name: "Product Launch", protocol: "slack", userID: "C-LAUNCH", status: "online", group: true, avatar: mockAvatar("Product Launch", "#4F46E5", "#E0E7FF"), messages: launchMessages},
		{name: "Emma Wilson", protocol: "whatsapp", userID: "emma@s.whatsapp.net", status: "online", avatar: emmaAvatar, messages: []models.Message{
			mockMessage("mock-emma-01", "emma", "Emma Wilson", emmaAvatar, "Hey Alex, are you still free for coffee tomorrow?", now.Add(-19*time.Minute), false),
			mockMessage("mock-emma-02", "emma", "Emma Wilson", emmaAvatar, "I found a quiet place near the office ☕", now.Add(-16*time.Minute), false),
		}},
		{name: "Leadership Weekly", protocol: "teams", userID: "19:leadership@thread.v2", status: "meeting", group: true, avatar: mockAvatar("Leadership Weekly", "#5059C9", "#E0E7FF"), messages: []models.Message{
			mockMessage("mock-leadership-01", "sarah", "Sarah Mitchell", mockAvatar("Sarah Mitchell", "#0891B2", "#CFFAFE"), "The agenda for Thursday is ready for review.", now.Add(-73*time.Minute), false),
		}},
		{name: "James Carter", protocol: "googlechat", userID: "users/james", status: "away", avatar: jamesAvatar, messages: []models.Message{
			mockMessage("mock-james-01", "james", "James Carter", jamesAvatar, "I left the performance notes in the shared document.", now.Add(-2*time.Hour-12*time.Minute), false),
		}},
		{name: "Family", protocol: "googlemessages", userID: "family-thread", status: "offline", group: true, avatar: mockAvatar("Family", "#D97706", "#FEF3C7"), messages: []models.Message{
			mockMessage("mock-family-01", "chris", "Chris Morgan", mockAvatar("Chris Morgan", "#B45309", "#FEF3C7"), "Sunday dinner at our place — 6 PM works for everyone?", now.Add(-5*time.Hour), false),
		}},
		{name: "Design Critique", protocol: "slack", userID: "C-DESIGN", status: "online", group: true, avatar: mockAvatar("Design Critique", "#9333EA", "#F3E8FF"), messages: []models.Message{
			mockMessage("mock-design-01", "olivia", "Olivia Brown", oliviaAvatar, "The revised empty states are ready in the design file.", now.Add(-7*time.Hour), false),
		}},
		{name: "Noah Williams", protocol: "whatsapp", userID: "noah@s.whatsapp.net", status: "busy", avatar: noahAvatar, messages: []models.Message{
			mockMessage("mock-noah-01", "noah", "Noah Williams", noahAvatar, "I’ll send you the customer notes before lunch.", now.Add(-20*time.Hour), false),
		}},
		{name: "Customer Experience", protocol: "teams", userID: "19:cx@thread.v2", status: "online", group: true, avatar: mockAvatar("Customer Experience", "#0284C7", "#E0F2FE"), messages: []models.Message{
			mockMessage("mock-cx-01", "mia", "Mia Davis", mockAvatar("Mia Davis", "#0D9488", "#CCFBF1"), "This week’s satisfaction score just reached 94%.", now.Add(-23*time.Hour), false),
		}},
		{name: "Research Circle", protocol: "googlechat", userID: "spaces/research", status: "away", group: true, avatar: mockAvatar("Research Circle", "#16A34A", "#DCFCE7"), messages: []models.Message{
			mockMessage("mock-research-01", "liam", "Liam Anderson", mockAvatar("Liam Anderson", "#15803D", "#DCFCE7"), "The interview synthesis is ready for the team.", now.Add(-27*time.Hour), false),
		}},
		{name: "Daniel Thompson", protocol: "googlemessages", userID: "daniel-thread", status: "offline", avatar: mockAvatar("Daniel Thompson", "#DC2626", "#FEE2E2"), messages: []models.Message{
			mockMessage("mock-daniel-01", "daniel", "Daniel Thompson", mockAvatar("Daniel Thompson", "#DC2626", "#FEE2E2"), "Great seeing you yesterday! Let’s do that again soon.", now.Add(-31*time.Hour), false),
		}},
	}

	for i, item := range conversations {
		if err := createMockConversation(i, item); err != nil {
			return err
		}
	}
	for _, info := range mockProviderInfos() {
		config := models.ProviderConfiguration{
			ProviderID: info.ID, InstanceID: info.InstanceID, InstanceName: info.InstanceName,
			ConfigJSON: "{}", IsActive: info.IsActive,
		}
		if err := db.DB.Create(&config).Error; err != nil {
			return err
		}
	}
	return db.ContactStore.Load()
}

func createMockConversation(index int, item mockConversation) error {
	meta := models.MetaContact{DisplayName: item.name, AvatarURL: item.avatar}
	if err := db.DB.Create(&meta).Error; err != nil {
		return err
	}
	instanceID := item.protocol + "-mock"
	account := models.LinkedAccount{
		MetaContactID: meta.ID, Protocol: item.protocol, ProviderInstanceID: instanceID,
		UserID: item.userID, Username: item.name, AvatarURL: item.avatar,
		Status: item.status, IsGroup: item.group,
	}
	if err := db.DB.Create(&account).Error; err != nil {
		return err
	}
	conversationID := fmt.Sprintf("%s::%s", instanceID, item.userID)
	conversation := models.Conversation{
		LinkedAccountID: account.ID, ProtocolConvID: conversationID,
		IsGroup: item.group, GroupName: item.name, IsPinned: index == 0,
	}
	if err := db.DB.Create(&conversation).Error; err != nil {
		return err
	}
	for i := range item.messages {
		item.messages[i].ConversationID = conversation.ID
		item.messages[i].ProtocolConvID = conversationID
		if item.messages[i].Attachments == "" {
			item.messages[i].Attachments = "[]"
		}
	}
	return db.DB.Create(&item.messages).Error
}

func mockMessage(id, senderID, senderName, avatar, body string, timestamp time.Time, fromMe bool) models.Message {
	return models.Message{
		ProtocolMsgID: id, SenderID: senderID, SenderName: senderName,
		SenderAvatarURL: avatar, Body: body, Timestamp: timestamp, IsFromMe: fromMe,
	}
}

func mockMessageWithAttachment(id, senderID, senderName, avatar, body, attachment string, timestamp time.Time, fromMe bool) models.Message {
	message := mockMessage(id, senderID, senderName, avatar, body, timestamp, fromMe)
	message.Attachments = attachment
	return message
}

func mockThreadMessage(id, threadID, senderID, senderName, avatar, body string, timestamp time.Time, fromMe bool) models.Message {
	message := mockMessage(id, senderID, senderName, avatar, body, timestamp, fromMe)
	message.ThreadID = &threadID
	return message
}

func mockReply(id, senderID, senderName, avatar, body, quotedID, quotedSenderID, quotedSenderName, quotedBody string, timestamp time.Time, fromMe bool) models.Message {
	message := mockMessage(id, senderID, senderName, avatar, body, timestamp, fromMe)
	message.QuotedMessageID = &quotedID
	message.QuotedSenderID = &quotedSenderID
	message.QuotedSenderName = quotedSenderName
	message.QuotedBody = &quotedBody
	return message
}

func mockAttachment(attachment models.Attachment) string {
	data, _ := json.Marshal([]models.Attachment{attachment})
	return string(data)
}

func mockAvatar(name, background, foreground string) string {
	var seed uint32
	for _, character := range name {
		seed = seed*31 + uint32(character)
	}
	gender := "men"
	switch name {
	case "Emma Wilson", "Olivia Brown", "Sarah Mitchell", "Mia Davis":
		gender = "women"
	default:
		if seed%2 == 0 {
			gender = "women"
		}
	}
	portraitIndex := 10 + int(seed%80)
	return fmt.Sprintf("https://randomuser.me/api/portraits/%s/%d.jpg", gender, portraitIndex)
}

func mockProductImage() string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="960" height="600" viewBox="0 0 960 600"><defs><linearGradient id="b" x2="1" y2="1"><stop stop-color="#312e81"/><stop offset="1" stop-color="#2563eb"/></linearGradient></defs><rect width="960" height="600" rx="32" fill="url(#b)"/><text x="64" y="88" font-family="Arial" font-size="30" font-weight="700" fill="white">Launch dashboard</text><text x="64" y="126" font-family="Arial" font-size="17" fill="#bfdbfe">Mobile conversion overview</text><rect x="64" y="168" width="250" height="154" rx="18" fill="white" opacity=".96"/><rect x="340" y="168" width="250" height="154" rx="18" fill="white" opacity=".96"/><rect x="616" y="168" width="280" height="154" rx="18" fill="white" opacity=".96"/><text x="88" y="211" font-family="Arial" font-size="16" fill="#64748b">Conversion</text><text x="88" y="272" font-family="Arial" font-size="46" font-weight="700" fill="#0f172a">28.4%</text><text x="364" y="211" font-family="Arial" font-size="16" fill="#64748b">New users</text><text x="364" y="272" font-family="Arial" font-size="46" font-weight="700" fill="#0f172a">12.8k</text><text x="640" y="211" font-family="Arial" font-size="16" fill="#64748b">Satisfaction</text><text x="640" y="272" font-family="Arial" font-size="46" font-weight="700" fill="#0f172a">94%</text><rect x="64" y="354" width="832" height="184" rx="18" fill="#eff6ff"/><path d="M100 486 C190 450 230 470 310 418 S450 454 530 390 S690 428 850 374" fill="none" stroke="#2563eb" stroke-width="9" stroke-linecap="round"/></svg>`
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

func mockVoiceNote() string {
	const sampleRate = 8000
	const seconds = 18
	samples := sampleRate * seconds
	data := make([]byte, 44+samples*2)
	copy(data[0:], "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], sampleRate)
	binary.LittleEndian.PutUint32(data[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(samples*2))
	for i := 0; i < samples; i++ {
		envelope := math.Sin(math.Pi * float64(i) / float64(samples))
		value := int16(7000 * envelope * math.Sin(2*math.Pi*220*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(value))
	}
	return "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(data)
}
