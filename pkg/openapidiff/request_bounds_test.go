package openapidiff

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCompareRequestsValidationBounds(t *testing.T) {
	for _, test := range []struct {
		name          string
		before, after map[string]any
		code          string
	}{
		{"minimum added", map[string]any{"type": "number"}, map[string]any{"type": "number", "minimum": 1}, "numeric_minimum_restricted"},
		{"minimum raised", map[string]any{"type": "number", "minimum": 0}, map[string]any{"type": "number", "minimum": 1}, "numeric_minimum_restricted"},
		{"minimum lowered", map[string]any{"type": "number", "minimum": 1}, map[string]any{"type": "number", "minimum": 0}, ""},
		{"minimum removed", map[string]any{"type": "number", "minimum": 0}, map[string]any{"type": "number"}, ""},
		{"maximum lowered", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "numeric_maximum_restricted"},
		{"maximum raised", map[string]any{"type": "integer", "maximum": 20}, map[string]any{"type": "integer", "maximum": 100}, ""},
		{"number lower exclusive", map[string]any{"type": "number", "minimum": 1}, map[string]any{"type": "number", "exclusiveMinimum": 1}, "numeric_minimum_restricted"},
		{"number upper exclusive", map[string]any{"type": "number", "maximum": 1}, map[string]any{"type": "number", "exclusiveMaximum": 1}, "numeric_maximum_restricted"},
		{"integer equivalent lower", map[string]any{"type": "integer", "exclusiveMinimum": 0}, map[string]any{"type": "integer", "minimum": 1}, ""},
		{"integer equivalent upper", map[string]any{"type": "integer", "exclusiveMaximum": 2}, map[string]any{"type": "integer", "maximum": 1}, ""},
		{"fractional integer minimum equivalent", map[string]any{"type": "integer", "minimum": -1.5}, map[string]any{"type": "integer", "minimum": -1}, ""},
		{"negative exclusive integer maximum", map[string]any{"type": "integer", "exclusiveMaximum": -1}, map[string]any{"type": "integer", "maximum": -2}, ""},
		{"number singleton integer", map[string]any{"type": "number", "minimum": 1, "maximum": 1}, map[string]any{"type": "integer", "minimum": 1, "maximum": 1}, ""},
		{"number interval excludes integers", map[string]any{"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 1}, map[string]any{"type": "number", "minimum": 0.25, "maximum": 0.75}, "numeric_minimum_restricted"},
		{"strongest exclusive lower", map[string]any{"type": "number", "minimum": 1, "exclusiveMinimum": 0}, map[string]any{"type": "number", "minimum": 1}, ""},
		{"strongest exclusive upper", map[string]any{"type": "number", "maximum": 2, "exclusiveMaximum": 3}, map[string]any{"type": "number", "maximum": 2}, ""},
		{"lower length raised", map[string]any{"type": "string", "minLength": 1}, map[string]any{"type": "string", "minLength": 2}, "string_min_length_restricted"},
		{"upper length lowered", map[string]any{"type": "string", "maxLength": 100}, map[string]any{"type": "string", "maxLength": 20}, "string_max_length_restricted"},
		{"length widening", map[string]any{"type": "string", "minLength": 2, "maxLength": 10}, map[string]any{"type": "string", "minLength": 1, "maxLength": 20}, ""},
		{"lower items raised", map[string]any{"type": "array", "minItems": 0}, map[string]any{"type": "array", "minItems": 1}, "array_min_items_restricted"},
		{"upper items lowered", map[string]any{"type": "array", "maxItems": 10}, map[string]any{"type": "array", "maxItems": 2}, "array_max_items_restricted"},
		{"empty array only", map[string]any{"type": "array", "items": false}, map[string]any{"type": "array", "maxItems": 0}, ""},
		{"empty array ignores items", map[string]any{"type": "array", "maxItems": 0}, map[string]any{"type": "array", "maxItems": 0, "items": false}, ""},
		{"minimum empty default", map[string]any{"type": "array"}, map[string]any{"type": "array", "minItems": 0}, ""},
		{"bounds irrelevant to type", map[string]any{"type": "boolean", "minimum": 2, "maxLength": 1}, map[string]any{"type": "boolean"}, ""},
		{"contradictory baseline", map[string]any{"type": "integer", "minimum": 2, "maximum": 1}, map[string]any{"type": "integer", "minimum": 10}, ""},
		{"empty string domain", map[string]any{"type": "string"}, map[string]any{"type": "string", "minLength": 2, "maxLength": 1}, "schema_rejects_all"},
		{"required impossible field baseline", map[string]any{"type": "object", "properties": map[string]any{"field": map[string]any{"type": "string", "minLength": 2, "maxLength": 1}}, "required": []any{"field"}}, map[string]any{"type": "object", "required": []any{"new"}}, ""},
		{"impossible array nullable", map[string]any{"type": []any{"array", "null"}}, map[string]any{"type": []any{"array", "null"}, "items": false, "minItems": 1}, "accepted_type_narrowed"},
		{"large integer maximum", map[string]any{"type": "integer", "maximum": uint64(18446744073709551615)}, map[string]any{"type": "integer", "maximum": uint64(18446744073709551614)}, "numeric_maximum_restricted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := requestTestCompare(t, requestTestBody(test.before), requestTestBody(test.after))
			if !row.Complete || test.code != "" && !requestTestHasCode(row, test.code) || test.code == "" && row.Status != "no_supported_breaks" {
				t.Fatal(row)
			}
		})
	}
}

func TestCompareRequestsBoundsRespectFiniteEnums(t *testing.T) {
	for _, test := range []struct {
		name          string
		before, after map[string]any
		code          string
	}{
		{"range fully covered by enum", map[string]any{"type": "integer", "minimum": 1, "maximum": 3}, map[string]any{"type": "integer", "enum": []any{1, 2, 3}}, ""},
		{"range partially covered by enum", map[string]any{"type": "integer", "minimum": 1, "maximum": 3}, map[string]any{"type": "integer", "enum": []any{1, 3}}, "enum_values_restricted"},
		{"fractional bounds integer singleton", map[string]any{"type": "integer", "minimum": 1.5, "maximum": 2.5}, map[string]any{"type": "integer", "enum": []any{2}}, ""},
		{"numeric fractional singleton enum", map[string]any{"type": "number", "minimum": 1.5, "maximum": 1.5}, map[string]any{"type": "number", "enum": []any{1.5}}, ""},
		{"empty string singleton enum", map[string]any{"type": "string", "maxLength": 0}, map[string]any{"type": "string", "enum": []any{""}}, ""},
		{"numeric large range finite enum", map[string]any{"type": "integer", "minimum": 0, "maximum": uint64(18446744073709551615)}, map[string]any{"type": "integer", "enum": []any{0}}, "enum_values_restricted"},
		{"numeric enum inside bounds", map[string]any{"type": "integer", "enum": []any{1, 2}}, map[string]any{"type": "integer", "enum": []any{1, 2}, "minimum": 1, "maximum": 2}, ""},
		{"numeric enum removed and bounds added", map[string]any{"type": "integer", "enum": []any{1, 2}}, map[string]any{"type": "integer", "minimum": 1, "maximum": 2}, ""},
		{"numeric enum actually restricted", map[string]any{"type": "integer", "enum": []any{1, 2}}, map[string]any{"type": "integer", "minimum": 2}, "numeric_minimum_restricted"},
		{"old limits already excluded value", map[string]any{"type": "integer", "enum": []any{1, 2}, "minimum": 2}, map[string]any{"type": "integer", "enum": []any{2}}, ""},
		{"Unicode rune length", map[string]any{"type": "string", "enum": []any{"é", "😀"}}, map[string]any{"type": "string", "maxLength": 1}, ""},
		{"Unicode combining sequence", map[string]any{"type": "string", "enum": []any{"é", "e\u0301"}}, map[string]any{"type": "string", "maxLength": 1}, "string_max_length_restricted"},
		{"old string limits excluded enum", map[string]any{"type": "string", "enum": []any{"a", "abc"}, "minLength": 2}, map[string]any{"type": "string", "enum": []any{"abc"}}, ""},
		{"numeric enum retains null", map[string]any{"type": []any{"integer", "null"}, "enum": []any{nil, 1}}, map[string]any{"type": []any{"integer", "null"}, "minimum": 1}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := requestTestCompare(t, requestTestBody(test.before), requestTestBody(test.after))
			if !row.Complete || test.code != "" && !requestTestHasCode(row, test.code) || test.code == "" && row.Status != "no_supported_breaks" {
				t.Fatal(row)
			}
		})
	}
}

func TestCompareRequestsBoundsVersionsAndNoise(t *testing.T) {
	before := requestTestSpec(t, "3.0.4", requestTestBody(map[string]any{"type": "integer", "minimum": 0, "exclusiveMinimum": true, "maximum": 10, "exclusiveMaximum": true}), nil, nil)
	after := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": "integer", "minimum": 1, "maximum": 9}), nil, nil)
	result, err := CompareRequests(before, after)
	if err != nil || result.Routes[0].Changed || result.Routes[0].Status != "no_supported_breaks" {
		t.Fatalf("equivalent version-specific integer bounds: %+v %v", result, err)
	}
	row := requestTestCompare(t, requestTestBody(map[string]any{"type": "string"}), requestTestBody(map[string]any{"type": "string", "minLength": 0, "default": "private-default", "example": "private-example"}))
	if row.Changed || !row.Complete {
		t.Fatal(row)
	}
}

func TestCompareRequestsInvalidBoundsRemainUnknown(t *testing.T) {
	for _, test := range []struct {
		version, keyword string
		value            any
	}{
		{"3.1.0", "minimum", "private-bound"}, {"3.1.0", "maximum", nil}, {"3.1.0", "exclusiveMinimum", true},
		{"3.0.4", "exclusiveMaximum", 1}, {"3.0.4", "exclusiveMinimum", true},
		{"3.1.0", "minLength", -1}, {"3.1.0", "maxLength", 1.5}, {"3.1.0", "minItems", "private-bound"}, {"3.1.0", "maxItems", nil},
	} {
		before := requestTestSpec(t, test.version, requestTestBody(map[string]any{"type": "integer"}), nil, nil)
		after := requestTestSpec(t, test.version, requestTestBody(map[string]any{"type": "integer", test.keyword: test.value}), nil, nil)
		result, err := CompareRequests(before, after)
		if err != nil || result.Routes[0].Complete || result.Routes[0].Status != "unknown" {
			t.Fatalf("invalid %s %s claimed compatibility: %+v %v", test.version, test.keyword, result, err)
		}
		body, _ := json.Marshal(result)
		if strings.Contains(string(body), "private-bound") {
			t.Fatal("invalid bound value leaked")
		}
	}
}

func nullableRequestSchema(schema any) map[string]any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}

func TestCompareRequestsNullableUnions(t *testing.T) {
	before := nullableRequestSchema(map[string]any{"type": "integer", "minimum": 1, "maximum": 100})
	after := nullableRequestSchema(map[string]any{"type": "integer", "minimum": 1, "maximum": 20})
	row := requestTestCompare(t, requestTestBody(before), requestTestBody(after))
	if !row.Complete || !requestTestHasCode(row, "numeric_maximum_restricted") {
		t.Fatal(row)
	}
	row = requestTestCompare(t, requestTestBody(after), requestTestBody(before))
	if !row.Complete || row.Status != "no_supported_breaks" {
		t.Fatal(row)
	}
	row = requestTestCompare(t, requestTestBody(before), requestTestBody(map[string]any{"type": "integer", "minimum": 1, "maximum": 100}))
	if !row.Complete || !requestTestHasCode(row, "null_no_longer_allowed") {
		t.Fatal(row)
	}
	for _, schema := range []map[string]any{
		{"type": "string", "minLength": 1, "maxLength": 10},
		{"type": "integer", "enum": []any{1, 2}},
		{"type": "object", "properties": map[string]any{"field": map[string]any{"type": "string"}}, "required": []any{"field"}},
		{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 1},
	} {
		union := nullableRequestSchema(schema)
		alternative := map[string]any{}
		for key, value := range schema {
			alternative[key] = value
		}
		alternative["type"] = []any{schema["type"], "null"}
		if values, present := schema["enum"].([]any); present {
			alternative["enum"] = append(append([]any{}, values...), nil)
		}
		row := requestTestCompare(t, requestTestBody(union), requestTestBody(alternative))
		if !row.Complete || row.Changed || row.Status != "no_supported_breaks" {
			t.Fatalf("equivalent nullable union changed: %+v", row)
		}
	}
	before = nullableRequestSchema(map[string]any{"type": "integer"})
	after = map[string]any{"title": "noise", "anyOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "integer"}}}
	if row = requestTestCompare(t, requestTestBody(before), requestTestBody(after)); row.Changed || !row.Complete {
		t.Fatal(row)
	}
	if !reflect.DeepEqual(before, nullableRequestSchema(map[string]any{"type": "integer"})) {
		t.Fatal("union normalization mutated input")
	}
}

func TestCompareRequestsNullableReferenceAndUnsupportedCompositions(t *testing.T) {
	components := map[string]any{"schemas": map[string]any{"Amount": map[string]any{"type": "integer", "minimum": 0}}}
	before := requestTestSpec(t, "3.1.0", requestTestBody(nullableRequestSchema(map[string]any{"$ref": "#/components/schemas/Amount"})), components, nil)
	after := requestTestSpec(t, "3.1.0", requestTestBody(map[string]any{"type": []any{"integer", "null"}, "minimum": 0}), nil, nil)
	result, err := CompareRequests(before, after)
	if err != nil || result.Routes[0].Changed || !result.Routes[0].Complete {
		t.Fatalf("nullable local model not normalized: %+v %v", result, err)
	}
	for _, schema := range []any{
		map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}},
		map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}, map[string]any{"type": "null"}}},
		map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}, "maxLength": 1},
		nullableRequestSchema(map[string]any{"type": "string", "format": "email"}),
		map[string]any{"anyOf": "private-union"},
		map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
		map[string]any{"allOf": []any{map[string]any{"type": "integer"}}},
	} {
		row := requestTestCompare(t, requestTestBody(map[string]any{}), requestTestBody(schema))
		if row.Complete || row.Status != "unknown" {
			t.Fatal(row)
		}
	}
}

// The oracle uses direct comparisons on a finite grid covering every interval
// endpoint and both sides of it. It does not use the normalizer or bound helpers.
func TestCompareRequestsNumericIntervalsMatchAcceptanceOracle(t *testing.T) {
	type endpoint struct {
		set, exclusive bool
		value          float64
	}
	endpoints := []endpoint{{}}
	for _, value := range []float64{-1.5, -1, 0, 1, 1.5} {
		endpoints = append(endpoints, endpoint{set: true, value: value}, endpoint{set: true, value: value, exclusive: true})
	}
	for _, kind := range []string{"integer", "number"} {
		type interval struct {
			spec *Spec
			bits uint64
		}
		var variants []interval
		for _, lower := range endpoints {
			for _, upper := range endpoints {
				schema := map[string]any{"type": kind}
				for _, bound := range []struct {
					point endpoint
					name  string
				}{{lower, "Minimum"}, {upper, "Maximum"}} {
					if bound.point.set {
						keyword := strings.ToLower(bound.name[:1]) + bound.name[1:]
						if bound.point.exclusive {
							keyword = "exclusive" + bound.name
						}
						schema[keyword] = bound.point.value
					}
				}
				var bits uint64
				for sample := 0; sample <= 24; sample++ {
					value := float64(sample-12) / 4
					accepted := kind == "number" || value == float64(int(value))
					accepted = accepted && (!lower.set || value > lower.value || value == lower.value && !lower.exclusive)
					accepted = accepted && (!upper.set || value < upper.value || value == upper.value && !upper.exclusive)
					if accepted {
						bits |= 1 << sample
					}
				}
				variants = append(variants, interval{requestTestSpec(t, "3.1.0", requestTestBody(schema), nil, nil), bits})
			}
		}
		for i, before := range variants {
			for j, after := range variants {
				result, err := CompareRequests(before.spec, after.spec)
				want := before.bits&^after.bits != 0
				if err != nil || len(result.Routes) != 1 || !result.Routes[0].Complete || (result.Routes[0].Status == "breaking") != want {
					t.Fatalf("%s interval %d -> %d want restriction %t: %+v %v", kind, i, j, want, result, err)
				}
			}
		}
	}
}

func TestCompareRequestsNullableEmptyBranchAcceptsOnlyNull(t *testing.T) {
	for _, empty := range []any{false, map[string]any{"enum": []any{}}, map[string]any{"type": "integer", "minimum": 2, "maximum": 1}} {
		row := requestTestCompare(t, requestTestBody(nullableRequestSchema(empty)), requestTestBody(map[string]any{"type": "null"}))
		if !row.Complete || row.Status != "no_supported_breaks" || row.Changed {
			t.Fatalf("empty branch widened the nullable schema: %+v", row)
		}
		row = requestTestCompare(t, requestTestBody(map[string]any{"type": "null"}), requestTestBody(nullableRequestSchema(empty)))
		if !row.Complete || row.Status != "no_supported_breaks" {
			t.Fatal(row)
		}
	}
}

func TestCompareRequestsNestedNullableBounds(t *testing.T) {
	before := requestTestBody(map[string]any{"type": "object", "properties": map[string]any{"lines": map[string]any{"type": "array", "items": nullableRequestSchema(map[string]any{"type": "string", "maxLength": 100})}}})
	after := requestTestBody(map[string]any{"type": "object", "properties": map[string]any{"lines": map[string]any{"type": "array", "items": nullableRequestSchema(map[string]any{"type": "string", "maxLength": 20})}}})
	row := requestTestCompare(t, before, after)
	if !row.Complete || !requestTestHasCode(row, "string_max_length_restricted") {
		t.Fatal(row)
	}
	if row.Findings[0].Location != "/requestBody/content/application~1json/schema/properties/lines/items/maxLength" {
		t.Fatal(row.Findings)
	}
}
