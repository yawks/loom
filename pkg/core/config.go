// Package core provides the core interfaces and types for chat providers.
package core

// ProviderConfig represents the configuration for a provider.
// The configuration is stored as key-value pairs, allowing each provider
// to define its own configuration schema.
type ProviderConfig map[string]interface{}

// GetString returns a string value from the configuration.
func (c ProviderConfig) GetString(key string) (string, bool) {
	val, ok := c[key]
	if !ok {
		return "", false
	}
	str, ok := val.(string)
	return str, ok
}

// GetInt returns an int value from the configuration.
func (c ProviderConfig) GetInt(key string) (int, bool) {
	val, ok := c[key]
	if !ok {
		return 0, false
	}
	// Handle both int and float64 (from JSON unmarshaling)
	switch v := val.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// GetBool returns a bool value from the configuration.
func (c ProviderConfig) GetBool(key string) (bool, bool) {
	val, ok := c[key]
	if !ok {
		return false, false
	}
	b, ok := val.(bool)
	return b, ok
}

// Set sets a value in the configuration.
func (c ProviderConfig) Set(key string, value interface{}) {
	c[key] = value
}


