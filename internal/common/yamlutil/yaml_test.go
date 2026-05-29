package yamlutil

import (
	"strings"
	"testing"
)

type strictYAMLConfig struct {
	Server struct {
		APIKey string `yaml:"api_key"`
	} `yaml:"server"`
}

func TestDecodeStrictRejectsMultipleDocuments(t *testing.T) {
	data := []byte(`
server:
  api_key: first-controller-secret
---
server:
  api_key: second-controller-secret
`)

	var cfg strictYAMLConfig
	err := DecodeStrict(data, &cfg)
	if err == nil || !strings.Contains(err.Error(), "multiple yaml documents are not supported") {
		t.Fatalf("DecodeStrict error = %v, want multiple document error", err)
	}
}

func TestDecodeStrictRejectsDuplicateMappingKeys(t *testing.T) {
	data := []byte(`
server:
  api_key: first-controller-secret
  api_key: second-controller-secret
`)

	var cfg strictYAMLConfig
	err := DecodeStrict(data, &cfg)
	if err == nil || !strings.Contains(err.Error(), `duplicate yaml key "server.api_key"`) {
		t.Fatalf("DecodeStrict error = %v, want duplicate key error", err)
	}
}
