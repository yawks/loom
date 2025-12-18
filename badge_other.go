//go:build !darwin

package main

import "log"

// setCgoDockBadge is a no-op on non-macOS platforms
func setCgoDockBadge(label string) {
	if label != "" {
		log.Printf("Badge update not supported on this platform (mocking badge: %s)", label)
	}
}
