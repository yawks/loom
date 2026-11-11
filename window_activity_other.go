//go:build !darwin

package main

// Other platforms use browser focus/visibility events without native polling.
func systemWindowActivityEvents() <-chan bool { return nil }
