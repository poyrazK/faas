package openapidiff

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func requestTestSpec(t *testing.T, version string, operation, components, pathMetadata map[string]any) *Spec {
	t.Helper()
	item := map[string]any{"post": operation}
	for key, value := range pathMetadata {
		item[key] = value
	}
	doc := map[string]any{"openapi": version, "info": map[string]any{"title": "test", "version": "1"}, "paths": map[string]any{"/items/{id}": item}, "components": components}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := LoadBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func requestTestBody(schema any) map[string]any {
	return map[string]any{"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": schema}}}}
}
func requestTestParameter(name string, required bool, schema any) map[string]any {
	return map[string]any{"name": name, "in": "query", "required": required, "schema": schema}
}
func requestTestCompare(t *testing.T, before, after map[string]any) RequestRoute {
	t.Helper()
	result, err := CompareRequests(requestTestSpec(t, "3.1.0", before, nil, nil), requestTestSpec(t, "3.1.0", after, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatal(result)
	}
	return result.Routes[0]
}
func requestTestHasCode(row RequestRoute, code string) bool {
	for _, finding := range row.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestCompareRequestsRequiredInputs(t *testing.T) {
	before := requestTestBody(map[string]any{"type": "object", "properties": map[string]any{"currency": map[string]any{"type": "string"}}})
	after := requestTestBody(map[string]any{"type": "object", "properties": map[string]any{"currency": map[string]any{"type": "string"}}, "required": []any{"currency"}})
	row := requestTestCompare(t, before, after)
	if row.Status != "breaking" || !row.Complete || !row.Changed || !requestTestHasCode(row, "property_required") {
		t.Fatal(row)
	}
	if reverse := requestTestCompare(t, after, before); reverse.Status != "no_supported_breaks" || !reverse.Changed {
		t.Fatal(reverse)
	}
	after = requestTestBody(map[string]any{"type": "string"})
	after["requestBody"].(map[string]any)["required"] = true
	if row := requestTestCompare(t, map[string]any{}, after); !requestTestHasCode(row, "request_body_required") {
		t.Fatal(row)
	}
	for _, location := range []string{"query", "header", "cookie"} {
		parameter := requestTestParameter("currency", true, map[string]any{"type": "string"})
		parameter["in"] = location
		row := requestTestCompare(t, map[string]any{}, map[string]any{"parameters": []any{parameter}})
		if !row.Complete || !requestTestHasCode(row, "parameter_required") {
			t.Fatal(row)
		}
	}
}

func TestCompareRequestsSchemaDirection(t *testing.T) {
	cases := []struct {
		name          string
		before, after any
		code          string
	}{
		{"number to integer", map[string]any{"type": "number"}, map[string]any{"type": "integer"}, "accepted_type_narrowed"},
		{"integer to number", map[string]any{"type": "integer"}, map[string]any{"type": "number"}, ""},
		{"null removed", map[string]any{"type": []any{"string", "null"}}, map[string]any{"type": "string"}, "null_no_longer_allowed"},
		{"null added", map[string]any{"type": "string"}, map[string]any{"type": []any{"string", "null"}}, ""},
		{"enum narrowed", map[string]any{"type": "string", "enum": []any{"a", "b"}}, map[string]any{"type": "string", "enum": []any{"a"}}, "enum_values_restricted"},
		{"enum widened", map[string]any{"type": "string", "enum": []any{"a"}}, map[string]any{"type": "string", "enum": []any{"a", "b"}}, ""},
		{"boolean exhaustive enum", map[string]any{"type": "boolean"}, map[string]any{"type": "boolean", "enum": []any{true, false}}, ""},
		{"enum already excludes null", map[string]any{"type": []any{"string", "null"}, "enum": []any{"a"}}, map[string]any{"type": "string", "enum": []any{"a"}}, ""},
		{"integer enum in widened type", map[string]any{"type": "integer", "enum": []any{1}}, map[string]any{"type": "number", "enum": []any{1.0}}, ""},
		{"array items narrowed", map[string]any{"type": "array", "items": map[string]any{"type": "number"}}, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "accepted_type_narrowed"},
		{"unconstrained arrays", map[string]any{"type": "array"}, map[string]any{"type": "array"}, ""},
		{"accepting schema", false, true, ""},
		{"rejecting schema", true, false, "schema_rejects_all"},
		{"enum covers all null", map[string]any{"type": "null"}, map[string]any{"type": "null", "enum": []any{nil}}, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := requestTestCompare(t, requestTestBody(test.before), requestTestBody(test.after))
			if !row.Complete {
				t.Fatal(row)
			}
			if test.code == "" {
				if row.Status != "no_supported_breaks" {
					t.Fatal(row)
				}
			} else if row.Status != "breaking" || !requestTestHasCode(row, test.code) {
				t.Fatal(row)
			}
		})
	}
}

func TestCompareRequestsObjectPropertiesAreInputRules(t *testing.T) {
	closed := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"old": map[string]any{"type": "string"}}}
	open := map[string]any{"type": "object", "properties": map[string]any{}}
	if row := requestTestCompare(t, requestTestBody(closed), requestTestBody(open)); row.Status != "no_supported_breaks" {
		t.Fatal("response removal rules leaked into requests", row)
	}
	after := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}
	if row := requestTestCompare(t, requestTestBody(closed), requestTestBody(after)); !requestTestHasCode(row, "property_no_longer_allowed") {
		t.Fatal(row)
	}
	if row := requestTestCompare(t, requestTestBody(open), requestTestBody(after)); !requestTestHasCode(row, "additional_properties_restricted") {
		t.Fatal(row)
	}
	optional := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"old": map[string]any{"type": "string"}, "new": map[string]any{"type": "string"}}}
	if row := requestTestCompare(t, requestTestBody(closed), requestTestBody(optional)); row.Status != "no_supported_breaks" {
		t.Fatal(row)
	}
	// An optional declared property can narrow a previously open object's extras.
	if row := requestTestCompare(t, requestTestBody(open), requestTestBody(optional)); row.Status != "breaking" {
		t.Fatal(row)
	}
}

func TestCompareRequestsLocalReferencesAndOverrides(t *testing.T) {
	operation := map[string]any{"requestBody": map[string]any{"$ref": "#/components/requestBodies/Input"}}
	components := func(required bool) map[string]any {
		schema := map[string]any{"type": "object", "properties": map[string]any{"a/b~c": map[string]any{"type": "string"}}}
		if required {
			schema["required"] = []any{"a/b~c"}
		}
		return map[string]any{"requestBodies": map[string]any{"Input": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Input~1Model~0"}}}}}, "schemas": map[string]any{"Input/Model~": schema}}
	}
	base := requestTestSpec(t, "3.1.0", operation, components(false), nil)
	proposed := requestTestSpec(t, "3.1.0", operation, components(true), nil)
	result, err := CompareRequests(base, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if row := result.Routes[0]; !row.Changed || !requestTestHasCode(row, "property_required") || !strings.HasSuffix(row.Findings[0].Location, "/a~1b~0c") {
		t.Fatal(row)
	}
	pathParameter := requestTestParameter("value", true, map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}})
	override := requestTestParameter("value", false, map[string]any{"type": "string"})
	before := requestTestSpec(t, "3.1.0", map[string]any{"parameters": []any{override}}, nil, map[string]any{"parameters": []any{pathParameter}})
	after := requestTestSpec(t, "3.1.0", map[string]any{"parameters": []any{override}}, nil, nil)
	result, err = CompareRequests(before, after)
	if err != nil || result.Routes[0].Status != "no_supported_breaks" || !result.Routes[0].Complete {
		t.Fatalf("%+v %v", result, err)
	}
	paramRef := map[string]any{"$ref": "#/components/parameters/Value"}
	before = requestTestSpec(t, "3.1.0", map[string]any{"parameters": []any{paramRef}}, map[string]any{"parameters": map[string]any{"Value": requestTestParameter("value", false, map[string]any{"type": "string"})}}, nil)
	after = requestTestSpec(t, "3.1.0", map[string]any{"parameters": []any{paramRef}}, map[string]any{"parameters": map[string]any{"Value": requestTestParameter("value", true, map[string]any{"type": "string"})}}, nil)
	result, err = CompareRequests(before, after)
	if err != nil || !requestTestHasCode(result.Routes[0], "parameter_required") {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestCompareRequestsMediaCoverageAndExceptions(t *testing.T) {
	body := func(content map[string]any) map[string]any {
		return map[string]any{"requestBody": map[string]any{"content": content}}
	}
	anyMedia := map[string]any{"schema": map[string]any{}}
	stringMedia := map[string]any{"schema": map[string]any{"type": "string"}}
	cases := []struct {
		name          string
		before, after map[string]any
		code          string
	}{
		{"removed JSON", map[string]any{"application/json": anyMedia}, map[string]any{"text/plain": anyMedia}, "content_type_removed"},
		{"wildcard covers literal", map[string]any{"application/json": anyMedia}, map[string]any{"*/*": anyMedia}, ""},
		{"range narrowed", map[string]any{"application/*": anyMedia}, map[string]any{"application/json": anyMedia}, "content_type_removed"},
		{"specific exception", map[string]any{"*/*": anyMedia}, map[string]any{"*/*": anyMedia, "application/json": stringMedia}, "accepted_type_narrowed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := requestTestCompare(t, body(test.before), body(test.after))
			if !row.Complete || (test.code == "" && row.Status != "no_supported_breaks") || (test.code != "" && !requestTestHasCode(row, test.code)) {
				t.Fatal(row)
			}
		})
	}
}

func TestCompareRequestsUnknownAndPrivacy(t *testing.T) {
	for _, schema := range []any{
		map[string]any{"oneOf": []any{map[string]any{"type": "string"}}},
		map[string]any{"type": "string", "pattern": "secret-value"},
		map[string]any{"$ref": "https://secret-value@example.com/schema"},
		map[string]any{"$ref": "#/components/schemas/Missing"},
		map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		map[string]any{"type": "string", "readOnly": true},
		map[string]any{"enum": []any{map[string]any{"secret-value": 1}}},
	} {
		row := requestTestCompare(t, requestTestBody(schema), requestTestBody(schema))
		if row.Status != "unknown" || row.Complete {
			t.Fatal(row)
		}
		body, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "secret-value") || strings.Contains(string(body), "example.com") {
			t.Fatal("leaked input", string(body))
		}
	}
	before := requestTestBody(map[string]any{"type": "string", "enum": []any{"secret-value", "retained"}})
	after := requestTestBody(map[string]any{"type": "string", "enum": []any{"retained"}})
	row := requestTestCompare(t, before, after)
	body, _ := json.Marshal(row)
	if row.Status != "breaking" || strings.Contains(string(body), "secret-value") || strings.Contains(string(body), "retained") {
		t.Fatal(row)
	}
	// A known required body can coexist with an unresolved child schema.
	after["requestBody"].(map[string]any)["required"] = true
	after["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"] = map[string]any{"oneOf": []any{map[string]any{"type": "string"}}}
	row = requestTestCompare(t, map[string]any{}, after)
	if row.Status != "breaking" || row.Complete || !requestTestHasCode(row, "request_body_required") {
		t.Fatal(row)
	}
}

func TestCompareRequestsCyclesAndReferenceSiblings(t *testing.T) {
	operation := requestTestBody(map[string]any{"$ref": "#/components/schemas/Node"})
	components := map[string]any{"schemas": map[string]any{"Node": map[string]any{"type": "object", "properties": map[string]any{"child": map[string]any{"$ref": "#/components/schemas/Node"}}}}}
	spec := requestTestSpec(t, "3.1.0", operation, components, nil)
	result, err := CompareRequests(spec, spec)
	if err != nil || !requestTestHasCode(result.Routes[0], "recursive_reference_not_compared") {
		t.Fatalf("%+v %v", result, err)
	}
	operation = requestTestBody(map[string]any{"$ref": "#/components/schemas/Input", "type": "integer"})
	components = map[string]any{"schemas": map[string]any{"Input": map[string]any{"type": "string"}}}
	for _, version := range []string{"3.0.4", "3.1.0"} {
		spec = requestTestSpec(t, version, operation, components, nil)
		result, err = CompareRequests(spec, spec)
		if err != nil {
			t.Fatal(err)
		}
		if version == "3.0.4" && !result.Routes[0].Complete {
			t.Fatal(result)
		}
		if version == "3.1.0" && !requestTestHasCode(result.Routes[0], "reference_siblings_not_compared") {
			t.Fatal(result)
		}
	}
}

func TestCompareRequestsNoiseAndVersions(t *testing.T) {
	before := requestTestBody(map[string]any{"type": "string", "enum": []any{"a", "b"}, "description": "old", "example": "secret-old"})
	after := requestTestBody(map[string]any{"type": "string", "enum": []any{"b", "a"}, "description": "new", "example": "secret-new"})
	row := requestTestCompare(t, before, after)
	if row.Changed || row.Status != "no_supported_breaks" {
		t.Fatal(row)
	}
	old := requestTestSpec(t, "3.0.4", requestTestBody(map[string]any{"type": "string", "nullable": true}), nil, nil)
	new := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": []any{"null", "string"}}), nil, nil)
	result, err := CompareRequests(old, new)
	if err != nil || result.Routes[0].Changed || !result.Routes[0].Complete {
		t.Fatalf("%+v %v", result, err)
	}
	// Normalized default serialization and parameter ordering are noise.
	p1, p2 := requestTestParameter("a", false, map[string]any{"type": "string"}), requestTestParameter("b", false, map[string]any{"type": "string"})
	q1, q2 := requestTestParameter("a", false, map[string]any{"type": "string"}), requestTestParameter("b", false, map[string]any{"type": "string"})
	q1["style"], q1["explode"] = "form", true
	row = requestTestCompare(t, map[string]any{"parameters": []any{p1, p2}}, map[string]any{"parameters": []any{q2, q1}})
	if row.Changed || !row.Complete {
		t.Fatal(row)
	}
	q1["explode"] = false
	row = requestTestCompare(t, map[string]any{"parameters": []any{p1}}, map[string]any{"parameters": []any{q1}})
	if row.Status != "unknown" || !requestTestHasCode(row, "parameter_serialization_changed") {
		t.Fatal(row)
	}
	if !reflect.DeepEqual(p1, requestTestParameter("a", false, map[string]any{"type": "string"})) {
		t.Fatal("comparison mutated input")
	}
}

func TestCompareRequestsLimitsReturnNoPartialComparison(t *testing.T) {
	spec := func(schema any, routes int) *Spec {
		s := &Spec{version: "3.1.0", Paths: map[string]*PathItem{}}
		for i := 0; i < routes; i++ {
			s.Paths[fmt.Sprintf("/r%d", i)] = &PathItem{Methods: map[string]*Operation{"post": {Raw: requestTestBody(schema)}}}
		}
		return s
	}
	assertLimit := func(base, candidate *Spec) {
		t.Helper()
		result, err := CompareRequests(base, candidate)
		if !errors.Is(err, ErrRequestComparisonLimit) || len(result.Routes) != 0 {
			t.Fatalf("partial/accepted result: %d %v", len(result.Routes), err)
		}
	}
	assertLimit(spec(map[string]any{}, 1), spec(map[string]any{}, api.RequestCompatibilityMaxRoutes+1))
	assertLimit(spec(map[string]any{}, 1), spec(map[string]any{"enum": make([]any, api.RequestCompatibilityMaxEnumValues+1)}, 1))
	var deep any = map[string]any{"type": "string"}
	for i := 0; i <= api.RequestCompatibilityMaxDepth; i++ {
		deep = map[string]any{"type": "array", "items": deep}
	}
	assertLimit(spec(map[string]any{}, 1), spec(deep, 1))
	var nullable any = map[string]any{"type": "integer"}
	for i := 0; i <= api.RequestCompatibilityMaxDepth; i++ {
		nullable = nullableRequestSchema(nullable)
	}
	assertLimit(spec(map[string]any{}, 1), spec(nullable, 1))
	assertLimit(spec(map[string]any{}, 1), spec(map[string]any{"anyOf": make([]any, api.RequestCompatibilityMaxNodes+1)}, 1))
	props := map[string]any{}
	for i := 0; i <= api.RequestCompatibilityMaxNodes; i++ {
		props[fmt.Sprintf("f%d", i)] = map[string]any{}
	}
	assertLimit(spec(map[string]any{}, 1), spec(map[string]any{"properties": props}, 1))
	required := make([]any, 3000)
	for i := range required {
		required[i] = fmt.Sprintf("f%d", i)
	}
	assertLimit(spec(map[string]any{"type": "object"}, 2), spec(map[string]any{"type": "object", "required": required}, 2))
	values := make([]any, api.RequestCompatibilityMaxEnumValues)
	for i := range values {
		values[i] = strings.Repeat("a", api.RequestCompatibilityMaxMetadataBytes)
	}
	assertLimit(spec(map[string]any{}, 5), spec(map[string]any{"enum": values}, 5))
}

func TestCompareRequestsUnknownParameterIdentityCannotProveNewRequirements(t *testing.T) {
	required := map[string]any{"parameters": []any{requestTestParameter("currency", true, map[string]any{"type": "string"})}}
	for _, raw := range []any{nil, "invalid", []any{map[string]any{"$ref": "#/components/parameters/Missing"}}, []any{map[string]any{"name": 123, "in": "query"}}} {
		row := requestTestCompare(t, map[string]any{"parameters": raw}, required)
		if row.Complete || row.Status != "unknown" || requestTestHasCode(row, "parameter_required") {
			t.Fatalf("unresolved baseline produced a known break: %+v", row)
		}
	}
	optional := map[string]any{"parameters": []any{requestTestParameter("currency", false, map[string]any{"type": "string"})}}
	if row := requestTestCompare(t, optional, map[string]any{}); row.Complete || !requestTestHasCode(row, "parameter_removed") {
		t.Fatal(row)
	}
	path := requestTestParameter("id", true, map[string]any{"type": "string"})
	path["in"] = "path"
	if row := requestTestCompare(t, map[string]any{}, map[string]any{"parameters": []any{path}}); row.Status != "unknown" || requestTestHasCode(row, "parameter_required") {
		t.Fatalf("path segment already existed: %+v", row)
	}
}

func TestCompareRequestsDialectAndReferencedPathItems(t *testing.T) {
	before := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": "number"}), nil, nil)
	for _, dialect := range []any{"https://example.invalid/private-dialect", []any{"invalid"}, nil} {
		after := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": "integer"}), nil, nil)
		after.Raw["jsonSchemaDialect"] = dialect
		result, err := CompareRequests(before, after)
		if err != nil || len(result.Routes) != 1 || result.Routes[0].Status != "unknown" || !requestTestHasCode(result.Routes[0], "unsupported_schema_dialect") {
			t.Fatalf("custom dialect compared with default rules: %+v, %v", result, err)
		}
	}
	after := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": "integer"}), nil, nil)
	after.Raw["jsonSchemaDialect"] = "https://spec.openapis.org/oas/3.1/dialect/base"
	result, err := CompareRequests(before, after)
	if err != nil || result.Routes[0].Status != "breaking" {
		t.Fatalf("default dialect unresolved: %+v, %v", result, err)
	}
	after.Paths["/items/{id}"].Raw["$ref"] = "#/components/pathItems/Shared"
	if result, err := CompareRequests(before, after); err == nil || len(result.Routes) != 0 {
		t.Fatalf("referenced path item yielded partial compatibility: %+v, %v", result, err)
	}
}

func TestCompareRequestsReadOnlyRequiredFieldsIn30(t *testing.T) {
	before := requestTestSpec(t, "3.0.4", requestTestBody(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "readOnly": true}}, "required": []any{"id"}}), nil, nil)
	after := requestTestSpec(t, "3.0.4", requestTestBody(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []any{"id"}}), nil, nil)
	result, err := CompareRequests(before, after)
	if err != nil || !requestTestHasCode(result.Routes[0], "property_required") || result.Routes[0].Complete {
		t.Fatalf("response-only required field hid new request requirement: %+v, %v", result, err)
	}
}

func TestCompareRequestsLeavesSecurityToSeparateComparison(t *testing.T) {
	before := requestTestSpec(t, "3.1.0", map[string]any{"responses": map[string]any{}}, nil, nil)
	after := requestTestSpec(t, "3.1.0", map[string]any{"responses": map[string]any{}}, nil, nil)
	after.Raw["security"] = []any{map[string]any{"token": []any{}}}
	result, err := CompareRequests(before, after)
	if err != nil || result.Routes[0].Status != "no_supported_breaks" || result.Routes[0].Changed {
		t.Fatalf("security change polluted request shape comparison: %+v, %v", result, err)
	}
	before.Paths["/items/{id}"].Methods["post"].Raw["security"] = []any{}
	after.Paths["/items/{id}"].Methods["post"].Raw["security"] = []any{}
	result, err = CompareRequests(before, after)
	if err != nil || result.Routes[0].Status != "no_supported_breaks" || result.Routes[0].Changed {
		t.Fatalf("ignored root security became a change: %+v, %v", result, err)
	}
}

func TestCompareRequestsUnparsedOperationsAndInvalid30NullType(t *testing.T) {
	before := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": "string"}), nil, nil)
	for _, raw := range []any{nil, "invalid", map[string]any{"responses": map[string]any{}}} {
		after := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": "string"}), nil, map[string]any{"trace": raw})
		if result, err := CompareRequests(before, after); err == nil || len(result.Routes) != 0 {
			t.Fatalf("unparsed operation silently omitted: %+v, %v", result, err)
		}
	}
	before = requestTestSpec(t, "3.0.4", requestTestBody(map[string]any{"type": "string", "nullable": true}), nil, nil)
	after := requestTestSpec(t, "3.0.4", requestTestBody(map[string]any{"type": "null"}), nil, nil)
	result, err := CompareRequests(before, after)
	if err != nil || result.Routes[0].Status != "unknown" || !requestTestHasCode(result.Routes[0], "invalid_schema_type") {
		t.Fatalf("invalid 3.0 null type treated as a restriction: %+v, %v", result, err)
	}
}
