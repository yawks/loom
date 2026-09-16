package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRenderLinkPreviewClientRedirect(t *testing.T) {
	if os.Getenv("LOOM_TEST_BROWSER") != "1" {
		t.Skip("requires installed Chromium and permission to launch it")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<script>setTimeout(() => location.href='/login', 200)</script>`))
		} else {
			_, _ = w.Write([]byte(`<title>Example login</title><link rel="icon" href="/brand.svg"><body style="background:purple;color:white"><h1>Please sign in to Example</h1></body>`))
		}
	}))
	defer server.Close()
	got, err := renderLinkPreview(context.Background(), server.URL+"/#/conversation/123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Example login" || got.FaviconURL != server.URL+"/brand.svg" || !strings.HasPrefix(got.ImageURL, "data:image/jpeg;base64,") || got.URL != server.URL+"/#/conversation/123" {
		t.Fatalf("unexpected render: title=%q icon=%q url=%q", got.Title, got.FaviconURL, got.URL)
	}
}

func TestRenderLinkPreviewLive(t *testing.T) {
	target := os.Getenv("LOOM_PREVIEW_TEST_URL")
	if target == "" {
		t.Skip("optional live visual verification")
	}
	got, err := renderLinkPreview(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got.ImageURL, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/loom-preview-render.jpg", data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("title=%q favicon=%q; screenshot saved to /tmp/loom-preview-render.jpg", got.Title, got.FaviconURL)
}

func TestRenderLinkPreviewRejectsHTTPErrors(t *testing.T) {
	if os.Getenv("LOOM_TEST_BROWSER") != "1" {
		t.Skip("requires Chromium")
	}
	for _, status := range []int{403, 404} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("This page cannot be displayed"))
		}))
		_, err := renderLinkPreview(context.Background(), server.URL)
		server.Close()
		if err == nil {
			t.Fatalf("HTTP %d was captured", status)
		}
	}
}
