package v1alpha1

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVirtualIPCRDEnforcesHealthCheckAdmissionValidation(t *testing.T) {
	crd := loadVirtualIPCRD(t)
	healthCheck := crdSchemaProperty(t, crd, "spec", "healthCheck")
	messages := validationMessages(t, healthCheck)
	for _, want := range []string{
		"spec.healthCheck.http is required for type http or https",
		"spec.healthCheck.tcp is required for type tcp or tls-hello",
		"spec.healthCheck.timeoutSeconds must be less than intervalSeconds",
	} {
		if !containsString(messages, want) {
			t.Fatalf("missing CRD validation message %q in %#v", want, messages)
		}
	}

	http := nestedMap(t, healthCheck, "properties", "http")
	if !containsString(stringSlice(t, http["required"]), "port") {
		t.Fatalf("healthCheck.http.port is not required in CRD schema")
	}
	expectedCodes := nestedMap(t, http, "properties", "expectedCodes")
	expectedCodeItems := nestedMap(t, expectedCodes, "items")
	if got := expectedCodeItems["minimum"]; got != 100 {
		t.Fatalf("expectedCodes item minimum = %v, want 100", got)
	}
	if got := expectedCodeItems["maximum"]; got != 599 {
		t.Fatalf("expectedCodes item maximum = %v, want 599", got)
	}

	tcp := nestedMap(t, healthCheck, "properties", "tcp")
	if !containsString(stringSlice(t, tcp["required"]), "port") {
		t.Fatalf("healthCheck.tcp.port is not required in CRD schema")
	}

	typeSchema := nestedMap(t, healthCheck, "properties", "type")
	typeEnum := stringSlice(t, typeSchema["enum"])
	if !containsString(typeEnum, "tls-hello") {
		t.Fatalf("healthCheck.type enum does not include tls-hello: %#v", typeEnum)
	}

	for _, field := range []string{"intervalSeconds", "timeoutSeconds", "riseCount", "fallCount"} {
		fieldSchema := nestedMap(t, healthCheck, "properties", field)
		if got := fieldSchema["maximum"]; got != 2147483647 {
			t.Fatalf("healthCheck.%s maximum = %v, want 2147483647", field, got)
		}
	}
}

func TestVirtualIPCRDRequiresSpec(t *testing.T) {
	crd := loadVirtualIPCRD(t)
	schema := virtualIPCRDSchema(t, crd)

	if !containsString(stringSlice(t, schema["required"]), "spec") {
		t.Fatalf("spec is not required in CRD schema")
	}
}

func TestVirtualIPCRDDefinesBackendMonitorAddress(t *testing.T) {
	crd := loadVirtualIPCRD(t)
	backends := crdSchemaProperty(t, crd, "spec", "backends")
	monitorAddress := nestedMap(t, backends, "items", "properties", "monitorAddress")

	if got := monitorAddress["format"]; got != "ip" {
		t.Fatalf("monitorAddress format = %v, want ip", got)
	}
	if containsString(stringSlice(t, nestedMap(t, backends, "items")["required"]), "monitorAddress") {
		t.Fatalf("monitorAddress is required in CRD schema")
	}
}

func TestVirtualIPCRDRejectsDuplicateBackendAddresses(t *testing.T) {
	crd := loadVirtualIPCRD(t)
	backends := crdSchemaProperty(t, crd, "spec", "backends")
	validations := nestedSlice(t, backends, "x-kubernetes-validations")

	for _, raw := range validations {
		validation, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("validation item has type %T, want map[string]interface{}", raw)
		}
		if validation["message"] == "spec.backends addresses must be unique" &&
			validation["rule"] == "self.all(b1, self.exists_one(b2, b2.address == b1.address))" {
			return
		}
	}
	t.Fatalf("missing backend uniqueness validation in %#v", validations)
}

func TestVirtualIPCRDRejectsUnsupportedEncapAddressFamilies(t *testing.T) {
	crd := loadVirtualIPCRD(t)
	spec := crdSchemaProperty(t, crd, "spec")
	validations := nestedSlice(t, spec, "x-kubernetes-validations")

	assertHasValidation(t, validations,
		"spec.encapType L3DSR/NAT4 requires an IPv4 spec.address",
		"!has(self.encapType) || (self.encapType != 'L3DSR' && self.encapType != 'NAT4') || self.address.matches('^([0-9]{1,3}[.]){3}[0-9]{1,3}$')",
	)
	assertHasValidation(t, validations,
		"spec.encapType NAT6 requires an IPv6 spec.address",
		"!has(self.encapType) || self.encapType != 'NAT6' || !self.address.matches('^([0-9]{1,3}[.]){3}[0-9]{1,3}$')",
	)
	assertHasValidation(t, validations,
		"spec.backends addresses must be IPv4 for GRE4/L3DSR/NAT4 encapType",
		"!has(self.encapType) || (self.encapType != 'GRE4' && self.encapType != 'L3DSR' && self.encapType != 'NAT4') || !has(self.backends) || self.backends.all(be, be.address.matches('^([0-9]{1,3}[.]){3}[0-9]{1,3}$'))",
	)
	assertHasValidation(t, validations,
		"spec.backends addresses must be IPv6 for GRE6/NAT6 encapType",
		"!has(self.encapType) || (self.encapType != 'GRE6' && self.encapType != 'NAT6') || !has(self.backends) || self.backends.all(be, !be.address.matches('^([0-9]{1,3}[.]){3}[0-9]{1,3}$'))",
	)
}

func assertHasValidation(t *testing.T, validations []interface{}, wantMessage, wantRule string) {
	t.Helper()

	for _, raw := range validations {
		validation, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("validation item has type %T, want map[string]interface{}", raw)
		}
		if validation["message"] == wantMessage && validation["rule"] == wantRule {
			return
		}
	}
	t.Fatalf("missing CRD validation message %q and rule %q in %#v", wantMessage, wantRule, validations)
}

func loadVirtualIPCRD(t *testing.T) map[string]interface{} {
	t.Helper()

	crdPath := filepath.Join("..", "..", "config", "crd", "bases", "arca.io_virtualips.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("failed to read CRD: %v", err)
	}

	var crd map[string]interface{}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("failed to parse CRD YAML: %v", err)
	}
	return crd
}

func crdSchemaProperty(t *testing.T, crd map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	current := virtualIPCRDSchema(t, crd)
	for _, name := range path {
		current = nestedMapFromValue(t, current, "properties", name)
	}
	return current
}

func virtualIPCRDSchema(t *testing.T, crd map[string]interface{}) map[string]interface{} {
	t.Helper()

	versions := nestedSlice(t, crd, "spec", "versions")
	if len(versions) == 0 {
		t.Fatalf("CRD has no versions")
	}
	return nestedMapFromValue(t, versions[0], "schema", "openAPIV3Schema")
}

func validationMessages(t *testing.T, schema map[string]interface{}) []string {
	t.Helper()

	validations := nestedSlice(t, schema, "x-kubernetes-validations")
	messages := make([]string, 0, len(validations))
	for _, validation := range validations {
		item, ok := validation.(map[string]interface{})
		if !ok {
			t.Fatalf("validation item has type %T, want map[string]interface{}", validation)
		}
		message, ok := item["message"].(string)
		if !ok {
			t.Fatalf("validation message has type %T, want string", item["message"])
		}
		messages = append(messages, message)
	}
	return messages
}

func nestedMap(t *testing.T, m map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	return nestedMapFromValue(t, m, path...)
}

func nestedMapFromValue(t *testing.T, value interface{}, path ...string) map[string]interface{} {
	t.Helper()

	current, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("value has type %T, want map[string]interface{}", value)
	}
	for _, key := range path {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			t.Fatalf("field %q has type %T, want map[string]interface{}", key, current[key])
		}
		current = next
	}
	return current
}

func nestedSlice(t *testing.T, m map[string]interface{}, path ...string) []interface{} {
	t.Helper()

	current := interface{}(m)
	for _, key := range path {
		asMap, ok := current.(map[string]interface{})
		if !ok {
			t.Fatalf("value before %q has type %T, want map[string]interface{}", key, current)
		}
		current = asMap[key]
	}
	slice, ok := current.([]interface{})
	if !ok {
		t.Fatalf("value has type %T, want []interface{}", current)
	}
	return slice
}

func stringSlice(t *testing.T, value interface{}) []string {
	t.Helper()

	raw, ok := value.([]interface{})
	if !ok {
		t.Fatalf("value has type %T, want []interface{}", value)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("slice item has type %T, want string", item)
		}
		out = append(out, s)
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
