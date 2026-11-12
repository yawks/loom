package teams

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func TestDownloadTeamsSharedFileFallsBackToShareURL(t *testing.T) {
	file := msteams.SharedFile{ItemID: "01QBJ7Y36VFOLMJQIMRVGLNDDK7XWRTGJC", ShareURL: "https://example.sharepoint.com/:u:/file"}
	data, mime, err := downloadTeamsSharedFile(context.Background(), file,
		func(context.Context, msteams.SharedFile) ([]byte, string, error) {
			return nil, "", errors.New("400 badArgument")
		},
		func(_ context.Context, link string) ([]byte, string, error) {
			if link != file.ShareURL {
				t.Fatalf("unexpected URL %q", link)
			}
			return []byte("audio"), "audio/wav", nil
		})
	if err != nil || string(data) != "audio" || mime != "audio/wav" {
		t.Fatalf("got %q, %q, %v", data, mime, err)
	}
}

func TestDownloadTeamsSharedFilePreservesErrors(t *testing.T) {
	file := msteams.SharedFile{ShareURL: "https://example.sharepoint.com/file"}
	_, _, err := downloadTeamsSharedFile(context.Background(), file,
		func(context.Context, msteams.SharedFile) ([]byte, string, error) {
			return nil, "", errors.New("direct failed")
		},
		func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("link failed") })
	if err == nil || !strings.Contains(err.Error(), "direct failed") || !strings.Contains(err.Error(), "link failed") {
		t.Fatalf("expected both errors, got %v", err)
	}
}

func TestDownloadTeamsSharedFileConvertsDriveItemID(t *testing.T) {
	// Microsoft Graph's documented driveItem example pairs this ID and GUID.
	file := msteams.SharedFile{ItemID: "01NKDM7HMOJTVYMDOSXFDK2QJDXCDI3WUK"}
	calls := 0
	data, _, err := downloadTeamsSharedFile(context.Background(), file,
		func(_ context.Context, f msteams.SharedFile) ([]byte, string, error) {
			calls++
			if calls == 1 {
				return nil, "", errors.New("400 badArgument")
			}
			if f.ItemID != "86eb4c8e-d20d-46b9-ad41-23b8868dda8a" {
				t.Fatalf("unexpected GUID: %s", f.ItemID)
			}
			return []byte("wav"), "audio/wav", nil
		},
		func(context.Context, string) ([]byte, string, error) {
			t.Fatal("unexpected link request")
			return nil, "", nil
		})
	if err != nil || string(data) != "wav" || calls != 2 {
		t.Fatalf("got %q, %v, %d calls", data, err, calls)
	}
}
