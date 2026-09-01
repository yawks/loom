package signal

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"fmt"
)

func unsupported(name string) error                             { return fmt.Errorf("signal: %s is not supported", name) }
func (p *Provider) GetThreads(string) ([]models.Message, error) { return nil, unsupported("threads") }
func (p *Provider) CreateGroup(string, []string) (*models.Conversation, error) {
	return nil, unsupported("create group")
}
func (p *Provider) UpdateGroupName(string, string) error { return unsupported("rename group") }
func (p *Provider) AddGroupParticipants(string, []string) error {
	return unsupported("add group participants")
}
func (p *Provider) RemoveGroupParticipants(string, []string) error {
	return unsupported("remove group participants")
}
func (p *Provider) LeaveGroup(string) error { return unsupported("leave group") }
func (p *Provider) PromoteGroupAdmins(string, []string) error {
	return unsupported("promote group admins")
}
func (p *Provider) DemoteGroupAdmins(string, []string) error {
	return unsupported("demote group admins")
}
func (p *Provider) GetGroupParticipants(string) ([]models.GroupParticipant, error) {
	return nil, unsupported("group participants")
}
func (p *Provider) CreateGroupInviteLink(string) (string, error) {
	return "", unsupported("group invite links")
}
func (p *Provider) RevokeGroupInviteLink(string) error { return unsupported("group invite links") }
func (p *Provider) JoinGroupByInviteLink(string) (*models.Conversation, error) {
	return nil, unsupported("join group link")
}
func (p *Provider) JoinGroupByInviteMessage(string) (*models.Conversation, error) {
	return nil, unsupported("join group message")
}
func (p *Provider) MarkMessageAsRead(string, string) error   { return nil }
func (p *Provider) MarkConversationAsRead(string) error      { return nil }
func (p *Provider) MarkMessageAsPlayed(string, string) error { return nil }
func (p *Provider) PinConversation(string) error             { return unsupported("pin conversation") }
func (p *Provider) UnpinConversation(string) error           { return unsupported("pin conversation") }
func (p *Provider) MuteConversation(string) error            { return unsupported("mute conversation") }
func (p *Provider) UnmuteConversation(string) error          { return unsupported("mute conversation") }
func (p *Provider) GetConversationState(string) (*models.Conversation, error) {
	return nil, unsupported("conversation state")
}
func (p *Provider) SendRetryReceipt(string, string) error { return unsupported("retry receipt") }
func (p *Provider) SendStatusMessage(string, *core.Attachment) (*models.Message, error) {
	return nil, unsupported("status message")
}
