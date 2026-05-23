package auth

import "testing"

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name                string
		authorizationValues []string
		apiKeyValues        []string
		want                string
	}{
		{
			name:                "bearer authorization",
			authorizationValues: []string{"Bearer controller-secret"},
			apiKeyValues:        []string{"ignored-secret"},
			want:                "controller-secret",
		},
		{
			name:         "x api key fallback",
			apiKeyValues: []string{" controller-secret "},
			want:         "controller-secret",
		},
		{
			name:                "multiple authorization values rejected",
			authorizationValues: []string{"Bearer controller-secret", "Bearer other"},
			want:                "",
		},
		{
			name:                "malformed authorization rejects fallback",
			authorizationValues: []string{"Basic controller-secret"},
			apiKeyValues:        []string{"controller-secret"},
			want:                "",
		},
		{
			name:         "multiple x api key values rejected",
			apiKeyValues: []string{"controller-secret", "other"},
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractAPIKey(tt.authorizationValues, tt.apiKeyValues)
			if got != tt.want {
				t.Fatalf("ExtractAPIKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIKeyMatches(t *testing.T) {
	if !APIKeyMatches("controller-secret", "controller-secret") {
		t.Fatal("APIKeyMatches returned false for matching keys")
	}
	if APIKeyMatches("controller-secret", "other-secret") {
		t.Fatal("APIKeyMatches returned true for mismatched keys")
	}
	if APIKeyMatches("", "controller-secret") {
		t.Fatal("APIKeyMatches returned true for empty provided key")
	}
	if APIKeyMatches("controller-secret", "") {
		t.Fatal("APIKeyMatches returned true for empty expected key")
	}
}
