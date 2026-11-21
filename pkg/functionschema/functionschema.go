// Package functionschema checks a typed-client call's body against the
// callee's input schema (ADR-20261111150000 §1, ADR-20261112160000 §5). It is
// the Go twin of the checker in the Node and Python function adapters
// (pkg/fnadapter/input_schema.go) and covers the same JSON Schema subset:
// type, enum, const, properties, required, additionalProperties, items,
// minItems/maxItems, minLength/maxLength, pattern, minimum/maximum and
// exclusiveMinimum/exclusiveMaximum. Other keywords are ignored, so a richer
// schema never rejects on what the subset cannot check.
package functionschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Schema is a decoded input schema.
type Schema map[string]any

// Parse decodes raw as a schema object.
func Parse(raw []byte) (Schema, error) {
	var s Schema
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("function input schema: %w", err)
	}
	if s == nil {
		return nil, fmt.Errorf("function input schema must be a JSON object")
	}
	return s, nil
}

// CheckBody checks a request body: empty is null, and a body that is not JSON
// fails. It returns the first violation, or "" when the body matches.
func (s Schema) CheckBody(body []byte) string {
	var value any
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return "input must be JSON"
		}
	}
	return check(map[string]any(s), value, "input")
}

func jsonType(value any, typ string) bool {
	switch typ {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := number(value)
		return ok
	case "integer":
		f, ok := number(value)
		return ok && f == math.Trunc(f)
	case "string":
		_, ok := value.(string)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	}
	return true
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	}
	return 0, false
}

func same(a, b any) bool {
	x, errA := json.Marshal(normalize(a))
	y, errB := json.Marshal(normalize(b))
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// normalize turns numbers into float64 so 1 and 1.0 compare equal, as they do
// in the adapters.
func normalize(v any) any {
	switch t := v.(type) {
	case json.Number:
		f, _ := t.Float64()
		return f
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalize(e)
		}
		return out
	}
	return v
}

func literal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func check(schema map[string]any, value any, at string) string {
	if schema == nil {
		return ""
	}
	if c, ok := schema["const"]; ok && !same(c, value) {
		return at + " must be " + literal(c)
	}
	if list, ok := schema["enum"].([]any); ok {
		match := false
		for _, option := range list {
			if same(option, value) {
				match = true
				break
			}
		}
		if !match {
			return at + " must be one of " + literal(list)
		}
	}
	var types []string
	switch t := schema["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				types = append(types, s)
			}
		}
	}
	if len(types) > 0 {
		match := false
		for _, t := range types {
			if jsonType(value, t) {
				match = true
				break
			}
		}
		if !match {
			return at + " must be " + strings.Join(types, " or ")
		}
	}
	bound := func(key string) (float64, bool) { return number(schema[key]) }
	switch v := value.(type) {
	case string:
		length := float64(utf8.RuneCountInString(v))
		if n, ok := bound("minLength"); ok && length < n {
			return fmt.Sprintf("%s must be at least %v characters", at, n)
		}
		if n, ok := bound("maxLength"); ok && length > n {
			return fmt.Sprintf("%s must be at most %v characters", at, n)
		}
		if p, ok := schema["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(v) {
				return at + " must match " + p
			}
		}
	case []any:
		if n, ok := bound("minItems"); ok && float64(len(v)) < n {
			return fmt.Sprintf("%s must have at least %v items", at, n)
		}
		if n, ok := bound("maxItems"); ok && float64(len(v)) > n {
			return fmt.Sprintf("%s must have at most %v items", at, n)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range v {
				if err := check(items, item, fmt.Sprintf("%s[%d]", at, i)); err != "" {
					return err
				}
			}
		}
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				name, _ := r.(string)
				if _, present := v[name]; name != "" && !present {
					return at + "." + name + " is required"
				}
			}
		}
		names := make([]string, 0, len(v))
		for name := range v {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if sub, ok := properties[name]; ok {
				subSchema, _ := sub.(map[string]any)
				if err := check(subSchema, v[name], at+"."+name); err != "" {
					return err
				}
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra {
					return at + "." + name + " is not allowed"
				}
			case map[string]any:
				if err := check(extra, v[name], at+"."+name); err != "" {
					return err
				}
			}
		}
	default:
		if f, ok := number(value); ok {
			if n, ok := bound("minimum"); ok && f < n {
				return fmt.Sprintf("%s must be >= %v", at, n)
			}
			if n, ok := bound("maximum"); ok && f > n {
				return fmt.Sprintf("%s must be <= %v", at, n)
			}
			if n, ok := bound("exclusiveMinimum"); ok && f <= n {
				return fmt.Sprintf("%s must be > %v", at, n)
			}
			if n, ok := bound("exclusiveMaximum"); ok && f >= n {
				return fmt.Sprintf("%s must be < %v", at, n)
			}
		}
	}
	return ""
}
