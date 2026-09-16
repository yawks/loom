package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestExtractLinkPreview(t *testing.T) {
	cases := []struct{ name, body, title, description, image string }{
		{"priority and relative image", `<meta name="twitter:title" content="Twitter"><meta property="og:title" content="Article"><meta property="og:image" content="../hero.jpg"><meta property="og:image" content="second.jpg"><meta property="og:url" content="javascript:alert(1)">`, "Article", "", "https://example.com/hero.jpg"},
		{"base and property twitter", `<base href="/assets/"><meta property="twitter:title" content="Title"><meta property="twitter:image" content="hero.jpg">`, "Title", "", "https://example.com/assets/hero.jpg"},
		{"article fallback", `<main><h1>Article heading</h1><p>This is a useful article description with enough detail to make the link preview informative.</p><img data-src="/cover.jpg"></main>`, "Article heading", "This is a useful article description with enough detail to make the link preview informative.", "https://example.com/cover.jpg"},
		{"unsafe image", `<meta property="og:image" content="javascript:alert(1)"><title>Safe</title>`, "Safe", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse("https://example.com/posts/page")
			got := extractLinkPreview(doc, u)
			if got.Title != tc.title || got.Description != tc.description || got.ImageURL != tc.image || got.URL != u.String() {
				t.Fatalf("unexpected preview: %+v", got)
			}
		})
	}
}

func TestFetchLinkPreviewRedirectImageAndCache(t *testing.T) {
	imageRequests := 0
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/posts/page", http.StatusFound)
		case "/posts/page":
			w.Header().Set("Content-Type", "text/html; charset=windows-1252")
			_, _ = w.Write([]byte("<title>Caf\xe9</title><meta property=og:image content=hero.png>"))
		case "/posts/hero.png":
			imageRequests++
			if !strings.HasSuffix(r.Referer(), "/posts/page") {
				t.Error("missing page referer")
			}
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app := &App{}
	for i := 0; i < 2; i++ {
		got, err := app.FetchLinkPreview(server.URL + "/start")
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "Café" || got.URL != server.URL+"/posts/page" || !strings.HasPrefix(got.ImageURL, "data:image/png;base64,") {
			t.Fatalf("unexpected preview: %+v", got)
		}
	}
	if imageRequests != 1 {
		t.Fatalf("image fetched %d times", imageRequests)
	}
}

func TestLinkPreviewFaviconBase(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(`<base href="/theme/"><link rel="shortcut icon" href="brand.svg">`))
	u, _ := url.Parse("https://login.example.com/auth")
	if got := extractLinkPreview(doc, u).FaviconURL; got != "https://login.example.com/theme/brand.svg" {
		t.Fatalf("favicon = %q", got)
	}
}
