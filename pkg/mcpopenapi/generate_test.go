package mcpopenapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/mcpopenapi"
)

func document(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/reports.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func options() mcpopenapi.Options {
	return mcpopenapi.Options{ServerURL: "https://api.example.com/v1", OperationIDs: []string{"getReport", "createReport"}}
}

func TestGenerateOpenAPIPublicProject(t *testing.T) {
	data := document(t)
	project, err := mcpopenapi.Generate(data, options())
	if err != nil {
		t.Fatal(err)
	}
	reversed := options()
	reversed.OperationIDs = []string{"createReport", "getReport"}
	again, err := mcpopenapi.Generate(data, reversed)
	if err != nil || !reflect.DeepEqual(project.Files, again.Files) {
		t.Fatal("generation depends on selection order", err)
	}
	digest := sha256.Sum256(data)
	if project.Manifest.DocumentSHA256 != hex.EncodeToString(digest[:]) || project.Manifest.ServerURL != options().ServerURL {
		t.Fatal("document or explicit server binding was lost")
	}
	if len(project.Manifest.Operations) != 2 || project.Manifest.Operations[0].ID != "createReport" || project.Manifest.Operations[1].ResponseSchema["type"] != "object" {
		t.Fatal("selected operations or resolved response schema were lost")
	}
	config, err := mcphosting.Decode(bytes.NewReader(project.Files["gregale-mcp.json"]))
	if err != nil || config.Auth.Mode != "open" || len(config.Auth.ToolScopes) != 2 || len(config.Auth.ResourceScopes) != 0 {
		t.Fatal("generated hosting policy", err)
	}
	if _, exists := project.Files["gregale-mcp.tests.json"]; exists {
		t.Fatal("generation supplied apparent passing behavioral evidence")
	}
	if _, err := mcphosting.LoadSmokeSuite(bytes.NewReader(project.Files["gregale-mcp.tests.example.json"])); err == nil {
		t.Fatal("unfinished fixture examples were publishable")
	}
	project.Files["openapi-tools.js"][0] = '!'
	fresh, err := mcpopenapi.Generate(data, options())
	if err != nil || fresh.Files["openapi-tools.js"][0] == '!' {
		t.Fatal("a returned source buffer could poison later generation", err)
	}
}

func TestGenerateOpenAPIYAMLAndNullable(t *testing.T) {
	data := []byte(`openapi: 3.0.3
paths:
  /status:
    get:
      operationId: getStatus
      responses:
        200:
          content:
            application/json:
              schema:
                type: string
                nullable: true
`)
	project, err := mcpopenapi.Generate(data, mcpopenapi.Options{ServerURL: "https://api.example.com", OperationIDs: []string{"getStatus"}})
	if err != nil || project.Manifest.Operations[0].ResponseSchema["nullable"] != true {
		t.Fatal("YAML, unquoted status code or nullable support failed", err)
	}
}

func changedDocument(t *testing.T, change func(map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(document(t), &root); err != nil {
		t.Fatal(err)
	}
	change(root)
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func getOperation(root map[string]any) map[string]any {
	return root["paths"].(map[string]any)["/reports/{id}"].(map[string]any)["get"].(map[string]any)
}

func responseSchema(root map[string]any) map[string]any {
	return root["components"].(map[string]any)["schemas"].(map[string]any)["Report"].(map[string]any)
}

func TestGenerateOpenAPIRefusesAmbiguousOrUnsupportedContracts(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"private operation", func(r map[string]any) { getOperation(r)["security"] = []any{map[string]any{"bearer": []any{}}} }},
		{"invalid null security override", func(r map[string]any) {
			r["security"] = []any{map[string]any{"bearer": []any{}}}
			getOperation(r)["security"] = nil
		}},
		{"duplicate operation ID", func(r map[string]any) {
			r["paths"].(map[string]any)["/duplicate"] = map[string]any{"get": getOperation(r)}
		}},
		{"remote schema reference", func(r map[string]any) {
			r["components"].(map[string]any)["schemas"].(map[string]any)["Report"] = map[string]any{"$ref": "https://api.example.com/schema.json"}
		}},
		{"recursive schema reference", func(r map[string]any) {
			responseSchema(r)["properties"].(map[string]any)["child"] = map[string]any{"$ref": "#/components/schemas/Report"}
		}},
		{"reference sibling", func(r map[string]any) {
			r["components"].(map[string]any)["schemas"].(map[string]any)["Report"] = map[string]any{"$ref": "#/components/schemas/Other", "type": "object"}
		}},
		{"composition", func(r map[string]any) { responseSchema(r)["allOf"] = []any{map[string]any{"type": "object"}} }},
		{"custom dialect", func(r map[string]any) { r["jsonSchemaDialect"] = "https://api.example.com/custom-dialect" }},
		{"3.1 nullable keyword", func(r map[string]any) {
			responseSchema(r)["properties"].(map[string]any)["id"].(map[string]any)["nullable"] = true
		}},
		{"directional property", func(r map[string]any) {
			responseSchema(r)["properties"].(map[string]any)["id"].(map[string]any)["readOnly"] = true
		}},
		{"regex constraint", func(r map[string]any) {
			responseSchema(r)["properties"].(map[string]any)["id"].(map[string]any)["pattern"] = "(a+)+$"
		}},
		{"prototype property", func(r map[string]any) {
			responseSchema(r)["properties"].(map[string]any)["__proto__"] = map[string]any{"type": "string"}
		}},
		{"undeclared required property", func(r map[string]any) { responseSchema(r)["required"] = []any{"missing"} }},
		{"additional properties schema", func(r map[string]any) { responseSchema(r)["additionalProperties"] = map[string]any{"type": "string"} }},
		{"contradictory bounds", func(r map[string]any) {
			s := responseSchema(r)["properties"].(map[string]any)["id"].(map[string]any)
			s["minLength"], s["maxLength"] = 5, 1
		}},
		{"enum type mismatch", func(r map[string]any) {
			responseSchema(r)["properties"].(map[string]any)["count"].(map[string]any)["enum"] = []any{"text"}
		}},
		{"missing path parameter", func(r map[string]any) {
			r["paths"].(map[string]any)["/reports/{id}"].(map[string]any)["parameters"] = []any{}
		}},
		{"header parameter", func(r map[string]any) {
			getOperation(r)["parameters"] = []any{map[string]any{"name": "Authorization", "in": "header", "schema": map[string]any{"type": "string"}}}
		}},
		{"non-JSON response", func(r map[string]any) {
			getOperation(r)["responses"] = map[string]any{"200": map[string]any{"content": map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}}}}
		}},
		{"multiple success responses", func(r map[string]any) {
			responses := getOperation(r)["responses"].(map[string]any)
			responses["201"] = responses["200"]
		}},
		{"wildcard success", func(r map[string]any) {
			responses := getOperation(r)["responses"].(map[string]any)
			responses["2XX"] = responses["200"]
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := mcpopenapi.Generate(changedDocument(t, tc.change), options()); err == nil {
				t.Fatal("unsupported contract was silently accepted")
			}
		})
	}
}

func TestGenerateOpenAPIPublicOverrideAndOperationParameterOverride(t *testing.T) {
	data := changedDocument(t, func(r map[string]any) {
		r["security"] = []any{map[string]any{"bearer": []any{}}}
		op := getOperation(r)
		op["security"] = []any{}
		op["parameters"] = []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer", "minimum": 1}}}
	})
	project, err := mcpopenapi.Generate(data, mcpopenapi.Options{ServerURL: options().ServerURL, OperationIDs: []string{"getReport"}})
	if err != nil || project.Manifest.Operations[0].Parameters[0].Schema["type"] != "integer" {
		t.Fatal("explicit public or path parameter override failed", err)
	}
}

func TestGenerateOpenAPINeverFetchesReferences(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	data := changedDocument(t, func(r map[string]any) {
		r["components"].(map[string]any)["schemas"].(map[string]any)["Report"] = map[string]any{"$ref": server.URL + "/schema"}
	})
	if _, err := mcpopenapi.Generate(data, options()); err == nil || requests.Load() != 0 {
		t.Fatal("reference was fetched or accepted", err)
	}
}

func TestGenerateOpenAPIInputBoundsAndParserRefusals(t *testing.T) {
	for name, data := range map[string][]byte{
		"oversize":           bytes.Repeat([]byte(" "), api.ToolOpenAPIDocumentMaxBytes+1),
		"duplicate JSON":     []byte(`{"openapi":"3.1.0","openapi":"3.0.3","paths":{}}`),
		"duplicate YAML":     []byte("openapi: 3.1.0\nopenapi: 3.0.3\npaths: {}\n"),
		"alias":              []byte("openapi: 3.1.0\npaths: &paths {}\ncopy: *paths\n"),
		"multiple documents": []byte("openapi: 3.1.0\npaths: {}\n---\nopenapi: 3.1.0\n"),
		"custom tag":         []byte("openapi: !custom 3.1.0\npaths: {}\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mcpopenapi.Generate(data, options()); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, ids := range [][]string{nil, {"getReport", "getReport"}, {"missing"}, {"privateReport"}, {"getReport,createReport"}, {"../escape"}, {"__proto__"}} {
		opts := options()
		opts.OperationIDs = ids
		if _, err := mcpopenapi.Generate(document(t), opts); err == nil {
			t.Fatalf("invalid selection %v accepted", ids)
		}
	}
}

func TestGenerateOpenAPIExplicitPublicTarget(t *testing.T) {
	for _, target := range []string{
		"http://api.example.com", "https://user:secret@api.example.com", "https://api.example.com?q=secret",
		"https://api.example.com/#fragment", "https://api.example.com:8443", "https://localhost", "https://api.local",
		"https://127.0.0.1", "https://127.1", "https://10.1.2.3", "https://100.64.0.1", "https://169.254.169.254",
		"https://192.0.2.1", "https://[::1]", "https://[::ffff:127.0.0.1]", "https://[2002:7f00:1::]", "https://[3fff::1]",
		"https://api.example.com/a/../b", "https://api.example.com/%2e%2e", "https://api.example.com./",
	} {
		t.Run(target, func(t *testing.T) {
			opts := options()
			opts.ServerURL = target
			if _, err := mcpopenapi.Generate(document(t), opts); err == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
	for _, target := range []string{"https://api.example.com/v1", "https://8.8.8.8", "https://192.2.2.2", "https://192.0.3.1", "https://[2606:4700:4700::1111]", "https://[2001:67c::1]"} {
		opts := options()
		opts.ServerURL = target
		if _, err := mcpopenapi.Generate(document(t), opts); err != nil {
			t.Fatalf("public target %s refused: %v", target, err)
		}
	}
}
