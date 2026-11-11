package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Bridge diagnostics record names, counts, timings and aggregate string sizes,
// never arguments, message content, identifiers or credentials. No timers or
// response traversal are installed in the frontend unless explicitly enabled.
func (a *App) GetBridgeDiagnosticsEnabled() bool {
	return os.Getenv("LOOM_BRIDGE_DIAGNOSTICS") == "1"
}

func (a *App) setBridgeDiagnosticsRecording(active bool) {
	if a.GetBridgeDiagnosticsEnabled() && a.ctx != nil {
		if active {
			log.Printf("[CPUProfile] Providers currently synchronizing: %v", a.GetSyncingProviderIDs())
		}
		runtime.EventsEmit(a.ctx, "diagnostics:bridge", active)
	}
}

type bridgeDiagnosticMetric struct {
	Name           string  `json:"name"`
	Calls          int     `json:"calls"`
	Errors         int     `json:"errors"`
	StringChars    int64   `json:"stringChars"`
	MaxStringChars int64   `json:"maxStringChars"`
	ElapsedMS      float64 `json:"elapsedMs"`
}

func (a *App) ReportBridgeDiagnostics(payload string) {
	if !a.GetBridgeDiagnosticsEnabled() || len(payload) > 128*1024 {
		return
	}
	var metrics []bridgeDiagnosticMetric
	if err := json.Unmarshal([]byte(payload), &metrics); err != nil || len(metrics) > 512 {
		return
	}
	for _, metric := range metrics {
		if len(metric.Name) > 128 {
			continue
		}
		data, err := json.Marshal(metric)
		if err == nil {
			log.Printf("[BridgeDiagnostics] %s", data)
		}
	}
	log.Printf("[BridgeDiagnostics] end metrics=%d", len(metrics))
}
