package teams

import (
	"net/url"
	"strings"

	"go.mau.fi/mautrix-teams/pkg/msteams"
	"golang.org/x/net/html"
)

// Teams drops the image itemtype when copying GIFs into a forwarded message.
// Normalize these ordinary HTML images alongside the typed AMS attachments.
// Reply previews are excluded: their media belongs to the quoted message.
func teamsEmbeddedMedia(content string) ([]msteams.AMSAttachment, bool) {
	content = msteams.StripReplyBlockquote(content)
	attachments := msteams.ExtractAMSAttachments(content)
	root, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return attachments, false
	}
	forwarded := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			itemType := htmlAttribute(node, "itemtype")
			if node.Data == "blockquote" && strings.EqualFold(itemType, "http://schema.skype.com/Forward") {
				forwarded = true
			}
			if node.Data == "img" && itemType == "" {
				src := htmlAttribute(node, "src")
				parsed, err := url.Parse(src)
				if err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") {
					attachments = append(attachments, msteams.AMSAttachment{
						URL: src, AltText: firstNonEmpty(htmlAttribute(node, "alt"), "image"), IsImage: true,
					})
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return attachments, forwarded
}
