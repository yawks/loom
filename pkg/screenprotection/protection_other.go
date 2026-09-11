//go:build !windows && !darwin

package screenprotection

import "fmt"

func Supported() bool { return false }
func Limited() bool   { return false }
func set(enabled bool) error {
	if enabled {
		return fmt.Errorf("capture protection is unavailable on this operating system")
	}
	return nil
}
