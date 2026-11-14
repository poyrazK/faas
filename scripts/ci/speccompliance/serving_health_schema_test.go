package speccompliance

import (
	"path/filepath"
	"testing"
)

func TestSpecComplianceServingHealthSchemas(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ObsServingHealth", "ObsServingProbeInventory", "ObsServingProbeCounts", "ObsServingProbe"} {
		t.Run(name, func(t *testing.T) {
			fields, err := scanStructJSONFields(filepath.Join(root, "pkg", "api", "obs_serving_health.go"), name)
			if err != nil {
				t.Fatal(err)
			}
			schema := spec.Schemas[name]
			properties, _ := schema["properties"].(map[string]any)
			var names, required []string
			for _, field := range fields {
				names = append(names, field.name)
				if field.required() {
					required = append(required, field.name)
				}
				property, _ := properties[field.name].(map[string]any)
				if field.nullable && !servingSchemaNullable(property) {
					t.Errorf("%s must preserve explicit null", field.name)
				}
			}
			if missing, extra := stageStateSetDiff(names, mapKeys(properties)); len(missing)+len(extra) != 0 {
				t.Errorf("JSON fields differ: missing %v extra %v", missing, extra)
			}
			if missing, extra := stageStateSetDiff(required, anyStrings(schema["required"])); len(missing)+len(extra) != 0 {
				t.Errorf("required fields differ: missing %v extra %v", missing, extra)
			}
		})
	}
}

func servingSchemaNullable(property map[string]any) bool {
	if anyContains(property["type"], "null") {
		return true
	}
	branches, _ := property["anyOf"].([]any)
	for _, branch := range branches {
		if schema, ok := branch.(map[string]any); ok && schema["type"] == "null" {
			return true
		}
	}
	return false
}
