package googlechat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"Loom/pkg/db"
	"Loom/pkg/models"
)

// GetContacts returns the list of DMs and Spaces visible to the authenticated user.
func (p *GoogleChatProvider) GetContacts() ([]models.LinkedAccount, error) {
	if p.getHTTPClient() == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Pre-populate user cache from Workspace directory (DOMAIN_PROFILE data).
	p.loadDirectoryPeople()

	var spaces []Space
	pageToken := ""
	for {
		params := url.Values{"pageSize": {"100"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var resp SpaceListResponse
		if err := p.apiGet("/spaces", params, &resp); err != nil {
			return nil, err
		}
		spaces = append(spaces, resp.Spaces...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	p.log("GoogleChatProvider.GetContacts: found %d spaces\n", len(spaces))

	instanceID := p.getInstanceID()
	selfID := p.getSelfID()
	var accounts []models.LinkedAccount

	for _, space := range spaces {
		isDM := space.SpaceType == "DIRECT_MESSAGE" || space.Type == "DM"
		displayName := space.DisplayName
		avatarURL := ""
		userID := space.Name

		p.log("GoogleChatProvider.GetContacts: space=%s type=%s spaceType=%s displayName=%q isDM=%v\n",
			space.Name, space.Type, space.SpaceType, space.DisplayName, isDM)

		if isDM {
			other := p.getDMOtherUser(space.Name, selfID)
			p.log("GoogleChatProvider.GetContacts: DM other=%+v\n", other)
			if other != nil {
				displayName = other.DisplayName
				avatarURL = other.AvatarUrl
				if id := strings.TrimPrefix(other.Name, "users/"); id != "" {
					userID = id
				}
			}
		}
		if displayName == "" {
			displayName = space.Name
		}

		acct := models.LinkedAccount{
			Protocol:           "googlechat",
			ProviderInstanceID: instanceID,
			UserID:             userID,
			Username:           displayName,
			AvatarURL:          avatarURL,
			IsGroup:            !isDM,
			ConversationID:     space.Name,
		}
		accounts = append(accounts, acct)
		p.persistContact(acct, space.Name, !isDM)
	}

	return accounts, nil
}

// loadDirectoryPeople pre-populates the user cache from the Workspace directory.
// people.get only returns public PROFILE data; listDirectoryPeople returns DOMAIN_PROFILE.
func (p *GoogleChatProvider) loadDirectoryPeople() {
	client := p.getHTTPClient()
	if client == nil {
		return
	}

	pageToken := ""
	loaded := 0
	for {
		urlStr := "https://people.googleapis.com/v1/people:listDirectoryPeople" +
			"?readMask=names,photos,emailAddresses" +
			"&sources=DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE" +
			"&pageSize=1000"
		if pageToken != "" {
			urlStr += "&pageToken=" + url.QueryEscape(pageToken)
		}

		resp, err := client.Get(urlStr)
		if err != nil {
			p.log("GoogleChatProvider.loadDirectoryPeople: %v\n", err)
			return
		}
		if resp.StatusCode != 200 {
			p.log("GoogleChatProvider.loadDirectoryPeople: HTTP %d\n", resp.StatusCode)
			resp.Body.Close()
			return
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		p.log("GoogleChatProvider.loadDirectoryPeople: raw=%s\n", string(body))

		var result struct {
			People []struct {
				ResourceName   string `json:"resourceName"`
				Names          []struct{ DisplayName string `json:"displayName"` } `json:"names"`
				Photos         []struct{ URL string `json:"url"` }                `json:"photos"`
				EmailAddresses []struct{ Value string `json:"value"` }            `json:"emailAddresses"`
			} `json:"people"`
			NextPageToken string `json:"nextPageToken"`
		}
		if json.Unmarshal(body, &result) != nil {
			return
		}

		p.userMu.Lock()
		for _, person := range result.People {
			userID := strings.TrimPrefix(person.ResourceName, "people/")
			if userID == "" {
				continue
			}
			name := ""
			if len(person.Names) > 0 {
				name = person.Names[0].DisplayName
			}
			if name == "" && len(person.EmailAddresses) > 0 {
				email := person.EmailAddresses[0].Value
				if at := strings.Index(email, "@"); at > 0 {
					name = email[:at]
				} else {
					name = email
				}
			}
			avatarURL := ""
			if len(person.Photos) > 0 {
				avatarURL = person.Photos[0].URL
			}
			if name != "" {
				p.userCache[userID] = cachedUser{Name: name, AvatarURL: avatarURL}
				loaded++
			}
		}
		p.userMu.Unlock()

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	p.log("GoogleChatProvider.loadDirectoryPeople: cached %d users\n", loaded)
}

// getDMOtherUser returns the other participant in a DM space (not selfID).
func (p *GoogleChatProvider) getDMOtherUser(spaceName, selfID string) *ChatUser {
	var resp MemberListResponse
	if err := p.apiGet("/"+spaceName+"/members", url.Values{"pageSize": {"50"}}, &resp); err != nil {
		return nil
	}
	for _, m := range resp.Memberships {
		if m.Member == nil {
			continue
		}
		memberID := strings.TrimPrefix(m.Member.Name, "users/")
		if memberID != selfID {
			if m.Member.DisplayName == "" {
				m.Member.DisplayName, m.Member.AvatarUrl = p.resolveUserInfo(memberID)
			}
			// People API didn't return a name — fall back to message history.
			if m.Member.DisplayName == memberID {
				if name, avatar := p.getNameFromMessages(spaceName, selfID); name != "" {
					m.Member.DisplayName = name
					if m.Member.AvatarUrl == "" && avatar != "" {
						m.Member.AvatarUrl = avatar
					}
				}
			}
			return m.Member
		}
	}
	return nil
}

// getNameFromMessages fetches recent messages in a space and returns the first
// non-self sender's display name and avatar.
func (p *GoogleChatProvider) getNameFromMessages(spaceName, selfID string) (name, avatarURL string) {
	params := url.Values{"pageSize": {"10"}, "orderBy": {"createTime desc"}}
	var resp MessageListResponse
	if err := p.apiGet("/"+spaceName+"/messages", params, &resp); err != nil {
		p.log("GoogleChatProvider.getNameFromMessages: %s error: %v\n", spaceName, err)
		return "", ""
	}
	p.log("GoogleChatProvider.getNameFromMessages: %s → %d messages\n", spaceName, len(resp.Messages))
	for _, msg := range resp.Messages {
		if msg.Sender == nil || msg.DeleteTime != nil {
			continue
		}
		senderID := strings.TrimPrefix(msg.Sender.Name, "users/")
		p.log("GoogleChatProvider.getNameFromMessages: sender=%s name=%q\n", senderID, msg.Sender.DisplayName)
		if senderID != selfID && msg.Sender.DisplayName != "" {
			return msg.Sender.DisplayName, msg.Sender.AvatarUrl
		}
	}
	return "", ""
}

// resolveUserInfo fetches a user's display name and avatar from the People API.
func (p *GoogleChatProvider) resolveUserInfo(userID string) (name, avatarURL string) {
	client := p.getHTTPClient()
	if client == nil {
		return userID, ""
	}

	p.userMu.RLock()
	cached, ok := p.userCache[userID]
	p.userMu.RUnlock()
	if ok && cached.Name != "" {
		return cached.Name, cached.AvatarURL
	}

	resp, err := client.Get("https://people.googleapis.com/v1/people/" + userID + "?personFields=names,photos,emailAddresses")
	if err != nil {
		p.log("GoogleChatProvider.resolveUserInfo: %s fetch error: %v\n", userID, err)
		return userID, ""
	}
	if resp.StatusCode != 200 {
		p.log("GoogleChatProvider.resolveUserInfo: %s → HTTP %d\n", userID, resp.StatusCode)
		resp.Body.Close()
		return userID, ""
	}
	defer resp.Body.Close()

	var result struct {
		Names []struct {
			DisplayName string `json:"displayName"`
		} `json:"names"`
		Photos []struct {
			URL string `json:"url"`
		} `json:"photos"`
		EmailAddresses []struct {
			Value string `json:"value"`
		} `json:"emailAddresses"`
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	p.log("GoogleChatProvider.resolveUserInfo: %s → raw=%s\n", userID, string(bodyBytes))
	if json.Unmarshal(bodyBytes, &result) != nil {
		return userID, ""
	}

	name = userID
	if len(result.Names) > 0 && result.Names[0].DisplayName != "" {
		name = result.Names[0].DisplayName
	} else if len(result.EmailAddresses) > 0 && result.EmailAddresses[0].Value != "" {
		email := result.EmailAddresses[0].Value
		if at := strings.Index(email, "@"); at > 0 {
			name = email[:at]
		} else {
			name = email
		}
		p.log("GoogleChatProvider.resolveUserInfo: %s → no name, using email prefix %q\n", userID, name)
	} else {
		// People API returned nothing useful — show a short suffix of the numeric ID.
		suffix := userID
		if len(userID) > 6 {
			suffix = userID[len(userID)-6:]
		}
		name = "Chat …" + suffix
		p.log("GoogleChatProvider.resolveUserInfo: %s → no name and no email, fallback=%q\n", userID, name)
	}
	if len(result.Photos) > 0 {
		avatarURL = result.Photos[0].URL
	}

	p.userMu.Lock()
	p.userCache[userID] = cachedUser{Name: name, AvatarURL: avatarURL}
	p.userMu.Unlock()
	return
}

// GetContactName resolves a user ID to a display name from cache.
func (p *GoogleChatProvider) GetContactName(contactID string) (string, error) {
	p.userMu.RLock()
	u, ok := p.userCache[contactID]
	p.userMu.RUnlock()
	if ok {
		return u.Name, nil
	}
	return contactID, nil
}

// RefreshContact is a no-op for Google Chat REST API.
func (p *GoogleChatProvider) RefreshContact(contactID string) error {
	return nil
}

// persistContact ensures LinkedAccount and Conversation rows exist in the DB.
func (p *GoogleChatProvider) persistContact(acct models.LinkedAccount, convID string, isGroup bool) {
	if db.DB == nil {
		return
	}
	instanceID := p.getInstanceID()

	var meta models.MetaContact
	db.DB.Where("id IN (SELECT meta_contact_id FROM linked_accounts WHERE provider_instance_id = ? AND user_id = ?)",
		instanceID, acct.UserID).First(&meta)
	if meta.ID == 0 {
		meta = models.MetaContact{DisplayName: acct.Username, AvatarURL: acct.AvatarURL}
		db.DB.Create(&meta)
		db.ContactStore.UpsertMetaContact(meta)
	} else if meta.DisplayName != acct.Username || meta.AvatarURL != acct.AvatarURL {
		meta.DisplayName = acct.Username
		meta.AvatarURL = acct.AvatarURL
		db.DB.Save(&meta)
		db.ContactStore.UpsertMetaContact(meta)
	}

	var linkedAccount models.LinkedAccount
	db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, acct.UserID).First(&linkedAccount)
	if linkedAccount.ID == 0 {
		linkedAccount = models.LinkedAccount{
			MetaContactID:      meta.ID,
			Protocol:           "googlechat",
			ProviderInstanceID: instanceID,
			UserID:             acct.UserID,
			Username:           acct.Username,
			AvatarURL:          acct.AvatarURL,
			IsGroup:            isGroup,
		}
		db.DB.Create(&linkedAccount)
		db.ContactStore.UpsertLinkedAccount(linkedAccount)
	} else if linkedAccount.Username != acct.Username || linkedAccount.AvatarURL != acct.AvatarURL {
		linkedAccount.Username = acct.Username
		linkedAccount.AvatarURL = acct.AvatarURL
		db.DB.Save(&linkedAccount)
		db.ContactStore.UpsertLinkedAccount(linkedAccount)
	}

	// For DMs we now have the real userID (e.g. "115390…"). If a stale LinkedAccount
	// was previously saved with userID = convID (e.g. "spaces/-zrGTi…") because
	// getDMOtherUser failed on the first sync, delete it.
	if !isGroup && acct.UserID != convID {
		var stale models.LinkedAccount
		db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, convID).First(&stale)
		if stale.ID != 0 {
			db.DB.Delete(&stale)
			db.ContactStore.DeleteLinkedAccount(stale.ID)
		}
	}

	expectedGroupName := ""
	if isGroup {
		expectedGroupName = acct.Username
	}

	var conv models.Conversation
	db.DB.Where("protocol_conv_id = ?", convID).First(&conv)
	if conv.ID == 0 {
		conv = models.Conversation{
			LinkedAccountID: linkedAccount.ID,
			ProtocolConvID:  convID,
			IsGroup:         isGroup,
			GroupName:       expectedGroupName,
		}
		db.DB.Create(&conv)
		db.ContactStore.UpsertConversation(linkedAccount.ID, convID)
	} else {
		needsSave := false
		if conv.GroupName != expectedGroupName || conv.IsGroup != isGroup {
			conv.GroupName = expectedGroupName
			conv.IsGroup = isGroup
			needsSave = true
		}
		if conv.LinkedAccountID != linkedAccount.ID {
			conv.LinkedAccountID = linkedAccount.ID
			needsSave = true
		}
		if needsSave {
			db.DB.Save(&conv)
			db.ContactStore.SetConversation(linkedAccount.ID, convID)
		}
	}
}
