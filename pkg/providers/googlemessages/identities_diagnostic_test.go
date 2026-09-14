package googlemessages

import (
	"fmt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"Loom/pkg/core"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// Explicitly enabled, read-only phone diagnostic. No sends, DB writes, session
// persistence, raw payloads, numbers, participant IDs or message text are logged.
func TestLiveLineDiagnostic(t *testing.T) {
	if os.Getenv("LOOM_GMESSAGES_LINE_DIAGNOSTIC") != "1" {
		t.Skip("requires explicit live diagnostic opt-in")
	}
	p := NewProvider()
	if err := p.Init(core.ProviderConfig{"_instance_id": "googlemessages-1"}); err != nil {
		t.Fatal(err)
	}
	p.client.SetEventHandler(func(event any) {
		if settings, ok := event.(*gmproto.Settings); ok {
			p.updateSIMCards(settings)
		}
	})
	if err := p.client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer p.client.Disconnect()
	response, err := p.client.ListConversations(12, gmproto.ListConversationsRequest_INBOX)
	if err != nil {
		t.Fatal(err)
	}
	unknownFields := map[string]int{}
	var incoming, outgoing, incomingPayload, outgoingPayload, incomingKnownLocalPayload, incomingLocalNumber, outgoingLocalNumber, incomingSomeIntMatch int
	for _, conversation := range response.GetConversations() {
		p.rememberConversationSIM(conversation)
		messages, err := p.client.FetchMessages(conversation.GetConversationID(), 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range messages.GetMessages() {
			participant := message.GetSenderParticipant()
			direction := "incoming"
			if participant.GetIsMe() {
				direction = "outgoing"
			}
			p.mu.RLock()
			localIDs := map[string]bool{}
			for _, card := range p.simCards {
				localIDs[card.GetSIMParticipant().GetID()] = true
				localIDs[strconv.Itoa(int(card.GetSIMData().GetSIMPayload().GetSIMNumber()))] = true
			}
			p.mu.RUnlock()
			summarizeUnknownMessage(message.ProtoReflect(), direction, localIDs, unknownFields, 0)
			if group := message.GetSomeKindOfGroupID(); group != nil {
				candidate := diagnosticUnknownString(group.ProtoReflect().GetUnknown(), 3)
				number := diagnosticPhoneDigits(candidate)
				if candidate != "" {
					unknownFields[direction+"/group-field3-present"]++
					if strings.HasPrefix(candidate, "+") {
						unknownFields[direction+"/group-field3-plus-prefix"]++
					}
					if strings.HasPrefix(candidate, "tel:") {
						unknownFields[direction+"/group-field3-tel-prefix"]++
					}
					if number != "" && number == diagnosticPhoneDigits(participant.GetID().GetNumber()) {
						unknownFields[direction+"/group-field3-matches-sender-number"]++
					}
					p.mu.RLock()
					for _, card := range p.simCards {
						matches := false
						for _, own := range []string{card.GetSIMData().GetFormattedPhoneNumber(), card.GetSIMData().GetInternationalPhoneNumber()} {
							local := diagnosticPhoneDigits(own)
							if local != "" && number == local {
								matches = true
							}
						}
						if matches {
							unknownFields[direction+"/group-field3-matches-local-number"]++
							break
						}
					}
					p.mu.RUnlock()
				}
			}
			if localIDs[message.GetParticipantID()] {
				unknownFields[direction+"/participant-id-matches-local"]++
			}

			payload := participant.GetSimPayload()
			if participant.GetIsMe() {
				p.mu.RLock()
				for _, card := range p.simCards {
					if payload != nil && card.GetSIMData().GetSIMPayload() != nil && payload.GetSIMNumber() == card.GetSIMData().GetSIMPayload().GetSIMNumber() {
						outgoingLocalNumber++
						break
					}
				}
				p.mu.RUnlock()
				outgoing++
				if payload != nil {
					outgoingPayload++
				}
				continue
			}
			incoming++
			if payload == nil {
				continue
			}
			incomingPayload++
			p.mu.RLock()
			for _, card := range p.simCards {
				local := card.GetSIMData().GetSIMPayload()
				if local != nil && payload.GetSIMNumber() == local.GetSIMNumber() && payload.GetTwo() == local.GetTwo() {
					incomingKnownLocalPayload++
					break
				}
			}
			for _, card := range p.simCards {
				local := card.GetSIMData().GetSIMPayload()
				if local != nil && payload.GetSIMNumber() == local.GetSIMNumber() {
					incomingLocalNumber++
					break
				}
			}
			for _, card := range p.simCards {
				local := card.GetSIMData().GetSIMPayload()
				if local != nil && message.GetSomeInt() == int64(local.GetSIMNumber()) {
					incomingSomeIntMatch++
					break
				}
			}
			p.mu.RUnlock()
		}
	}
	keys := make([]string, 0, len(unknownFields))
	for key := range unknownFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("wire shape %s count=%d", key, unknownFields[key])
	}
	p.mu.RLock()
	count, settingsKnown := len(p.simCards), p.simSettingsKnown
	p.mu.RUnlock()
	t.Logf("incoming_sim_number_matching_local=%d outgoing_sim_number_matching_local=%d incoming_some_int_matching_local=%d", incomingLocalNumber, outgoingLocalNumber, incomingSomeIntMatch)
	t.Logf("lines=%d settings_received=%t incoming=%d outgoing=%d incoming_sender_payload=%d outgoing_sender_payload=%d incoming_payload_matching_local=%d", count, settingsKnown, incoming, outgoing, incomingPayload, outgoingPayload, incomingKnownLocalPayload)
}

// Inspect only undecoded wire fields; print field paths/counts, never raw values.
func summarizeUnknownWire(data []byte, path string, localIDs map[string]bool, counts map[string]int, depth int) {
	if depth > 3 {
		return
	}
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 || number < 1 {
			return
		}
		data = data[n:]
		size := protowire.ConsumeFieldValue(number, kind, data)
		if size < 0 {
			return
		}
		key := fmt.Sprintf("%s.%d/type%d", path, number, kind)
		counts[key]++
		if kind == protowire.VarintType {
			value, _ := protowire.ConsumeVarint(data)
			if localIDs[strconv.FormatUint(value, 10)] {
				counts[key+"/matches-local-id"]++
			}
		} else if kind == protowire.BytesType {
			value, _ := protowire.ConsumeBytes(data)
			if localIDs[string(value)] {
				counts[key+"/matches-local-id"]++
			}
			if validDiagnosticWire(value) {
				summarizeUnknownWire(value, key, localIDs, counts, depth+1)
			}
		}
		data = data[size:]
	}
}

func validDiagnosticWire(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	for len(data) > 0 {
		number, _, n := protowire.ConsumeField(data)
		if n < 0 || number < 1 || number > 128 {
			return false
		}
		data = data[n:]
	}
	return true
}

func summarizeUnknownMessage(message protoreflect.Message, path string, localIDs map[string]bool, counts map[string]int, depth int) {
	if depth > 6 {
		return
	}
	summarizeUnknownWire(message.GetUnknown(), path, localIDs, counts, 0)
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() != protoreflect.MessageKind {
			return true
		}
		nestedPath := path + "." + string(field.Name())
		if field.IsList() {
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				summarizeUnknownMessage(list.Get(i).Message(), nestedPath, localIDs, counts, depth+1)
			}
		} else if !field.IsMap() {
			summarizeUnknownMessage(value.Message(), nestedPath, localIDs, counts, depth+1)
		}
		return true
	})
}

func diagnosticUnknownString(data []byte, wanted protowire.Number) string {
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return ""
		}
		data = data[n:]
		size := protowire.ConsumeFieldValue(number, kind, data)
		if size < 0 {
			return ""
		}
		if number == wanted && kind == protowire.BytesType {
			value, _ := protowire.ConsumeBytes(data)
			return string(value)
		}
		data = data[size:]
	}
	return ""
}
func diagnosticPhoneDigits(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
}
