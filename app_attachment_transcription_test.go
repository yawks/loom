package main

import (
	"Loom/pkg/models"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMacBundlesDeclareSpeechRecognitionUsage(t *testing.T) {
	for _, path := range []string{"build/darwin/Info.dev.plist", "build/darwin/Info.plist"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "<key>NSSpeechRecognitionUsageDescription</key>") {
			t.Errorf("%s is missing NSSpeechRecognitionUsageDescription", path)
		}
	}
}

func TestSpeechTranscriptionIsDisabledUnderWailsDev(t *testing.T) {
	t.Setenv("devserver", "localhost:34115")
	if speechTranscriptionAvailable() {
		t.Fatal("speech transcription must be disabled under wails dev")
	}
}

func TestSplitTranscriptionWAV(t *testing.T) {
	const sampleRate = 16000
	const byteRate = sampleRate * 2
	// 40 seconds of audio
	audio := make([]byte, 44+byteRate*40)
	copy(audio[:4], "RIFF")
	copy(audio[8:12], "WAVE")
	copy(audio[12:16], "fmt ")
	binary.LittleEndian.PutUint32(audio[16:20], 16)
	binary.LittleEndian.PutUint16(audio[20:22], 1)
	binary.LittleEndian.PutUint16(audio[22:24], 1)
	binary.LittleEndian.PutUint32(audio[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(audio[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(audio[32:34], 2)
	binary.LittleEndian.PutUint16(audio[34:36], 16)
	copy(audio[36:40], "data")
	binary.LittleEndian.PutUint32(audio[4:8], uint32(len(audio)-8))
	binary.LittleEndian.PutUint32(audio[40:44], uint32(len(audio)-44))

	chunks, err := splitTranscriptionWAV(audio)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("chunks = %d, want at least 3 for 40s audio", len(chunks))
	}
	for _, chunk := range chunks {
		chunkDataSize := int(binary.LittleEndian.Uint32(chunk[40:44]))
		chunkDuration := float64(chunkDataSize) / float64(byteRate)
		if chunkDuration > 13.0 {
			t.Fatalf("chunk duration = %.2f, want <= 13.0", chunkDuration)
		}
		if got := binary.LittleEndian.Uint32(chunk[40:44]); got != uint32(len(chunk)-44) {
			t.Fatalf("WAV data size = %d, want %d", got, len(chunk)-44)
		}
	}
}

func TestPersistAttachmentTranscription(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:attachment-transcription?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	message := models.Message{
		ProtocolMsgID: "remote-message",
		Attachments:   `[{"type":"voice","url":"voice-one","fileName":"voice.ogg","fileSize":12,"mimeType":"audio/ogg"}]`,
	}
	if err := database.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := persistAttachmentTranscription(database, "remote-message", "voice-one", "Bonjour tout le monde."); err != nil {
		t.Fatal(err)
	}

	var stored models.Message
	if err := database.First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(stored.Attachments), &attachments); err != nil {
		t.Fatal(err)
	}
	if attachments[0].Transcription != "Bonjour tout le monde." {
		t.Fatalf("transcription = %q", attachments[0].Transcription)
	}
}

func TestSpeechTranscriptionLocales(t *testing.T) {
	app := &App{}
	locales := app.GetSpeechTranscriptionLocales()
	for _, loc := range locales {
		if loc.Identifier == "" {
			t.Errorf("empty locale identifier found")
		}
		if loc.DisplayName == "" {
			t.Errorf("empty display name for %s", loc.Identifier)
		}
	}
}


