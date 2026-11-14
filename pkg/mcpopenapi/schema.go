package mcpopenapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type schemaResolver struct {
	components map[string]any
	nodes      int
	legacy     bool
}

func (r *schemaResolver) schema(value any, depth int, refs map[string]bool) (map[string]any, error) {
	r.nodes++
	if depth > api.ToolOpenAPISchemaMaxDepth || r.nodes > api.ToolOpenAPISchemaMaxNodes {
		return nil, errors.New("expanded schemas exceed the nesting or node limit")
	}
	in, err := object(value, "schema")
	if err != nil {
		return nil, err
	}
	if ref, exists := in["$ref"]; exists {
		name, ok := ref.(string)
		const prefix = "#/components/schemas/"
		if !ok || !strings.HasPrefix(name, prefix) || len(in) != 1 || refs[name] {
			return nil, errors.New("schema references must be acyclic local component references without siblings")
		}
		key := strings.TrimPrefix(name, prefix)
		if strings.Contains(key, "/") {
			return nil, errors.New("only direct component schema references are supported")
		}
		for i := 0; i < len(key); i++ {
			if key[i] == '~' {
				if i+1 >= len(key) || key[i+1] != '0' && key[i+1] != '1' {
					return nil, errors.New("component reference contains an invalid JSON pointer escape")
				}
				i++
			}
		}
		key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
		target, exists := r.components[key]
		if !exists {
			return nil, errors.New("component schema reference does not exist")
		}
		refs[name] = true
		out, err := r.schema(target, depth+1, refs)
		delete(refs, name)
		return out, err
	}
	kind, _ := in["type"].(string)
	nullable := false
	if types, ok := in["type"].([]any); ok {
		if r.legacy {
			return nil, errors.New("OpenAPI 3.0 requires a single type and nullable instead of type unions")
		}
		if len(types) != 2 {
			return nil, errors.New("schema type unions must contain one type and null")
		}
		for _, typ := range types {
			if typ == "null" {
				nullable = true
			} else if name, ok := typ.(string); ok {
				kind = name
			}
		}
		if !nullable {
			return nil, errors.New("schema type unions must contain null")
		}
	}
	if !slices.Contains([]string{"object", "array", "string", "integer", "number", "boolean", "null"}, kind) {
		return nil, errors.New("schemas require a supported explicit type; composition and untyped schemas are unsupported")
	}
	if r.legacy && kind == "null" {
		return nil, errors.New("OpenAPI 3.0 requires nullable on a supported non-null type")
	}
	out := map[string]any{"type": kind}
	if flag, exists := in["nullable"]; exists {
		if !r.legacy {
			return nil, errors.New("OpenAPI 3.1 requires a type union with null instead of nullable")
		}
		b, ok := flag.(bool)
		if !ok {
			return nil, errors.New("nullable must be a boolean")
		}
		nullable = nullable || b
	}
	if nullable {
		out["nullable"] = true
	}
	for key, value := range in {
		switch key {
		case "type", "nullable":
		case "description", "title", "format":
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("schema %s must be a string", key)
			}
			out[key] = value
		case "readOnly", "writeOnly":
			if value != false {
				return nil, fmt.Errorf("%s schemas require a separately reviewed directional contract", key)
			}
		case "example", "examples", "default", "deprecated":
			// These annotations neither fill arguments nor supply test evidence.
		case "properties", "required", "additionalProperties":
			if kind != "object" {
				return nil, fmt.Errorf("%s requires an object schema", key)
			}
		case "items":
			if kind != "array" {
				return nil, errors.New("items requires an array schema")
			}
		case "minLength", "maxLength":
			if kind != "string" {
				return nil, fmt.Errorf("%s requires a string schema", key)
			}
			if err := copyBound(out, key, value, true); err != nil {
				return nil, err
			}
		case "minItems", "maxItems":
			if kind != "array" {
				return nil, fmt.Errorf("%s requires an array schema", key)
			}
			if err := copyBound(out, key, value, true); err != nil {
				return nil, err
			}
		case "minimum", "maximum":
			if kind != "number" && kind != "integer" {
				return nil, fmt.Errorf("%s requires a numeric schema", key)
			}
			if err := copyBound(out, key, value, false); err != nil {
				return nil, err
			}
		case "enum":
			values, ok := value.([]any)
			if !ok || len(values) == 0 || kind == "object" || kind == "array" {
				return nil, errors.New("enum requires a nonempty list of scalar values")
			}
			for _, v := range values {
				if !enumValueMatches(kind, nullable, v) {
					return nil, errors.New("enum values must match the declared schema type")
				}
			}
			out[key] = values
		default:
			if !strings.HasPrefix(key, "x-") {
				return nil, fmt.Errorf("unsupported schema keyword %q; simplify the selected operation schema", key)
			}
		}
	}
	if kind == "object" {
		if err := r.objectSchema(in, out, depth, refs); err != nil {
			return nil, err
		}
	}
	if kind == "array" {
		items, err := r.schema(in["items"], depth+1, refs)
		if err != nil {
			return nil, err
		}
		out["items"] = items
	}
	for _, pair := range [][2]string{{"minLength", "maxLength"}, {"minItems", "maxItems"}, {"minimum", "maximum"}} {
		lo, lok := out[pair[0]].(json.Number)
		hi, hik := out[pair[1]].(json.Number)
		if lok && hik {
			a, _ := lo.Float64()
			b, _ := hi.Float64()
			if a > b {
				return nil, errors.New("schema lower bound exceeds upper bound")
			}
		}
	}
	return out, nil
}

func (r *schemaResolver) objectSchema(in, out map[string]any, depth int, refs map[string]bool) error {
	properties := map[string]any{}
	if value, exists := in["properties"]; exists {
		var err error
		properties, err = object(value, "schema properties")
		if err != nil {
			return err
		}
	}
	normalized := map[string]any{}
	for name, value := range properties {
		if !safeName(name) {
			return errors.New("schema property names must be bounded printable names without prototype keys")
		}
		schema, err := r.schema(value, depth+1, refs)
		if err != nil {
			return fmt.Errorf("property %q: %w", name, err)
		}
		normalized[name] = schema
	}
	out["properties"] = normalized
	if value, exists := in["required"]; exists {
		required, ok := value.([]any)
		if !ok {
			return errors.New("required must be an array")
		}
		names := []string{}
		for _, item := range required {
			name, ok := item.(string)
			if !ok || normalized[name] == nil || slices.Contains(names, name) {
				return errors.New("required must name unique declared properties")
			}
			names = append(names, name)
		}
		slices.Sort(names)
		out["required"] = names
	}
	if value, exists := in["additionalProperties"]; exists {
		if _, ok := value.(bool); !ok {
			return errors.New("additionalProperties schemas are unsupported; use true or false")
		}
		out["additionalProperties"] = value
	}
	return nil
}

func copyBound(out map[string]any, key string, value any, nonnegativeInteger bool) error {
	n, ok := value.(json.Number)
	if !ok {
		return fmt.Errorf("%s must be a JSON number", key)
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > 9007199254740991 || nonnegativeInteger && (f < 0 || math.Trunc(f) != f) {
		return fmt.Errorf("%s must be a finite supported bound", key)
	}
	out[key] = n
	return nil
}

func enumValueMatches(kind string, nullable bool, value any) bool {
	if value == nil {
		return nullable || kind == "null"
	}
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && !math.IsInf(f, 0) && math.Abs(f) <= 9007199254740991 && (kind != "integer" || math.Trunc(f) == f)
	}
	return false
}
