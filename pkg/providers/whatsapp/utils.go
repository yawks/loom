package whatsapp

import (
	"strings"
)

func formatPhoneNumber(phoneNumber string) string {
	// Remove @s.whatsapp.net suffix if present
	cleaned := phoneNumber
	if strings.Contains(cleaned, "@s.whatsapp.net") {
		cleaned = strings.Split(cleaned, "@s.whatsapp.net")[0]
	}

	// Extract all digits
	digits := ""
	for _, r := range cleaned {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}

	if len(digits) == 0 {
		return phoneNumber
	}

	// Handle French numbers with +33 prefix
	// French numbers: +33 followed by 9 digits (without leading 0)
	// Format: +33 X XX XX XX XX
	if len(digits) >= 11 && digits[:2] == "33" {
		// Check if it's a valid French number (33 + 9 digits = 11 total)
		if len(digits) == 11 {
			countryCode := digits[:2]
			rest := digits[2:]
			// Format as +33 X XX XX XX XX
			formatted := "+" + countryCode + " " + rest[:1] + " " + rest[1:3] + " " + rest[3:5] + " " + rest[5:7] + " " + rest[7:]
			return formatted
		}
		// If longer, might be international format, format 2 by 2
	}

	// For other numbers, format 2 digits at a time
	formatted := ""
	for i := 0; i < len(digits); i += 2 {
		if i > 0 {
			formatted += " "
		}
		if i+2 <= len(digits) {
			formatted += digits[i : i+2]
		} else {
			formatted += digits[i:]
		}
	}

	return formatted
}

func isPhoneNumber(str string) bool {
	if str == "" {
		return false
	}
	// Remove spaces and common phone formatting characters
	cleaned := strings.ReplaceAll(str, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "(", "")
	cleaned = strings.ReplaceAll(cleaned, ")", "")
	cleaned = strings.ReplaceAll(cleaned, "+", "")

	// Count digits
	digitCount := 0
	for _, r := range cleaned {
		if r >= '0' && r <= '9' {
			digitCount++
		}
	}

	// Consider it a phone number if at least 8 digits and mostly digits
	return digitCount >= 8 && float64(digitCount)/float64(len(cleaned)) > 0.7
}

// markUnused is a helper to silence static analysis warnings for stub implementations.
func markUnused(values ...interface{}) {
	for _, v := range values {
		_ = v
	}
}
