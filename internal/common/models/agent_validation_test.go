package models

import (
	"strings"
	"testing"
)

func TestValidateAgentID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:  "dns-like",
			value: "node-a.example",
		},
		{
			name:  "uri-like",
			value: "spiffe://cluster.local/ns/default/sa/arca-agent",
		},
		{
			name:    "empty",
			value:   "",
			wantErr: "agent_id is required",
		},
		{
			name:    "blank",
			value:   "  ",
			wantErr: "agent_id is required",
		},
		{
			name:    "contains space",
			value:   "node a",
			wantErr: "agent_id must not contain whitespace",
		},
		{
			name:    "contains newline",
			value:   "node-a\nnode-b",
			wantErr: "agent_id must not contain whitespace",
		},
		{
			name:    "contains control character",
			value:   "node-a\x00",
			wantErr: "agent_id must not contain whitespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAgentID("agent_id", tt.value)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAgentID() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateAgentID() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
