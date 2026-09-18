//go:build !darwin

package main

import "errors"

type SpeechLocale struct {
	Identifier   string `json:"identifier"`
	DisplayName  string `json:"displayName"`
	IsOnDevice   bool   `json:"isOnDevice"`
	IsDictation  bool   `json:"isDictation"`
	IsDefault    bool   `json:"isDefault"`
}

func speechTranscriptionAvailable() bool { return false }

func getSpeechTranscriptionLocales() []SpeechLocale { return nil }

func transcribeAudioFile(path string, localeID string, allowNetwork bool) (string, error) {
	return "", errors.New("on-device speech transcription is unavailable")
}

