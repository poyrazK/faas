package openapidiff

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

const importedDocParams = `{
  "openapi": "3.0.3",
  "info": {"title": "Test API", "version": "1.0.0"},
  "paths": {
    "/users/{id}/orders": {
      "parameters": [{"$ref": "#/components/parameters/UserID"}],
      "get": {
        "parameters": [
          {"name": "limit", "in": "query", "required": true, "schema": {"type": "integer", "maximum": 100}},
          {"name": "tag", "in": "query", "schema": {"type": "array", "items": {"type": "string"}}},
          {"name": "filter", "in": "query", "schema": {"type": "object"}},
          {"name": "csv", "in": "query", "explode": false, "schema": {"type": "array", "items": {"type": "string"}}},
          {"name": "q", "in": "query", "content": {"application/json": {"schema": {"type": "object"}}}},
          {"name": "session", "in": "cookie", "schema": {"type": "string"}},
          {"name": "X-Request-Id", "in": "header", "schema": {"type": "string", "nullable": true}},
          {"name": "Authorization", "in": "header", "required": true, "schema": {"type": "string"}}
        ]
      }
    },
    "/health": {"get": {"parameters": [{"name": "session", "in": "cookie", "schema": {"type": "string"}}]}}
  },
  "components": {
    "parameters": {"UserID": {"name": "id", "in": "path", "required": true, "schema": {"$ref": "#/components/schemas/ID"}}},
    "schemas": {"ID": {"type": "integer", "minimum": 1}}
  }
}`

func TestComputeDryRun_ParameterSuggestions(t *testing.T) {
	out, err := ComputeDryRun([]byte(importedDocParams), nil)
	if err != nil {
		t.Fatalf("ComputeDryRun: %v", err)
	}
	// /health only has a cookie parameter, which the gateway cannot check.
	if len(out.Suggestions) != 1 || out.Suggestions[0].Path != "/users/?*/orders" {
		t.Fatalf("suggestions = %v", suggestionPaths(out.Suggestions))
	}
	validate := out.Suggestions[0].Action["validate"].(map[string]any)
	if _, hasBody := validate["schema"]; hasBody {
		t.Fatal("body-less GET got a body schema")
	}
	params := validate["parameters"].(*api.EdgeRuleValidateParameters)
	if params.PathTemplate != "/users/{id}/orders" {
		t.Fatalf("path_template = %q", params.PathTemplate)
	}
	// The generated rule must pass the same checks apid applies on create.
	if prob := params.Validate(out.Suggestions[0].Path); prob != nil {
		t.Fatalf("generated parameters rejected: %s", prob.Detail)
	}
	for location, wantKinds := range map[string]map[string]api.EdgeRuleParamKind{
		"path":    {"id": {Type: "integer"}},
		"query":   {"limit": {Type: "integer"}, "tag": {Type: "array", Item: "string"}},
		"headers": {"x-request-id": {Type: "string"}},
	} {
		raw := map[string]json.RawMessage{"path": params.Path, "query": params.Query, "headers": params.Headers}[location]
		kinds, err := api.EdgeRuleParamKinds(raw)
		if err != nil {
			t.Fatalf("%s kinds: %v (%s)", location, err, raw)
		}
		if len(kinds) != len(wantKinds) {
			t.Fatalf("%s kinds = %v, want %v", location, kinds, wantKinds)
		}
		for name, want := range wantKinds {
			if kinds[name] != want {
				t.Fatalf("%s %s kind = %v, want %v", location, name, kinds[name], want)
			}
		}
		if _, err := edgevalidate.Compile(raw, false); err != nil {
			t.Fatalf("%s schema does not compile: %v\n%s", location, err, raw)
		}
	}
	// The shared ID component keeps its constraint after dereferencing.
	compiled, _ := edgevalidate.Compile(params.Path, false)
	for value, valid := range map[string]bool{`{"id":7}`: true, `{"id":0}`: false} {
		if fieldErr, err := compiled.Validate([]byte(value)); err != nil || (fieldErr == nil) != valid {
			t.Errorf("path %s: want valid=%v, fieldErr=%v err=%v", value, valid, fieldErr, err)
		}
	}
	compiled, _ = edgevalidate.Compile(params.Query, false)
	if fieldErr, _ := compiled.Validate([]byte(`{"tag":["a"]}`)); fieldErr == nil {
		t.Error("missing required limit accepted")
	}
}
