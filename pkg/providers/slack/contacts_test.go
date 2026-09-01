package slack

import "testing"

func TestMPIMNeedsNameResolution(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"", true},
		{"Group Chat", true},
		{"mpdm-alice--bob-1", true},
		{"Alice, Bob", false},
	}

	for _, test := range tests {
		if got := mpimNeedsNameResolution(test.name); got != test.want {
			t.Errorf("mpimNeedsNameResolution(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}
