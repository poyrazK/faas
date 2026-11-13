package speccompliance

import (
	"path/filepath"
	"testing"
)

// Receiver observations add an optional top-level section, but unknown expected
// flags are explicit nulls. Both distinctions must survive SDK generation.
func TestSpecComplianceNotificationReceiverSchemas(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ObsNotificationHealth", "ObsNotificationReceiverHealth", "ObsNotificationReceiver"} {
		t.Run(name, func(t *testing.T) {
			fields, err := scanStructJSONFields(filepath.Join(root, "pkg", "api", "obs_notifications.go"), name)
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
				if field.nullable && !anyContains(property["type"], "null") {
					t.Errorf("%s must preserve an explicit null", field.name)
				}
			}
			if missing, extra := stageStateSetDiff(names, mapKeys(properties)); len(missing)+len(extra) != 0 {
				t.Errorf("JSON fields and schema differ: missing %v, extra %v", missing, extra)
			}
			if missing, extra := stageStateSetDiff(required, anyStrings(schema["required"])); len(missing)+len(extra) != 0 {
				t.Errorf("required fields differ: missing %v, extra %v", missing, extra)
			}
		})
	}
}
