package edgevalidate

import (
	"fmt"
	"strings"
)

// walkSchemaNodes visits schema positions, rather than application data in
// const/enum/examples. This also covers legacy drafts supported by the compiler.
func walkSchemaNodes(node any, visit func(map[string]any)) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	visit(m)
	for _, key := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies"} {
		if children, ok := m[key].(map[string]any); ok {
			for _, child := range children {
				walkSchemaNodes(child, visit)
			}
		}
	}
	for _, key := range []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "additionalItems", "unevaluatedItems", "contains", "not", "if", "then", "else", "contentSchema"} {
		walkSchemaNodes(m[key], visit)
	}
	for _, key := range []string{"items", "prefixItems", "allOf", "anyOf", "oneOf"} {
		if children, ok := m[key].([]any); ok {
			for _, child := range children {
				walkSchemaNodes(child, visit)
			}
		} else {
			walkSchemaNodes(m[key], visit)
		}
	}
}

func checkInlineSchema(doc any) error {
	var invalid error
	legacy := legacyDraft(doc)
	walkSchemaNodes(doc, func(node map[string]any) {
		for _, key := range []string{"$ref", "$dynamicRef", "$recursiveRef", "$id", "id"} {
			if key == "id" && !legacy {
				continue
			}
			if ref, ok := node[key].(string); ok && ref != "" && !strings.HasPrefix(ref, "#") {
				invalid = fmt.Errorf("%w: %s must resolve within the inline document", ErrSchemaExternalRef, key)
			}
		}
	})
	return invalid
}
