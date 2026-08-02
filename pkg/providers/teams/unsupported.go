package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"fmt"
	"time"
)

type unsupportedProvider struct{}

func unsupported(op string) error                       { return fmt.Errorf("%s: %s not supported", providerID, op) }
func (unsupportedProvider) SyncHistory(time.Time) error { return unsupported("SyncHistory") }
func (unsupportedProvider) GetContacts() ([]models.LinkedAccount, error) {
	return nil, unsupported("GetContacts")
}
func (unsupportedProvider) GetConversationHistory(string, int, *time.Time, *time.Time) ([]models.Message, error) {
	return nil, unsupported("GetConversationHistory")
}
func (unsupportedProvider) SendMessage(string, string, *core.Attachment, *string) (*models.Message, error) {
	return nil, unsupported("SendMessage")
}
func (unsupportedProvider) SendReply(string, string, string) (*models.Message, error) {
	return nil, unsupported("SendReply")
}
func (unsupportedProvider) SendThreadReply(string, string, string, string) (*models.Message, error) {
	return nil, unsupported("SendThreadReply")
}
func (unsupportedProvider) SendFile(string, *core.Attachment, *string) (*models.Message, error) {
	return nil, unsupported("SendFile")
}
func (unsupportedProvider) EditMessage(string, string, string) (*models.Message, error) {
	return nil, unsupported("EditMessage")
}
func (unsupportedProvider) DeleteMessage(string, string) error { return unsupported("DeleteMessage") }
func (unsupportedProvider) GetThreads(string) ([]models.Message, error) {
	return nil, unsupported("GetThreads")
}
func (unsupportedProvider) AddReaction(string, string, string) error {
	return unsupported("AddReaction")
}
func (unsupportedProvider) RemoveReaction(string, string, string) error {
	return unsupported("RemoveReaction")
}
func (unsupportedProvider) SendTypingIndicator(string, bool) error {
	return unsupported("SendTypingIndicator")
}
func (unsupportedProvider) CreateGroup(string, []string) (*models.Conversation, error) {
	return nil, unsupported("CreateGroup")
}
func (unsupportedProvider) UpdateGroupName(string, string) error {
	return unsupported("UpdateGroupName")
}
func (unsupportedProvider) AddGroupParticipants(string, []string) error {
	return unsupported("AddGroupParticipants")
}
func (unsupportedProvider) LeaveGroup(string) error { return unsupported("LeaveGroup") }
func (unsupportedProvider) PromoteGroupAdmins(string, []string) error {
	return unsupported("PromoteGroupAdmins")
}
func (unsupportedProvider) DemoteGroupAdmins(string, []string) error {
	return unsupported("DemoteGroupAdmins")
}
func (unsupportedProvider) GetGroupParticipants(string) ([]models.GroupParticipant, error) {
	return nil, unsupported("GetGroupParticipants")
}
func (unsupportedProvider) CreateGroupInviteLink(string) (string, error) {
	return "", unsupported("CreateGroupInviteLink")
}
func (unsupportedProvider) RevokeGroupInviteLink(string) error {
	return unsupported("RevokeGroupInviteLink")
}
func (unsupportedProvider) JoinGroupByInviteLink(string) (*models.Conversation, error) {
	return nil, unsupported("JoinGroupByInviteLink")
}
func (unsupportedProvider) JoinGroupByInviteMessage(string) (*models.Conversation, error) {
	return nil, unsupported("JoinGroupByInviteMessage")
}
func (unsupportedProvider) MarkMessageAsRead(string, string) error {
	return unsupported("MarkMessageAsRead")
}
func (unsupportedProvider) MarkConversationAsRead(string) error {
	return unsupported("MarkConversationAsRead")
}
func (unsupportedProvider) MarkMessageAsPlayed(string, string) error {
	return unsupported("MarkMessageAsPlayed")
}
func (unsupportedProvider) PinConversation(string) error    { return unsupported("PinConversation") }
func (unsupportedProvider) UnpinConversation(string) error  { return unsupported("UnpinConversation") }
func (unsupportedProvider) MuteConversation(string) error   { return unsupported("MuteConversation") }
func (unsupportedProvider) UnmuteConversation(string) error { return unsupported("UnmuteConversation") }
func (unsupportedProvider) GetConversationState(string) (*models.Conversation, error) {
	return nil, unsupported("GetConversationState")
}
func (unsupportedProvider) SendRetryReceipt(string, string) error {
	return unsupported("SendRetryReceipt")
}
func (unsupportedProvider) SendStatusMessage(string, *core.Attachment) (*models.Message, error) {
	return nil, unsupported("SendStatusMessage")
}
func (unsupportedProvider) GetContactName(string) (string, error) {
	return "", unsupported("GetContactName")
}
func (unsupportedProvider) GetCustomEmojis() (map[string]string, error) {
	return nil, unsupported("GetCustomEmojis")
}
func (unsupportedProvider) GetAuthQRCode() (string, error) { return "", unsupported("GetAuthQRCode") }
func (unsupportedProvider) RefreshContact(string) error    { return unsupported("RefreshContact") }
