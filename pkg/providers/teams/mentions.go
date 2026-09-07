package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

const teamsMentionTokenPrefix = "LOOMCANONICALMENTION"

// extractCanonicalTeamsMentions replaces wire mention elements with inert
// tokens before the general HTML-to-Markdown conversion strips their metadata.
func extractCanonicalTeamsMentions(content string, remote []msteams.Mention) (string, []models.MessageMention) {
	mentions := make([]models.MessageMention, 0, len(remote))
	add := func(userID, name string) string {
		name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "@"))
		if userID == "" || name == "" {
			if name == "" {
				return ""
			}
			return "@" + name
		}
		index := len(mentions)
		mentions = append(mentions, models.MessageMention{UserID: userID, DisplayName: name})
		return fmt.Sprintf("%s%dTOKEN", teamsMentionTokenPrefix, index)
	}
	out := msteams.RewriteTeamsSpanMentions(content, func(itemID, name string) string {
		var index int
		if _, err := fmt.Sscanf(itemID, "%d", &index); err != nil || index < 0 || index >= len(remote) {
			return "@" + strings.TrimPrefix(strings.TrimSpace(name), "@")
		}
		return add(remote[index].UserID, name)
	})
	out = msteams.RewriteTeamsMentions(out, func(userID, name string) string { return add(userID, name) })
	return out, mentions
}

func materializeCanonicalTeamsMentions(body string, mentions []models.MessageMention) (string, []models.MessageMention) {
	result := make([]models.MessageMention, 0, len(mentions))
	for index, mention := range mentions {
		token := fmt.Sprintf("%s%dTOKEN", teamsMentionTokenPrefix, index)
		position := strings.Index(body, token)
		if position < 0 {
			continue
		}
		visible := mention.DisplayName
		mention.Start = len(utf16.Encode([]rune(body[:position])))
		mention.Length = len(utf16.Encode([]rune(visible)))
		body = body[:position] + visible + body[position+len(token):]
		result = append(result, mention)
	}
	return body, mergeCanonicalMentionFragments(body, result)
}

func mergeCanonicalMentionFragments(body string, mentions []models.MessageMention) []models.MessageMention {
	if len(mentions) < 2 {
		return mentions
	}
	sort.SliceStable(mentions, func(i, j int) bool { return mentions[i].Start < mentions[j].Start })
	merged := make([]models.MessageMention, 0, len(mentions))
	for _, mention := range mentions {
		if len(merged) == 0 {
			merged = append(merged, mention)
			continue
		}
		previous := &merged[len(merged)-1]
		previousEnd := previous.Start + previous.Length
		gapStart := utf16RuneIndex(body, previousEnd)
		gapEnd := utf16RuneIndex(body, mention.Start)
		if previous.UserID == mention.UserID && gapStart <= gapEnd && strings.TrimSpace(string([]rune(body)[gapStart:gapEnd])) == "" {
			previous.Length = mention.Start + mention.Length - previous.Start
			previous.DisplayName = strings.TrimSpace(previous.DisplayName + " " + mention.DisplayName)
			continue
		}
		merged = append(merged, mention)
	}
	return merged
}

func utf16RuneIndex(value string, target int) int {
	units := 0
	for index, r := range []rune(value) {
		width := len(utf16.Encode([]rune{r}))
		if units+width > target {
			return index
		}
		units += width
		if units == target {
			return index + 1
		}
	}
	return len([]rune(value))
}

// RefreshHistoricalMessageMetadata repairs visible messages created before
// canonical mentions were persisted. A page is audited once per process and
// the remote request is bounded so opening a conversation cannot hang.
func (p *Provider) RefreshHistoricalMessageMetadata(conversationID string, messages []models.Message) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	targets := make(map[string]struct{}, len(messages))
	var oldest, newest time.Time
	for _, message := range messages {
		if message.ProtocolMsgID == "" {
			continue
		}
		ids = append(ids, message.ProtocolMsgID)
		targets[message.ProtocolMsgID] = struct{}{}
		if oldest.IsZero() || message.Timestamp.Before(oldest) {
			oldest = message.Timestamp
		}
		if newest.IsZero() || message.Timestamp.After(newest) {
			newest = message.Timestamp
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	pageKey := conversationID + "\x00" + strings.Join(ids, "\x00")
	p.metadataMu.Lock()
	if _, done := p.metadataPages[pageKey]; done {
		p.metadataMu.Unlock()
		return nil
	}
	p.metadataPages[pageKey] = struct{}{}
	p.metadataMu.Unlock()
	completed := false
	defer func() {
		if completed {
			return
		}
		p.metadataMu.Lock()
		delete(p.metadataPages, pageKey)
		p.metadataMu.Unlock()
	}()

	type result struct {
		messages []models.Message
		err      error
	}
	resultCh := make(chan result, 1)
	since := oldest.Add(-time.Second)
	before := newest.Add(time.Second)
	go func() {
		fetched, err := p.GetConversationHistory(conversationID, 500, &before, &since)
		resultCh <- result{messages: fetched, err: err}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var fetched []models.Message
	select {
	case <-ctx.Done():
		return fmt.Errorf("refresh historical message metadata: %w", ctx.Err())
	case outcome := <-resultCh:
		if outcome.err != nil {
			return outcome.err
		}
		fetched = outcome.messages
	}
	updated := make([]models.Message, 0, len(fetched))
	for _, message := range fetched {
		if _, wanted := targets[message.ProtocolMsgID]; wanted {
			updated = append(updated, message)
		}
	}
	if len(updated) == 0 {
		completed = true
		return nil
	}
	if err := p.storeMessages(updated); err != nil {
		return err
	}
	p.emit(core.MessageBatchEvent{
		InstanceID: p.instance, ConversationID: core.BuildConvID(p.instance, core.StripConvID(conversationID)),
		Messages: updated, IsHistorical: true,
	})
	completed = true
	return nil
}
