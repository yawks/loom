package matrix

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

func (p *Provider) SendTypingIndicator(room string, typing bool) error {
	timeout := 0
	if typing {
		timeout = 30000
	}
	return p.do(noCancel(), http.MethodPut, p.roomPath(room)+"/typing/"+url.PathEscape(p.CurrentUserID()), nil, map[string]any{"typing": typing, "timeout": timeout}, nil)
}
func (p *Provider) MarkMessageAsRead(room, id string) error {
	return p.do(noCancel(), http.MethodPost, p.roomPath(room)+"/receipt/m.read/"+url.PathEscape(id), nil, map[string]string{}, nil)
}
func (p *Provider) MarkConversationAsRead(room string) error {
	messages, err := p.GetConversationHistory(room, 1, nil, nil)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	return p.MarkMessageAsRead(room, messages[len(messages)-1].ProtocolMsgID)
}
func (p *Provider) MuteConversation(room string) error   { return p.setPushRule(room, true) }
func (p *Provider) UnmuteConversation(room string) error { return p.setPushRule(room, false) }
func (p *Provider) setPushRule(room string, mute bool) error {
	path := "/pushrules/global/room/" + url.PathEscape(core.StripConvID(room))
	if !mute {
		return p.do(noCancel(), http.MethodDelete, path, nil, nil, nil)
	}
	return p.do(noCancel(), http.MethodPut, path, nil, map[string]any{"actions": []any{"dont_notify"}}, nil)
}
func (p *Provider) GetConversationState(room string) (*models.Conversation, error) {
	return &models.Conversation{ProtocolConvID: p.namespacedRoom(core.StripConvID(room))}, nil
}
func (p *Provider) PinConversation(string) error {
	return fmt.Errorf("matrix: pinning conversations is not a protocol feature")
}
func (p *Provider) UnpinConversation(string) error {
	return fmt.Errorf("matrix: pinning conversations is not a protocol feature")
}
func (p *Provider) MarkMessageAsPlayed(string, string) error {
	return fmt.Errorf("matrix: played receipts are not supported")
}
func (p *Provider) SendRetryReceipt(string, string) error {
	return fmt.Errorf("matrix: retry receipts are not supported")
}
func (p *Provider) SendStatusMessage(string, *core.Attachment) (*models.Message, error) {
	return nil, fmt.Errorf("matrix: status messages are not supported")
}

var _ = time.Time{}
