package logging

import "testing"

func TestProviderConsoleLoggingEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "true", value: "true", want: true},
		{name: "one", value: "1", want: true},
		{name: "false", value: "false", want: false},
		{name: "invalid", value: "verbose", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(providerConsoleLogEnv, tt.value)
			if got := providerConsoleLoggingEnabled(); got != tt.want {
				t.Fatalf("providerConsoleLoggingEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
