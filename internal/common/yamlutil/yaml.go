package yamlutil

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// DecodeStrict decodes YAML with struct field checking and duplicate key rejection.
func DecodeStrict(data []byte, out interface{}) error {
	var node yaml.Node
	nodeDecoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := nodeDecoder.Decode(&node); err != nil {
		return err
	}
	if err := rejectDuplicateMappingKeys(&node, ""); err != nil {
		return err
	}
	var extra yaml.Node
	if err := nodeDecoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple yaml documents are not supported")
	} else if err != io.EOF {
		return err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(out)
}

func rejectDuplicateMappingKeys(node *yaml.Node, path string) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := rejectDuplicateMappingKeys(child, path); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]*yaml.Node, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			keyPath := joinYAMLPath(path, keyNode.Value)
			if first := seen[keyNode.Value]; first != nil {
				return fmt.Errorf("duplicate yaml key %q at line %d (first defined at line %d)", keyPath, keyNode.Line, first.Line)
			}
			seen[keyNode.Value] = keyNode
			if err := rejectDuplicateMappingKeys(valueNode, keyPath); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := rejectDuplicateMappingKeys(child, path+"[]"); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinYAMLPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
