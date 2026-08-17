package main

import "testing"

func TestLooksLikePhoneNumberLabel(t *testing.T) {
	tests := map[string]bool{
		"+33 6 50 40 12 44": true,
		"33650401244":       true,
		"(336) 504-01244":   true,
		"Johan":             false,
		"Studio 54":         false,
		"":                  false,
	}
	for value, expected := range tests {
		if got := looksLikePhoneNumberLabel(value); got != expected {
			t.Errorf("looksLikePhoneNumberLabel(%q) = %v, want %v", value, got, expected)
		}
	}
}
