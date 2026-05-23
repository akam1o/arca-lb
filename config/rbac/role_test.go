package rbac

import (
	"io"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type roleManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Rules []roleRule `yaml:"rules"`
}

type roleRule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

func TestOperatorVirtualIPRBACUsesLeastPrivilegeVerbs(t *testing.T) {
	data, err := os.ReadFile("role.yaml")
	if err != nil {
		t.Fatalf("ReadFile role.yaml: %v", err)
	}

	docs := decodeRoleManifests(t, data)

	rule := findRule(t, docs, "arca-lb-operator", "arca.io", "virtualips")
	for _, want := range []string{"get", "list", "watch"} {
		if !contains(rule.Verbs, want) {
			t.Fatalf("operator virtualips verbs = %#v, want %q", rule.Verbs, want)
		}
	}
	for _, disallowed := range []string{"create", "update", "patch", "delete"} {
		if contains(rule.Verbs, disallowed) {
			t.Fatalf("operator virtualips verbs = %#v, must not include %q", rule.Verbs, disallowed)
		}
	}
}

func findRule(t *testing.T, docs []roleManifest, name, group, resource string) roleRule {
	t.Helper()

	for _, doc := range docs {
		if doc.Kind != "ClusterRole" || doc.Metadata.Name != name {
			continue
		}
		for _, rule := range doc.Rules {
			if contains(rule.APIGroups, group) && contains(rule.Resources, resource) {
				return rule
			}
		}
	}

	t.Fatalf("missing %s rule for %s/%s", name, group, resource)
	return roleRule{}
}

func decodeRoleManifests(t *testing.T, data []byte) []roleManifest {
	t.Helper()

	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var docs []roleManifest
	for {
		var doc roleManifest
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode role.yaml: %v", err)
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
	return docs
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
