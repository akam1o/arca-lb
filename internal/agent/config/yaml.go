package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

func decodeStrictYAML(data []byte, out interface{}) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(out)
}
