package mcpmanagement

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

func guidanceOutputSchema(name string) map[string]any {
	var schema *jsonschema.Schema
	var err error
	if name == "gregale_get_workflow" {
		schema, err = jsonschema.For[workflowResult](nil)
	} else {
		schema, err = jsonschema.For[projectAssessment](nil)
	}
	if err != nil {
		panic(err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		panic(err)
	}
	normalizeGuidanceSchema(result)
	return result
}

// The bridge's exact-number envelope validator uses typed string lists.
// Restore them after decoding the reflected JSON Schema.
func normalizeGuidanceSchema(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "type" || key == "required" || key == "enum" {
				if items, ok := child.([]any); ok {
					list := make([]string, len(items))
					for i, item := range items {
						list[i] = item.(string)
					}
					value[key] = list
				}
			}
			normalizeGuidanceSchema(child)
		}
	case []any:
		for _, child := range value {
			normalizeGuidanceSchema(child)
		}
	}
}
