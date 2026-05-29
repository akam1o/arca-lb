package manager

import (
	"io"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []container `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type container struct {
	Name string   `yaml:"name"`
	Args []string `yaml:"args"`
}

func TestOperatorManifestDisablesWebhooksByDefault(t *testing.T) {
	data, err := os.ReadFile("manager.yaml")
	if err != nil {
		t.Fatalf("ReadFile manager.yaml: %v", err)
	}

	deployment := findDeployment(t, data, "arca-lb-operator")
	operator := findContainer(t, deployment.Spec.Template.Spec.Containers, "operator")

	if !containsArg(operator.Args, "--enable-webhooks=false") {
		t.Fatalf("operator args = %#v, want --enable-webhooks=false", operator.Args)
	}
}

func findDeployment(t *testing.T, data []byte, name string) manifest {
	t.Helper()

	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var doc manifest
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode manager.yaml: %v", err)
		}
		if doc.Kind == "Deployment" && doc.Metadata.Name == name {
			return doc
		}
	}

	t.Fatalf("missing Deployment %q", name)
	return manifest{}
}

func findContainer(t *testing.T, containers []container, name string) container {
	t.Helper()

	for _, container := range containers {
		if container.Name == name {
			return container
		}
	}

	t.Fatalf("missing container %q", name)
	return container{}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
