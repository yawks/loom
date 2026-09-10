package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

type widgetContent struct {
	Type string `json:"type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// GetConversationApplications normalizes Matrix room widget state. m.widget
// is supported as the bot contract used here, not as a universally stable
// Matrix standard. The established Element state type is accepted too.
func (p *Provider) GetConversationApplications(ctx context.Context, conversationID string) ([]models.ConversationApplication, error) {
	var events []matrixEvent
	if err := p.do(ctx, http.MethodGet, p.roomPath(core.StripConvID(conversationID))+"/state", nil, nil, &events); err != nil {
		return nil, err
	}
	return normalizeWidgetEvents(events), nil
}

func normalizeWidgetEvents(events []matrixEvent) []models.ConversationApplication {
	byID := make(map[string]models.ConversationApplication)
	for _, event := range events {
		if event.StateKey == nil || (event.Type != "m.widget" && event.Type != "im.vector.modular.widgets") {
			continue
		}
		id := strings.TrimSpace(*event.StateKey)
		if id == "" {
			continue
		}
		var content widgetContent
		if len(event.Content) == 0 || string(event.Content) == "{}" || json.Unmarshal(event.Content, &content) != nil || strings.TrimSpace(content.URL) == "" {
			delete(byID, id)
			continue
		}
		launch, err := url.Parse(content.URL)
		if err != nil || (launch.Scheme != "https" && launch.Scheme != "http") || launch.Host == "" {
			continue
		}
		name := strings.TrimSpace(content.Name)
		if name == "" {
			name = id
		}
		byID[id] = models.ConversationApplication{ID: id, Name: name, LaunchURL: launch.String(), Origin: launch.Scheme + "://" + launch.Host}
	}
	result := make([]models.ConversationApplication, 0, len(byID))
	for _, application := range byID {
		result = append(result, application)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

var _ core.ConversationApplicationProvider = (*Provider)(nil)
