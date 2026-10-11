package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// EdgeRuleValidateParameters extends a kind=validate rule beyond the request
// body (ADR-091 amendment: request parameters). Each schema is a JSON Schema
// object whose properties are the parameter names; the gateway builds one
// JSON object per location from the request and validates it before reading
// the body. Request values arrive as strings and are converted to each
// property's declared type (integer, number, boolean, string, or an array of
// those) before validation.
//
//   - Path needs PathTemplate, an OpenAPI path template whose {name}
//     placeholders are exactly the path schema's properties. The rule's
//     match_path must be OpenAPIPathGlob(PathTemplate) so every matched
//     request has one value per placeholder.
//   - Query sees every query parameter, so additionalProperties=false can
//     reject unknown ones. A repeated name becomes an array.
//   - Headers sees only the headers its schema declares; property names are
//     lowercase. Comma-separated values become an array when the property
//     is an array.
type EdgeRuleValidateParameters struct {
	PathTemplate string          `json:"path_template,omitempty"`
	Path         json.RawMessage `json:"path,omitempty"`
	Query        json.RawMessage `json:"query,omitempty"`
	Headers      json.RawMessage `json:"headers,omitempty"`
}

// Empty reports whether no parameter schema is configured.
func (p *EdgeRuleValidateParameters) Empty() bool {
	return p == nil || (len(p.Path) == 0 && len(p.Query) == 0 && len(p.Headers) == 0)
}

// EdgeRuleParamKind is the declared JSON type of one parameter, used to
// convert its string value. Item is set only for Type "array".
type EdgeRuleParamKind struct {
	Type string
	Item string
}

var edgeRuleHeaderParamName = regexp.MustCompile(`^[a-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)

// Validate checks the parameter schemas against the rule's match path.
func (p *EdgeRuleValidateParameters) Validate(matchPath string) *Problem {
	if p.Empty() {
		if p != nil && p.PathTemplate != "" {
			return ErrValidation("validate action: parameters.path_template needs parameters.path")
		}
		return nil
	}
	for _, loc := range []struct {
		name   string
		schema json.RawMessage
	}{{"path", p.Path}, {"query", p.Query}, {"headers", p.Headers}} {
		if len(loc.schema) == 0 {
			continue
		}
		if prob := validateParamSchemaBytes(loc.name, loc.schema); prob != nil {
			return prob
		}
		kinds, err := EdgeRuleParamKinds(loc.schema)
		if err != nil {
			return ErrValidation(fmt.Sprintf("validate action: parameters.%s: %v", loc.name, err))
		}
		if loc.name == "headers" {
			for name := range kinds {
				if !edgeRuleHeaderParamName.MatchString(name) {
					return ErrValidation(fmt.Sprintf("validate action: parameters.headers property %q must be a lowercase header name", name))
				}
			}
		}
		if loc.name == "path" {
			if prob := validatePathParams(p.PathTemplate, matchPath, loc.schema, kinds); prob != nil {
				return prob
			}
		}
	}
	if len(p.Path) == 0 && p.PathTemplate != "" {
		return ErrValidation("validate action: parameters.path_template needs parameters.path")
	}
	return nil
}

func validateParamSchemaBytes(location string, schema json.RawMessage) *Problem {
	if len(schema) > MaxEdgeRuleValidateSchemaBytes {
		return ErrValidation(fmt.Sprintf("validate action: parameters.%s exceeds %d bytes (got %d)",
			location, MaxEdgeRuleValidateSchemaBytes, len(schema)))
	}
	if match := edgeRuleValidateRefURLPattern.FindStringIndex(string(schema)); match != nil {
		return ErrValidation(fmt.Sprintf(
			"validate action: parameters.%s contains an external $ref or $id URL (around byte %d); inline schemas only",
			location, match[0]))
	}
	return nil
}

func validatePathParams(template, matchPath string, schema json.RawMessage, kinds map[string]EdgeRuleParamKind) *Problem {
	if template == "" {
		return ErrValidation("validate action: parameters.path needs parameters.path_template")
	}
	glob, ok := OpenAPIPathGlob(template)
	if !ok {
		return ErrValidation(fmt.Sprintf("validate action: parameters.path_template %q is not a valid OpenAPI path template", template))
	}
	for _, seg := range strings.Split(template, "/") {
		if strings.Count(seg, "{") > 1 {
			return ErrValidation(fmt.Sprintf("validate action: parameters.path_template segment %q has more than one placeholder", seg))
		}
	}
	if glob != matchPath {
		return ErrValidation(fmt.Sprintf(
			"validate action: match_path must be %q for parameters.path_template %q so every matched request has its path parameters", glob, template))
	}
	names := PathTemplateParams(template)
	declared := make([]string, 0, len(kinds))
	for name := range kinds {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	sortedNames := append([]string(nil), names...)
	sort.Strings(sortedNames)
	if strings.Join(declared, ",") != strings.Join(sortedNames, ",") {
		return ErrValidation(fmt.Sprintf(
			"validate action: parameters.path properties %v must match the path_template placeholders %v", declared, sortedNames))
	}
	return nil
}

// PathTemplateParams returns the {name} placeholders of an OpenAPI path
// template in order.
func PathTemplateParams(template string) []string {
	var names []string
	for rest := template; ; {
		start := strings.IndexByte(rest, '{')
		if start < 0 {
			return names
		}
		end := strings.IndexByte(rest[start:], '}')
		if end < 0 {
			return names
		}
		names = append(names, rest[start+1:start+end])
		rest = rest[start+end+1:]
	}
}

// PathTemplateValues extracts the {name} values of an OpenAPI path template
// from a request's escaped path. Segments are compared on the escaped form so
// an encoded "/" inside a value cannot shift segments, then each value is
// unescaped. It reports false when the path does not fit the template.
func PathTemplateValues(template, escapedPath string) (map[string]string, bool) {
	tSegs := strings.Split(template, "/")
	pSegs := strings.Split(escapedPath, "/")
	if len(tSegs) != len(pSegs) {
		return nil, false
	}
	values := map[string]string{}
	for i, tSeg := range tSegs {
		start := strings.IndexByte(tSeg, '{')
		if start < 0 {
			if tSeg != pSegs[i] {
				return nil, false
			}
			continue
		}
		end := strings.IndexByte(tSeg, '}')
		prefix, suffix := tSeg[:start], tSeg[end+1:]
		seg := pSegs[i]
		if end < start || strings.ContainsAny(suffix, "{}") ||
			len(seg) <= len(prefix)+len(suffix) || !strings.HasPrefix(seg, prefix) || !strings.HasSuffix(seg, suffix) {
			return nil, false
		}
		raw := seg[len(prefix) : len(seg)-len(suffix)]
		value, err := url.PathUnescape(raw)
		if err != nil {
			return nil, false
		}
		values[tSeg[start+1:end]] = value
	}
	return values, true
}

// EdgeRuleParamKinds reads the declared type of every property of a
// parameter schema. The schema must be an object with "properties"; each
// property may declare a scalar type, an array of scalars, or no type
// (treated as string). Anything else is rejected because a request
// parameter cannot carry it.
func EdgeRuleParamKinds(schema json.RawMessage) (map[string]EdgeRuleParamKind, error) {
	var root struct {
		Type       any                        `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, fmt.Errorf("schema is not a JSON object: %w", err)
	}
	if root.Type != "object" {
		return nil, fmt.Errorf(`schema must have "type": "object"`)
	}
	if len(root.Properties) == 0 {
		return nil, fmt.Errorf("schema must declare at least one property")
	}
	kinds := make(map[string]EdgeRuleParamKind, len(root.Properties))
	for name, raw := range root.Properties {
		var prop struct {
			Type  any `json:"type"`
			Items *struct {
				Type any `json:"type"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			return nil, fmt.Errorf("property %q is not a schema object", name)
		}
		typ, ok := paramScalarType(prop.Type)
		switch {
		case prop.Type == "array":
			item := "string"
			if prop.Items != nil {
				if item, ok = paramScalarType(prop.Items.Type); !ok {
					return nil, fmt.Errorf("property %q: array items must be a scalar type", name)
				}
			}
			kinds[name] = EdgeRuleParamKind{Type: "array", Item: item}
		case ok:
			kinds[name] = EdgeRuleParamKind{Type: typ}
		default:
			return nil, fmt.Errorf("property %q: type must be string, integer, number, boolean, or an array of those", name)
		}
	}
	return kinds, nil
}

// paramScalarType accepts a missing type (string) or one scalar type name,
// optionally paired with "null" as OpenAPI 3.0 nullable output produces.
func paramScalarType(raw any) (string, bool) {
	switch t := raw.(type) {
	case nil:
		return "string", true
	case string:
		switch t {
		case "string", "integer", "number", "boolean":
			return t, true
		}
	case []any:
		var picked string
		for _, item := range t {
			name, _ := item.(string)
			if name == "null" {
				continue
			}
			if picked != "" {
				return "", false
			}
			picked = name
		}
		return paramScalarType(picked)
	}
	return "", false
}

// EdgeRuleParamInstance builds the JSON object validated for one parameter
// location. values maps each parameter name to its raw request values;
// declared properties are converted to their kind, while undeclared names
// stay strings (an array when repeated). A value that does not parse as its
// declared type stays a string so the schema reports the mismatch.
func EdgeRuleParamInstance(kinds map[string]EdgeRuleParamKind, values map[string][]string) ([]byte, error) {
	out := make(map[string]any, len(values))
	for name, raw := range values {
		if len(raw) == 0 {
			continue
		}
		kind, declared := kinds[name]
		if !declared {
			kind = EdgeRuleParamKind{Type: "string"}
		}
		if kind.Type == "array" {
			items := make([]any, len(raw))
			for i, v := range raw {
				items[i] = convertParamValue(kind.Item, v)
			}
			out[name] = items
			continue
		}
		if len(raw) == 1 {
			out[name] = convertParamValue(kind.Type, raw[0])
			continue
		}
		items := make([]any, len(raw))
		for i, v := range raw {
			items[i] = convertParamValue(kind.Type, v)
		}
		out[name] = items
	}
	return json.Marshal(out)
}

func convertParamValue(kind, value string) any {
	switch kind {
	case "integer":
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			return json.Number(value)
		}
	case "number":
		var n json.Number
		if json.Unmarshal([]byte(value), &n) == nil && n.String() == value {
			return n
		}
	case "boolean":
		switch value {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return value
}
