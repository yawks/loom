//go:build !darwin

package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func openFile(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
	case "linux":
		return exec.Command("xdg-open", path).Start()
	default:
		return fmt.Errorf("openFile: unsupported platform %s", runtime.GOOS)
	}
}
