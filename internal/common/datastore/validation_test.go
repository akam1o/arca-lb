package datastore

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateResourceID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{
			name:  "simple id",
			value: "vip-1",
		},
		{
			name:  "max length",
			value: strings.Repeat("a", MaxResourceIDBytes),
		},
		{
			name:    "empty",
			value:   "",
			wantErr: true,
		},
		{
			name:    "contains slash",
			value:   "vip/1",
			wantErr: true,
		},
		{
			name:    "contains whitespace",
			value:   "vip 1",
			wantErr: true,
		},
		{
			name:    "contains control character",
			value:   "vip\x001",
			wantErr: true,
		},
		{
			name:    "too long",
			value:   strings.Repeat("a", MaxResourceIDBytes+1),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResourceID("id", tt.value)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}
