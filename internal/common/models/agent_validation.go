package models

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateAgentID validates an agent identity used in config, RPCs, and status.
func ValidateAgentID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return fmt.Errorf("%s must not contain whitespace or control characters", field)
	}
	return nil
}
