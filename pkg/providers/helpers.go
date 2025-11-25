// Package providers contains provider implementations and helpers.
package providers

// markUnused is a helper to silence static analysis warnings for stub implementations.
func markUnused(values ...interface{}) {
	for _, v := range values {
		_ = v
	}
}
