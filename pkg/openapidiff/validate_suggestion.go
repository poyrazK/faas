package openapidiff

import (
	"encoding/json"
	"mime"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// requestBodyValidateSchema returns the operation's JSON request-body schema as
// a self-contained Draft 2020-12 document for a kind=validate rule. Component
// schema references move under $defs so recursive schemas still compile.
// OpenAPI 3.0 nullable and boolean exclusive bounds are rewritten to their
// 2020-12 forms. It reports false when the operation has no JSON body or the
// schema cannot be made self-contained within the rule size limit.
func requestBodyValidateSchema(spec *Spec, op *Operation) (map[string]any, bool) {
	if spec == nil || op == nil {
		return nil, false
	}
	body, ok := resolveComponentRef(spec.Raw, op.Raw["requestBody"], "requestBodies")
	if !ok || body == nil {
		return nil, false
	}
	content, _ := body["content"].(map[string]any)
	var raw any
	for _, key := range requestSortedKeys(content) {
		media, _, err := mime.ParseMediaType(key)
		if err == nil && (media == "application/json" || strings.HasSuffix(media, "+json")) {
			object, _ := content[key].(map[string]any)
			raw = object["schema"]
			break
		}
	}
	if raw == nil {
		return nil, false
	}
	components, _ := spec.Raw["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	conv := schemaConverter{schemas: schemas, openAPI30: strings.HasPrefix(spec.OpenAPIVersion(), "3.0"), defs: map[string]any{}}
	root, ok := conv.convert(raw, 0)
	if !ok {
		return nil, false
	}
	object, isObject := root.(map[string]any)
	if !isObject {
		return nil, false
	}
	if len(conv.defs) > 0 {
		object["$defs"] = conv.defs
	}
	encoded, err := json.Marshal(object)
	if err != nil || len(encoded) > api.MaxEdgeRuleValidateSchemaBytes {
		return nil, false
	}
	return object, true
}

// resolveComponentRef follows one "#/components/<category>/<name>" reference.
func resolveComponentRef(doc map[string]any, raw any, category string) (map[string]any, bool) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, raw == nil
	}
	ref, isRef := object["$ref"].(string)
	if !isRef {
		return object, true
	}
	name, ok := strings.CutPrefix(ref, "#/components/"+category+"/")
	if !ok || strings.Contains(name, "/") {
		return nil, false
	}
	components, _ := doc["components"].(map[string]any)
	entries, _ := components[category].(map[string]any)
	target, ok := entries[name].(map[string]any)
	return target, ok
}

const maxValidateSchemaDepth = 64

type schemaConverter struct {
	schemas   map[string]any
	openAPI30 bool
	defs      map[string]any
}

func (c *schemaConverter) convert(raw any, depth int) (any, bool) {
	if depth > maxValidateSchemaDepth {
		return nil, false
	}
	switch value := raw.(type) {
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			converted, ok := c.convert(item, depth+1)
			if !ok {
				return nil, false
			}
			out[i] = converted
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			if key == "$ref" {
				ref, ok := c.ref(item, depth)
				if !ok {
					return nil, false
				}
				out[key] = ref
				continue
			}
			converted, ok := c.convert(item, depth+1)
			if !ok {
				return nil, false
			}
			out[key] = converted
		}
		if c.openAPI30 {
			openAPI30ToDraft2020(out)
		}
		return out, true
	default:
		return value, true
	}
}

// ref rewrites a component schema reference to #/$defs/<name> and converts the
// target once; recursion terminates because a name is recorded before its body
// is converted.
func (c *schemaConverter) ref(raw any, depth int) (string, bool) {
	ref, ok := raw.(string)
	if !ok {
		return "", false
	}
	name, ok := strings.CutPrefix(ref, "#/components/schemas/")
	if !ok || name == "" || strings.ContainsAny(name, "/~") {
		return "", false
	}
	if _, seen := c.defs[name]; !seen {
		target, exists := c.schemas[name]
		if !exists {
			return "", false
		}
		c.defs[name] = true // placeholder breaks reference cycles
		converted, ok := c.convert(target, depth+1)
		if !ok {
			return "", false
		}
		c.defs[name] = converted
	}
	return "#/$defs/" + name, true
}

// openAPI30ToDraft2020 rewrites the OpenAPI 3.0 keywords whose meaning differs
// in Draft 2020-12, so a valid 3.0 document does not produce false rejections.
func openAPI30ToDraft2020(schema map[string]any) {
	// Only boolean keyword values are rewritten: a "properties" map can hold a
	// property literally named nullable or exclusiveMinimum, whose value is a
	// schema object and must be left alone.
	if nullable, isBool := schema["nullable"].(bool); isBool {
		if typ, ok := schema["type"].(string); ok && nullable {
			schema["type"] = []any{typ, "null"}
		}
		delete(schema, "nullable")
	}
	for _, bound := range [][2]string{{"exclusiveMinimum", "minimum"}, {"exclusiveMaximum", "maximum"}} {
		exclusive, isBool := schema[bound[0]].(bool)
		if !isBool {
			continue
		}
		delete(schema, bound[0])
		if limit, ok := schema[bound[1]]; ok && exclusive {
			schema[bound[0]] = limit
			delete(schema, bound[1])
		}
	}
}
