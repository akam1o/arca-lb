package config

import "github.com/akam1o/arca-lb/internal/common/yamlutil"

func decodeStrictYAML(data []byte, out interface{}) error {
	return yamlutil.DecodeStrict(data, out)
}
