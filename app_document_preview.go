package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	"github.com/emersion/go-vcard"
	"golang.org/x/net/html"
)

const maxDocumentPreviewBytes = 8 << 20

type DocumentPreview struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Subject      string   `json:"subject"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Date         string   `json:"date"`
	Body         string   `json:"body"`
	Organization string   `json:"organization"`
	Title        string   `json:"title"`
	Phones       []string `json:"phones"`
	Emails       []string `json:"emails"`
	Addresses    []string `json:"addresses"`
}

func (a *App) GetDocumentPreview(instanceID, source, kind string) (DocumentPreview, error) {
	dataURL, err := a.GetAttachmentData(source)
	if instanceID != "" {
		dataURL, err = a.GetProviderAttachmentData(instanceID, source)
	}
	if err != nil {
		return DocumentPreview{}, err
	}
	data, err := decodeDocumentDataURL(dataURL)
	if err != nil {
		return DocumentPreview{}, err
	}
	switch kind {
	case "eml":
		return parseEmailPreview(data)
	case "vcard":
		return parseVCardPreview(data)
	default:
		return DocumentPreview{}, fmt.Errorf("unsupported preview kind %q", kind)
	}
}

func decodeDocumentDataURL(dataURL string) ([]byte, error) {
	metadata, encoded, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasPrefix(metadata, "data:") || !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return nil, fmt.Errorf("invalid attachment data")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode attachment: %w", err)
	}
	if len(data) > maxDocumentPreviewBytes {
		return nil, fmt.Errorf("attachment is too large to preview")
	}
	return data, nil
}

func parseEmailPreview(data []byte) (DocumentPreview, error) {
	message, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return DocumentPreview{}, fmt.Errorf("parse email: %w", err)
	}
	decode := func(value string) string {
		decoded, err := new(mime.WordDecoder).DecodeHeader(value)
		if err == nil {
			return decoded
		}
		return value
	}
	body, err := emailText(message.Header.Get("Content-Type"), message.Header.Get("Content-Transfer-Encoding"), message.Body)
	if err != nil {
		return DocumentPreview{}, fmt.Errorf("parse email body: %w", err)
	}
	return DocumentPreview{Kind: "eml", Subject: decode(message.Header.Get("Subject")), From: decode(message.Header.Get("From")), To: decode(message.Header.Get("To")), Date: message.Header.Get("Date"), Body: strings.TrimSpace(body)}, nil
}

func emailText(contentType, transferEncoding string, body io.Reader) (string, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(body, params["boundary"])
		var fallback string
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return fallback, nil
			}
			if err != nil {
				return "", err
			}
			text, err := emailText(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part)
			if err != nil {
				continue
			}
			partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if partType == "text/plain" && strings.TrimSpace(text) != "" {
				return text, nil
			}
			if fallback == "" && strings.TrimSpace(text) != "" {
				fallback = text
			}
		}
	}
	var decoded io.Reader = body
	switch strings.ToLower(transferEncoding) {
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		decoded = quotedprintable.NewReader(body)
	}
	data, err := io.ReadAll(io.LimitReader(decoded, maxDocumentPreviewBytes+1))
	if err != nil {
		return "", err
	}
	if mediaType == "text/html" {
		return htmlText(data), nil
	}
	if mediaType == "" || mediaType == "text/plain" {
		return string(data), nil
	}
	return "", nil
}

func htmlText(data []byte) string {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) != "" {
			text.WriteString(strings.TrimSpace(node.Data))
			text.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return text.String()
}

func parseVCardPreview(data []byte) (DocumentPreview, error) {
	card, err := vcard.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return DocumentPreview{}, fmt.Errorf("parse vCard: %w", err)
	}
	addresses := make([]string, 0, len(card.Addresses()))
	for _, address := range card.Addresses() {
		parts := []string{address.StreetAddress, address.Locality, address.Region, address.PostalCode, address.Country}
		present := parts[:0]
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				present = append(present, part)
			}
		}
		if len(present) > 0 {
			addresses = append(addresses, strings.Join(present, ", "))
		}
	}
	return DocumentPreview{Kind: "vcard", Name: card.PreferredValue(vcard.FieldFormattedName), Organization: card.Value(vcard.FieldOrganization), Title: card.Value(vcard.FieldTitle), Phones: card.Values(vcard.FieldTelephone), Emails: card.Values(vcard.FieldEmail), Addresses: addresses}, nil
}
