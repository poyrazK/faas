package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestOpenAPIScaffoldResolvesPathItemReferences(t *testing.T) {
	spec := `openapi: 3.1.0
info: {title: Example, version: '1'}
paths:
  /health:
    get:
      responses: {'200': {description: OK}}
  /other:
    $ref: '#/components/pathItems/First'
    description: A referenced operation
  /another:
    $ref: '#/paths/~1other'
components:
  pathItems:
    First:
      $ref: '#/components/pathItems/%53econd'
    Second:
      servers: [{url: /v1}]
      get:
        responses:
          '200': {$ref: '#/components/responses/JSON'}
      post:
        responses: {'200': {description: OK}}
      trace:
        responses: {'200': {description: OK}}
  responses:
    JSON:
      description: OK
      content: {application/json: {}}
`
	steps, skipped, err := main.ScaffoldTestHTTPRequests([]byte(spec))
	if err != nil || len(steps) != 3 || skipped != 4 {
		t.Fatalf("scaffold: %+v skipped=%d error=%v", steps, skipped, err)
	}
	for i, path := range []string{"/v1/another", "/health", "/v1/other"} {
		if steps[i].Path != path {
			t.Fatalf("step %d path = %q", i, steps[i].Path)
		}
		if i != 1 && steps[i].Expect.ContentType != "application/json" {
			t.Fatalf("referenced JSON contract missing: %+v", steps[i])
		}
	}
	if err := main.ValidateTestHTTPRequests(main.TestScenario{Requests: steps}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if _, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, steps, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPIScaffoldRejectsUnsafePathItemReferences(t *testing.T) {
	for _, tc := range []struct{ name, item, components string }{
		{"external", "$ref: 'https://example.invalid/openapi.yaml#/paths/~1x'", ""},
		{"missing", "$ref: '#/components/pathItems/Missing'", ""},
		{"invalid pointer", "$ref: '#/components/pathItems/~2broken'", ""},
		{"invalid percent escape", "$ref: '#/components/pathItems/%ZZ'", ""},
		{"cycle", "$ref: '#/components/pathItems/A'", "A: {$ref: '#/components/pathItems/B'}\n    B: {$ref: '#/components/pathItems/A'}"},
		{"not a path item", "$ref: '#/components/pathItems/A'", "A: {type: object}"},
		{"conflicting sibling", "$ref: '#/components/pathItems/A'\n    get: {responses: {'200': {description: other}}}", "A: {get: {responses: {'200': {description: OK}}}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := "openapi: 3.1.0\npaths:\n  /health:\n    get: {responses: {'200': {description: OK}}}\n  /reference:\n    " + tc.item + "\ncomponents:\n  pathItems:\n    " + tc.components + "\n"
			if _, _, err := main.ScaffoldTestHTTPRequests([]byte(spec)); err == nil {
				t.Fatalf("unsafe reference accepted: %s", spec)
			}
		})
	}
	var parts strings.Builder
	parts.WriteString("openapi: 3.1.0\npaths:\n  /reference: {$ref: '#/components/pathItems/P0'}\ncomponents:\n  pathItems:\n")
	for i := 0; i < 101; i++ {
		_, _ = fmt.Fprintf(&parts, "    P%d: {$ref: '#/components/pathItems/P%d'}\n", i, i+1)
	}
	parts.WriteString("    P101: {get: {responses: {'200': {description: OK}}}}\n")
	if _, _, err := main.ScaffoldTestHTTPRequests([]byte(parts.String())); err == nil {
		t.Fatal("excessively deep path references accepted")
	}
	dir := t.TempDir()
	from, output := filepath.Join(dir, "openapi.yaml"), filepath.Join(dir, "scenario.yaml")
	main.WriteSuiteTestFile(t, from, "openapi: 3.1.0\npaths:\n  /health: {get: {responses: {'200': {description: OK}}}}\n  /missing: {$ref: '#/components/pathItems/Missing'}\n")
	main.CaptureSuiteTestOutput(t)
	if code := main.CmdTestInit([]string{"--from", from, "--project", "test-app", "--output", output}); code == 0 {
		t.Fatal("unresolved path item was accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed import left output: %v", err)
	}
}

func TestOpenAPIScaffoldReferencedOperationsRetainRestrictions(t *testing.T) {
	spec := `openapi: 3.1.0
paths:
  /health: {get: {responses: {'200': {description: OK}}}}
  /private: {$ref: '#/components/pathItems/Private'}
  /query: {$ref: '#/components/pathItems/Query'}
  /body: {$ref: '#/components/pathItems/Body'}
components:
  pathItems:
    Private:
      get:
        security: [{bearer: []}]
        responses: {'200': {description: OK}}
    Query:
      parameters: [{name: q, in: query, required: true, schema: {type: string}}]
      get:
        responses: {'200': {description: OK}}
    Body:
      get:
        requestBody: {required: true, content: {application/json: {schema: {type: object}}}}
        responses: {'200': {description: OK}}
`
	steps, skipped, err := main.ScaffoldTestHTTPRequests([]byte(spec))
	if err != nil || len(steps) != 1 || steps[0].Path != "/health" || skipped != 3 {
		t.Fatalf("referenced restrictions were lost: %+v skipped=%d error=%v", steps, skipped, err)
	}
}

func TestOpenAPIScaffoldSkipsRequiredAndReferencedGETBodies(t *testing.T) {
	spec := `openapi: 3.1.0
paths:
  /required:
    get:
      requestBody: {required: true, content: {application/json: {schema: {type: object}}}}
      responses: {'200': {description: OK}}
  /referenced:
    get:
      requestBody: {$ref: '#/components/requestBodies/Body'}
      responses: {'200': {description: OK}}
  /invalid:
    get:
      requestBody: {required: 'true'}
      responses: {'200': {description: OK}}
  /null-ref:
    get:
      requestBody: {$ref: null}
      responses: {'200': {description: OK}}
  /optional:
    get:
      requestBody: {required: false, content: {application/json: {schema: {type: object}}}}
      responses: {'200': {description: OK}}
  /health:
    get:
      responses: {'200': {description: OK}}
components:
  requestBodies:
    Body: {required: true, content: {application/json: {schema: {type: object}}}}
`
	steps, skipped, err := main.ScaffoldTestHTTPRequests([]byte(spec))
	if err != nil || skipped != 4 || len(steps) != 2 || steps[0].Path != "/health" || steps[1].Path != "/optional" {
		t.Fatalf("scaffold: %+v skipped=%d error=%v", steps, skipped, err)
	}
	dir := t.TempDir()
	from, output := filepath.Join(dir, "openapi.yaml"), filepath.Join(dir, "scenario.yaml")
	main.WriteSuiteTestFile(t, from, spec)
	main.CaptureSuiteTestOutput(t)
	if code := main.CmdTestInit([]string{"--from", from, "--project", "test-app", "--output", output}); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	manifest, _, err := main.ReadTestManifest(output)
	if err != nil || len(manifest["api-smoke"].Requests) != 2 {
		t.Fatalf("generated manifest: %v %+v", err, manifest)
	}
}
