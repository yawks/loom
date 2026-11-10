package main

import (
	"testing"
	"time"
)

func TestDiagnosticCPUProfileConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"disabled", map[string]string{"LOOM_CPU_PROFILE_DURATION": "invalid"}, false},
		{"defaults", map[string]string{"LOOM_CPU_PROFILE": "profile.pprof"}, false},
		{"custom", map[string]string{"LOOM_CPU_PROFILE": "profile.pprof", "LOOM_CPU_PROFILE_DELAY": "0s", "LOOM_CPU_PROFILE_DURATION": "90s"}, false},
		{"negative delay", map[string]string{"LOOM_CPU_PROFILE": "p", "LOOM_CPU_PROFILE_DELAY": "-1s"}, true},
		{"zero duration", map[string]string{"LOOM_CPU_PROFILE": "p", "LOOM_CPU_PROFILE_DURATION": "0s"}, true},
		{"invalid duration", map[string]string{"LOOM_CPU_PROFILE": "p", "LOOM_CPU_PROFILE_DURATION": "bad"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, err := diagnosticCPUProfileConfig(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if tt.name == "defaults" && (c.delay != 2*time.Minute || c.duration != 2*time.Minute) {
				t.Fatalf("unexpected defaults: %+v", c)
			}
			if tt.name == "custom" && (c.delay != 0 || c.duration != 90*time.Second) {
				t.Fatalf("unexpected overrides: %+v", c)
			}
		})
	}
}
