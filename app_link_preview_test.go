package main

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestWalkHTMLForJSONLDFillsMissingPreviewFields(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(`<html><head>
		<script type="application/ld+json">{
			"@context":"https://schema.org", "@type":"Product",
			"name":"Montessori shelf", "description":"Five compartments",
			"image":[{"contentUrl":"https://cdn.example.com/shelf.jpg"}]
		}</script>
	</head></html>`))
	if err != nil {
		t.Fatal(err)
	}

	preview := LinkPreview{}
	walkHTMLForJSONLD(doc, &preview)

	if preview.Title != "Montessori shelf" || preview.Description != "Five compartments" || preview.ImageURL != "https://cdn.example.com/shelf.jpg" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
}

func TestWalkHTMLForJSONLDDoesNotOverrideOpenGraph(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(`<html><head>
		<script type="application/ld+json">{"name":"JSON title","image":"https://example.com/json.jpg"}</script>
	</head></html>`))
	if err != nil {
		t.Fatal(err)
	}

	preview := LinkPreview{Title: "OG title", ImageURL: "https://example.com/og.jpg"}
	walkHTMLForJSONLD(doc, &preview)

	if preview.Title != "OG title" || preview.ImageURL != "https://example.com/og.jpg" {
		t.Fatalf("JSON-LD overrode existing metadata: %+v", preview)
	}
}

func TestWalkHTMLForJSONLDReadsGraph(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(`<script type="application/ld+json">{
		"@graph":[{"@type":"WebSite","name":"Store"},{"@type":"Product","image":{"url":"https://example.com/product.jpg"}}]
	}</script>`))
	if err != nil {
		t.Fatal(err)
	}

	preview := LinkPreview{Title: "Page title"}
	walkHTMLForJSONLD(doc, &preview)

	if preview.ImageURL != "https://example.com/product.jpg" {
		t.Fatalf("image = %q", preview.ImageURL)
	}
}
