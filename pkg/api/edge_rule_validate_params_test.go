package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEdgeRuleValidateParametersValidate(t *testing.T) {
	pathSchema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`)
	ok := []struct {
		name      string
		matchPath string
		p         *EdgeRuleValidateParameters
	}{
		{"nil", "/x", nil},
		{"query only", "/x", &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"},"tag":{"type":"array","items":{"type":"string"}}}}`)}},
		{"headers", "/x", &EdgeRuleValidateParameters{Headers: json.RawMessage(`{"type":"object","properties":{"x-tenant":{}}}`)}},
		{"path", "/users/?*", &EdgeRuleValidateParameters{PathTemplate: "/users/{id}", Path: pathSchema}},
		{"nullable 3.0 output", "/x", &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"q":{"type":["string","null"]}}}`)}},
	}
	for _, tc := range ok {
		if prob := tc.p.Validate(tc.matchPath); prob != nil {
			t.Errorf("%s: %v", tc.name, prob.Detail)
		}
	}
	bad := []struct {
		name, matchPath, wantDetail string
		p                           *EdgeRuleValidateParameters
	}{
		{"not an object schema", "/x", `"type": "object"`, &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"string"}`)}},
		{"no properties", "/x", "at least one property", &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object"}`)}},
		{"object property", "/x", "type must be", &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"f":{"type":"object"}}}`)}},
		{"array of objects", "/x", "array items", &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"f":{"type":"array","items":{"type":"object"}}}}`)}},
		{"uppercase header", "/x", "lowercase", &EdgeRuleValidateParameters{Headers: json.RawMessage(`{"type":"object","properties":{"X-Tenant":{}}}`)}},
		{"external ref", "/x", "external $ref", &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"https://evil.example/s.json"}}}`)}},
		{"path without template", "/users/?*", "path_template", &EdgeRuleValidateParameters{Path: pathSchema}},
		{"template without path", "/users/?*", "needs parameters.path", &EdgeRuleValidateParameters{PathTemplate: "/users/{id}", Query: json.RawMessage(`{"type":"object","properties":{"a":{}}}`)}},
		{"match path disagrees", "/users/*", `match_path must be "/users/?*"`, &EdgeRuleValidateParameters{PathTemplate: "/users/{id}", Path: pathSchema}},
		{"properties disagree", "/users/?*/x/?*", "must match the path_template placeholders", &EdgeRuleValidateParameters{PathTemplate: "/users/{id}/x/{n}", Path: pathSchema}},
		{"two placeholders in a segment", "/a/?*-?*", "more than one placeholder", &EdgeRuleValidateParameters{PathTemplate: "/a/{x}-{y}", Path: json.RawMessage(`{"type":"object","properties":{"x":{},"y":{}}}`)}},
	}
	for _, tc := range bad {
		prob := tc.p.Validate(tc.matchPath)
		if prob == nil || !strings.Contains(prob.Detail, tc.wantDetail) {
			t.Errorf("%s: got %v, want detail containing %q", tc.name, prob, tc.wantDetail)
		}
	}
}

func TestEdgeRuleValidateActionAllowsParameterOnlyRules(t *testing.T) {
	params := &EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"a":{}}}`)}
	if prob := (&EdgeRuleValidateAction{Parameters: params}).Validate(); prob != nil {
		t.Fatalf("parameter-only rule rejected: %v", prob.Detail)
	}
	if prob := (&EdgeRuleValidateAction{}).Validate(); prob == nil || !strings.Contains(prob.Detail, "schema or parameters") {
		t.Fatalf("empty rule = %v", prob)
	}
}

func TestEdgeRuleParamInstance(t *testing.T) {
	kinds := map[string]EdgeRuleParamKind{
		"limit": {Type: "integer"}, "ratio": {Type: "number"}, "dry": {Type: "boolean"},
		"tag": {Type: "array", Item: "integer"}, "name": {Type: "string"},
	}
	raw, err := EdgeRuleParamInstance(kinds, map[string][]string{
		"limit": {"10"}, "ratio": {"0.5"}, "dry": {"true"}, "tag": {"1", "x"},
		"name": {"007"}, "extra": {"a", "b"}, "bad": {"1"}, "empty": {},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"limit": 10.0, "ratio": 0.5, "dry": true, "tag": []any{1.0, "x"},
		"name": "007", "extra": []any{"a", "b"}, "bad": "1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("instance = %v, want %v", got, want)
	}
	// A value that does not parse as its declared type stays a string so the
	// schema reports the mismatch.
	raw, _ = EdgeRuleParamInstance(kinds, map[string][]string{"limit": {"1.5"}, "ratio": {"NaN"}, "dry": {"yes"}})
	if string(raw) != `{"dry":"yes","limit":"1.5","ratio":"NaN"}` {
		t.Fatalf("unparsable values = %s", raw)
	}
}

func TestPathTemplateValues(t *testing.T) {
	for _, tc := range []struct {
		template, path string
		want           map[string]string
	}{
		{"/users/{id}", "/users/7", map[string]string{"id": "7"}},
		{"/users/{id}/orders/{n}", "/users/a%2Fb/orders/3", map[string]string{"id": "a/b", "n": "3"}},
		{"/files/{name}.json", "/files/report.json", map[string]string{"name": "report"}},
	} {
		got, ok := PathTemplateValues(tc.template, tc.path)
		if !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("PathTemplateValues(%q, %q) = %v, %v; want %v", tc.template, tc.path, got, ok, tc.want)
		}
	}
	for _, tc := range [][2]string{
		{"/users/{id}", "/users"}, {"/users/{id}", "/users/7/x"}, {"/users/{id}", "/people/7"},
		{"/files/{name}.json", "/files/.json"}, {"/files/{name}.json", "/files/report.txt"},
	} {
		if got, ok := PathTemplateValues(tc[0], tc[1]); ok {
			t.Errorf("PathTemplateValues(%q, %q) = %v, want no fit", tc[0], tc[1], got)
		}
	}
}
