package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestOpenAPIScaffoldServerAndAnonymousAccess(t *testing.T) {
	spec := `openapi: 3.1.0
servers: [{url: https://example.invalid/v1}]
security: [{bearer: []}, {}]
paths:
  /one:
    get:
      responses: {'200': {description: OK}}
  /two:
    servers: [{url: /v2}]
    get:
      servers: [{url: https://other.invalid/v3}]
      responses: {'200': {description: OK}}
  /private:
    get:
      security: [{bearer: []}]
      responses: {'200': {description: OK}}
`
	steps, skipped, err := main.ScaffoldTestHTTPRequests([]byte(spec))
	if err != nil || skipped != 1 || len(steps) != 2 || steps[0].Path != "/v1/one" || steps[1].Path != "/v3/two" {
		t.Fatalf("scaffold: steps=%+v skipped=%d error=%v", steps, skipped, err)
	}
	for _, server := range []string{"[{url: /one}, {url: /two}]", "[{url: 'https://example.invalid/{version}'}]", "[{url: '//other.invalid/path'}]"} {
		invalid := strings.Replace(spec, "[{url: https://example.invalid/v1}]", server, 1)
		if _, _, err := main.ScaffoldTestHTTPRequests([]byte(invalid)); err == nil {
			t.Fatalf("accepted ambiguous server %s", server)
		}
	}
}

func TestOpenAPIScaffoldEnforcesDeclaredJSONResponse(t *testing.T) {
	for _, response := range []string{
		"description: OK\n          content: {application/json: {example: {ok: true}}}",
		"description: OK\n          content: {application/json: {schema: true}}",
		"$ref: '#/components/responses/JSON'",
	} {
		t.Run(fmt.Sprintf("response-%d", len(response)), func(t *testing.T) {
			stdout, _ := main.CaptureSuiteTestOutput(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/health" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html>wrong</html>"))
			}))
			defer server.Close()
			dir := t.TempDir()
			specPath, manifestPath := filepath.Join(dir, "openapi.yaml"), filepath.Join(dir, "gregale-test.yaml")
			spec := "openapi: 3.1.0\nservers: [{url: " + server.URL + "/v1}]\nsecurity: [{}]\npaths:\n  /health:\n    get:\n      responses:\n        '200':\n          " + response + "\ncomponents:\n  responses:\n    JSON:\n      description: OK\n      content: {application/json: {}}\n"
			main.WriteSuiteTestFile(t, specPath, spec)
			if code := main.CmdTestInit([]string{"--from", specPath, "--project", "probe-app", "--output", manifestPath}); code != 0 {
				t.Fatal(code)
			}
			scenarios, _, err := main.ReadTestManifest(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			step := scenarios["api-smoke"].Requests[0]
			if step.Path != "/v1/health" || step.Expect.ContentType != "application/json" {
				t.Fatalf("generated step: %+v", step)
			}
			stdout.Reset()
			code, _ := main.RunWithStderr(t, func() int {
				return main.CmdTest([]string{"--manifest", manifestPath, "--scenario", "api-smoke", "--engine", "local", "--base-url", server.URL})
			})
			if code == 0 || !strings.Contains(stdout.String(), "expected content type application/json") {
				t.Fatalf("wrong response passed: %d %s", code, stdout)
			}
		})
	}
}
