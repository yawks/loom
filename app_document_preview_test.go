package main

import (
	"strings"
	"testing"
)

func TestDocumentPreviews(t *testing.T) {
	email, err := parseEmailPreview([]byte("From: =?UTF-8?Q?Alice_Dupont?= <alice@example.com>\r\nTo: Bob <bob@example.com>\r\nSubject: =?UTF-8?Q?Bonjour_=C3=A0_tous?=\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nCorps du message"))
	if err != nil || email.Subject != "Bonjour à tous" || email.Body != "Corps du message" {
		t.Fatalf("email preview = %#v, %v", email, err)
	}

	contact, err := parseVCardPreview([]byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice Dupont\r\nORG:Acme\r\nTEL:+33123456789\r\nEMAIL:alice@example.com\r\nADR:;;1 rue de Paris;Paris;;75001;France\r\nEND:VCARD\r\n"))
	if err != nil || contact.Name != "Alice Dupont" || len(contact.Phones) != 1 || !strings.Contains(contact.Addresses[0], "Paris") {
		t.Fatalf("vCard preview = %#v, %v", contact, err)
	}
}
