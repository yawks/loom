package main

import (
	"Loom/pkg/models"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestContactInlineAvatarsAreLazyAndProviderNeutral(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	source := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	app := &App{avatarReferences: avatarReferenceCache{directory: t.TempDir()}}
	contacts := []models.MetaContact{{AvatarURL: source, LinkedAccounts: []models.LinkedAccount{
		{ProviderInstanceID: "one", AvatarURL: source},
		{ProviderInstanceID: "two", AvatarURL: "https://example.test/avatar"},
	}}}
	app.compactContactAvatars(contacts)
	path := contacts[0].AvatarURL
	if path == source || strings.HasPrefix(path, "data:") || path != contacts[0].LinkedAccounts[0].AvatarURL {
		t.Fatal("inline avatars were not replaced by one stable reference")
	}
	if contacts[0].LinkedAccounts[1].AvatarURL != "https://example.test/avatar" {
		t.Fatal("changed a remote avatar")
	}
	if app.GetAvatar(path) != source {
		t.Fatal("lazy avatar bytes or MIME type changed")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("avatar cache permissions: %v, %v", info, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := app.avatarReferences.compact(source); got != path || app.GetAvatar(got) != source {
		t.Fatal("evicted cache file did not regenerate")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected temporary files: %v, %v", entries, err)
	}
}

func TestAvatarReferenceCachePreservesInvalidFormatsAndConcurrentReaders(t *testing.T) {
	cache := avatarReferenceCache{directory: t.TempDir()}
	for _, source := range []string{"", "/local/avatar.jpg", "https://example.test/a", "data:image/png;base64,bad!", "data:image/svg+xml,<svg/>", "data:text/plain;base64,YQ=="} {
		if got := cache.compact(source); got != source {
			t.Fatalf("changed unsupported avatar %q", source)
		}
	}
	source := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("image bytes"))
	var wg sync.WaitGroup
	paths := make(chan string, 16)
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); paths <- cache.compact(source) }()
	}
	wg.Wait()
	close(paths)
	want := cache.compact(source)
	for got := range paths {
		if got != want {
			t.Fatal("concurrent callers received different paths")
		}
	}
}

func TestCompactContactAvatarPayloadReduction(t *testing.T) {
	source := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 2*1024*1024))
	contacts := []models.MetaContact{{AvatarURL: source, LinkedAccounts: []models.LinkedAccount{{AvatarURL: source}}}}
	before, _ := json.Marshal(contacts)
	app := &App{avatarReferences: avatarReferenceCache{directory: t.TempDir()}}
	app.compactContactAvatars(contacts)
	after, _ := json.Marshal(contacts)
	if len(after)*100 >= len(before) {
		t.Fatalf("insufficient payload reduction: %d -> %d", len(before), len(after))
	}
	t.Logf("contact bridge payload: %d -> %d bytes", len(before), len(after))
}
