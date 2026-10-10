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

// requestParameterSchemas builds the validate-rule parameters for one
// operation from its path-level and operation-level OpenAPI parameters (an
// operation entry overrides a path entry with the same name and location).
// Only what the gateway can validate is kept: path, query, and header
// parameters with a plain schema, default serialization style, and a scalar
// or scalar-array type. Cookie parameters, content-encoded parameters, and
// the headers OpenAPI says to ignore (Accept, Content-Type, Authorization)
// are skipped. The path schema is emitted only when every placeholder in the
// template has a usable parameter, because the gateway requires one value
// per placeholder.
func requestParameterSchemas(spec *Spec, item *PathItem, op *Operation, template string) *api.EdgeRuleValidateParameters {
	type key struct{ in, name string }
	merged := map[key]map[string]any{}
	var order []key
	for _, raw := range []any{item.Raw["parameters"], op.Raw["parameters"]} {
		list, _ := raw.([]any)
		for _, entry := range list {
			param, ok := resolveComponentRef(spec.Raw, entry, "parameters")
			if !ok || param == nil {
				continue
			}
			in, _ := param["in"].(string)
			name, _ := param["name"].(string)
			if name == "" {
				continue
			}
			k := key{in, name}
			if in == "header" {
				k.name = strings.ToLower(name)
			}
			if _, seen := merged[k]; !seen {
				order = append(order, k)
			}
			merged[k] = param
		}
	}
	components, _ := spec.Raw["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	locations := map[string]*paramLocation{}
	for _, k := range order {
		param := merged[k]
		if !gatewayParameter(k.in, k.name, param) {
			continue
		}
		conv := schemaConverter{schemas: schemas, openAPI30: strings.HasPrefix(spec.OpenAPIVersion(), "3.0"), defs: map[string]any{}}
		converted, ok := conv.convert(param["schema"], 0)
		if !ok {
			continue
		}
		required, _ := param["required"].(bool)
		loc := locations[k.in]
		if loc == nil {
			loc = &paramLocation{properties: map[string]any{}, defs: map[string]any{}}
			locations[k.in] = loc
		}
		loc.add(k.name, converted, conv.defs, required || k.in == "path")
	}
	out := &api.EdgeRuleValidateParameters{}
	if loc := locations["path"]; loc != nil && loc.coversAll(api.PathTemplateParams(template)) {
		if schema, ok := loc.schema(); ok {
			out.PathTemplate, out.Path = template, schema
		}
	}
	if loc := locations["query"]; loc != nil {
		out.Query, _ = loc.schema()
	}
	if loc := locations["header"]; loc != nil {
		out.Headers, _ = loc.schema()
	}
	if out.Empty() {
		return nil
	}
	return out
}

// gatewayParameter reports whether the gateway can validate this parameter.
func gatewayParameter(in, name string, param map[string]any) bool {
	if _, hasSchema := param["schema"]; !hasSchema {
		return false // content-encoded parameters are not decoded at the edge
	}
	style, _ := param["style"].(string)
	explode, hasExplode := param["explode"].(bool)
	switch in {
	case "path":
		return style == "" || style == "simple"
	case "query":
		return (style == "" || style == "form") && (!hasExplode || explode)
	case "header":
		switch name {
		case "accept", "content-type", "authorization":
			return false
		}
		return style == "" || style == "simple"
	default:
		return false
	}
}

// paramLocation accumulates the properties of one parameter location.
type paramLocation struct {
	properties map[string]any
	defs       map[string]any
	required   []string
}

// coversAll reports whether every name has a property.
func (l *paramLocation) coversAll(names []string) bool {
	if len(names) != len(l.properties) {
		return false
	}
	for _, name := range names {
		if _, ok := l.properties[name]; !ok {
			return false
		}
	}
	return true
}

// add keeps a property only if its converted schema is a type the gateway can
// convert a request value into. A top-level component reference is replaced
// by its target so the gateway reads the real declared type (a shared integer
// ID would otherwise look like an untyped string).
func (l *paramLocation) add(name string, schema any, defs map[string]any, required bool) bool {
	if object, ok := schema.(map[string]any); ok {
		if ref, isRef := object["$ref"].(string); isRef && len(object) == 1 {
			target, found := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
			if !found {
				return false
			}
			schema = target
		}
	}
	probe, err := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{name: schema}})
	if err != nil {
		return false
	}
	if _, err := api.EdgeRuleParamKinds(probe); err != nil {
		return false
	}
	l.properties[name] = schema
	for k, v := range defs {
		l.defs[k] = v
	}
	if required {
		l.required = append(l.required, name)
	}
	return true
}

func (l *paramLocation) schema() (json.RawMessage, bool) {
	object := map[string]any{"type": "object", "properties": l.properties}
	if len(l.required) > 0 {
		object["required"] = l.required
	}
	if len(l.defs) > 0 {
		object["$defs"] = l.defs
	}
	encoded, err := json.Marshal(object)
	if err != nil || len(encoded) > api.MaxEdgeRuleValidateSchemaBytes {
		return nil, false
	}
	return encoded, true
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
