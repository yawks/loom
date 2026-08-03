//go:build !darwin

package main

func systemSessionUnlockEvents() <-chan struct{} {
	return nil
}
