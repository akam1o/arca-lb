package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// ExtractAPIKey applies the controller API key extraction rules shared by REST and gRPC.
func ExtractAPIKey(authorizationValues, apiKeyValues []string) string {
	if len(authorizationValues) > 0 {
		if len(authorizationValues) != 1 {
			return ""
		}
		fields := strings.Fields(authorizationValues[0])
		if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
			return fields[1]
		}
		return ""
	}
	if len(apiKeyValues) != 1 {
		return ""
	}
	return strings.TrimSpace(apiKeyValues[0])
}

// APIKeyMatches compares API keys without leaking early mismatch timing.
func APIKeyMatches(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}
