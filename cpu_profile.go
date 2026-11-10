package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"time"
)

type cpuProfileConfig struct {
	path            string
	delay, duration time.Duration
}

// Profiling is opt-in and writes locally; no diagnostic HTTP server is exposed.
func diagnosticCPUProfileConfig(getenv func(string) string) (cpuProfileConfig, error) {
	c := cpuProfileConfig{path: getenv("LOOM_CPU_PROFILE"), delay: 2 * time.Minute, duration: 2 * time.Minute}
	if c.path == "" {
		return c, nil
	}
	for _, setting := range []struct {
		name      string
		value     *time.Duration
		allowZero bool
	}{
		{"LOOM_CPU_PROFILE_DELAY", &c.delay, true},
		{"LOOM_CPU_PROFILE_DURATION", &c.duration, false},
	} {
		if raw := getenv(setting.name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value < 0 || (!setting.allowZero && value == 0) {
				return c, fmt.Errorf("invalid %s: %q", setting.name, raw)
			}
			*setting.value = value
		}
	}
	return c, nil
}

func startDiagnosticCPUProfile(ready func() bool) func() {
	c, err := diagnosticCPUProfileConfig(os.Getenv)
	if err != nil {
		log.Printf("[CPUProfile] %v", err)
		return func() {}
	}
	if c.path == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		log.Printf("[CPUProfile] Waiting %s, then 30s without active synchronization", c.delay)
		timer := time.NewTimer(c.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// Startup synchronization must not be mistaken for idle work.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var idleSince time.Time
		for {
			if ready() {
				if idleSince.IsZero() {
					idleSince = time.Now()
				}
				if time.Since(idleSince) >= 30*time.Second {
					break
				}
			} else {
				idleSince = time.Time{}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		ticker.Stop()
		file, err := os.OpenFile(c.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			log.Printf("[CPUProfile] Create: %v", err)
			return
		}
		defer file.Close()
		if err := pprof.StartCPUProfile(file); err != nil {
			log.Printf("[CPUProfile] Start: %v", err)
			return
		}
		log.Printf("[CPUProfile] Recording %s to %s", c.duration, c.path)
		timer.Reset(c.duration)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		pprof.StopCPUProfile()
		log.Printf("[CPUProfile] Saved %s", c.path)
	}()
	return func() { cancel(); <-done }
}
