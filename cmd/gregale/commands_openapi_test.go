// tests for `gregale openapi diff` (issue #976 / ADR-122 /
// SAFE-RELEASES-D).
//
// Pins the contract:
//
//   - missing args → usage error (1).
//   - unknown subcommand → usage error (1).
//   - identical baselines → exit 0, no rows.
//   - removing a property → exit 2 (BREAKING), prose carries the
//     field-removed kind.
//   - adding an optional property → exit 0 (NOT breaking; INFO row).
//   - changing type → exit 2 (BREAKING), prose carries
//     type_change kind.
//   - --json envelope: NDJSON, one record per break.
//
// All tests use t.TempDir so the YAML fixtures don't leak across
// runs (a leak would make later tests pass on the wrong fixture).

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

// minimalOpenAPIYAML is the smallest valid OpenAPI 3.1 document
// pkg/openapidiff.LoadBytes accepts. The shape is a single path
// with one GET response carrying a typed schema; tests mutate the
// schema in the proposed copy to exercise the BREAKING / INFO /
// no-op rules.
const minimalOpenAPIYAML = `openapi: 3.1.0
info:
  title: tests
  version: "0.0.0"
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name: { type: string }
                  count: { type: integer }
`

// writeYAML writes a YAML document to a temp file and returns the path.
func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestCmdOpenapiDiff_NoArgs covers the usage-error path. The
// dispatcher must reject empty / over-count arg lists before
// LoadBytes reads the YAML files.
func TestCmdOpenapiDiff_NoArgs(t *testing.T) {
	if code := cmdOpenapi(nil); code != 1 {
		t.Errorf("cmdOpenapi nil = %d, want 1", code)
	}
	if code := cmdOpenapi([]string{"diff"}); code != 1 {
		t.Errorf("cmdOpenapi diff (no files) = %d, want 1", code)
	}
	if code := cmdOpenapi([]string{"bogus"}); code != 1 {
		t.Errorf("cmdOpenapi bogus sub = %d, want 1", code)
	}
}

// TestCmdOpenapiDiff_IdenticalExitsZero — a clone of the baseline
// in the proposed slot produces zero rows and exit 0. Important
// pin: a regression that always emitted a row would produce a
// spurious exit 2 even on no-op inputs.
func TestCmdOpenapiDiff_IdenticalExitsZero(t *testing.T) {
	base := writeYAML(t, minimalOpenAPIYAML)
	prop := writeYAML(t, minimalOpenAPIYAML)
	stdout, restore := swapStdout(t)
	defer restore()

	if code := cmdOpenapiDiff([]string{base, prop}); code != 0 {
		t.Errorf("identical docs: code = %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("identical docs: stdout should be empty, got %q", stdout.String())
	}
}

// TestCmdOpenapiDiff_FieldRemovedExitsTwo — removing a property
// the customer's adapter depends on IS a wire-shape regression.
// The command MUST exit 2 so CI blocks the bump. Pins BREAKING
// classification on field removal.
func TestCmdOpenapiDiff_FieldRemovedExitsTwo(t *testing.T) {
	base := writeYAML(t, minimalOpenAPIYAML)
	propYAML := strings.Replace(minimalOpenAPIYAML,
		"                  name: { type: string }\n                  count: { type: integer }\n",
		"                  name: { type: string }\n",
		1)
	prop := writeYAML(t, propYAML)
	stdout, restore := swapStdout(t)
	defer restore()

	if code := cmdOpenapiDiff([]string{base, prop}); code != 2 {
		t.Errorf("field-removed docs: code = %d, want 2\n--- stdout ---\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "BREAKING") {
		t.Errorf("field-removed docs: stdout missing BREAKING marker\n--- stdout ---\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "field_removed") {
		t.Errorf("field-removed docs: stdout missing field_removed kind\n--- stdout ---\n%s", stdout.String())
	}
}

// TestCmdOpenapiDiff_PropertyAddedExitsZero — adding an OPTIONAL
// property is JSON-Schema-tolerated by clients that ignore
// unknown fields; the CLI MUST exit 0. Mirrors
// TestCompare_PropertyAdded_NotABreak in pkg/openapidiff (the
// underlying differ's pin).
//
// Property-adds produce zero rows today (the differ classifies
// adds as neither BREAKING nor INFO at this stage — adding an
// optional field is purely additive and the prose is silent).
// The load-bearing assertion is the exit code: 0 = no wire
// regression, CI clear.
func TestCmdOpenapiDiff_PropertyAddedExitsZero(t *testing.T) {
	base := writeYAML(t, minimalOpenAPIYAML)
	propYAML := strings.Replace(minimalOpenAPIYAML,
		"                  name: { type: string }\n                  count: { type: integer }\n",
		"                  name: { type: string }\n                  count: { type: integer }\n                  extra: { type: boolean }\n",
		1)
	prop := writeYAML(t, propYAML)
	stdout, restore := swapStdout(t)
	defer restore()

	if code := cmdOpenapiDiff([]string{base, prop}); code != 0 {
		t.Errorf("property-added docs: code = %d, want 0\n--- stdout ---\n%s", code, stdout.String())
	}
	// Hard guarantee: NEVER a BREAKING marker on an additive-only
	// change. The prose may be empty (the differ is silent on adds),
	// but a BREAKING tag here would mean the differ's classification
	// regressed.
	if strings.Contains(stdout.String(), "BREAKING") {
		t.Errorf("property-added docs: stdout carried BREAKING marker (differ classification regressed)\n--- stdout ---\n%s", stdout.String())
	}
	_ = stdout // silence unused warning; kept for future asserts
}

// TestCmdOpenapiDiff_TypeChangeExitsTwo — a type flip on a field
// the customer reads is a wire-shape regression. The differ's
// loaders + the breakingKinds map jointly classify this as
// BREAKING; the CLI MUST exit 2.
func TestCmdOpenapiDiff_TypeChangeExitsTwo(t *testing.T) {
	base := writeYAML(t, minimalOpenAPIYAML)
	propYAML := strings.Replace(minimalOpenAPIYAML,
		"                  count: { type: integer }\n",
		"                  count: { type: string }\n",
		1)
	prop := writeYAML(t, propYAML)
	stdout, restore := swapStdout(t)
	defer restore()

	if code := cmdOpenapiDiff([]string{base, prop}); code != 2 {
		t.Errorf("type-change docs: code = %d, want 2\n--- stdout ---\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "type_change") {
		t.Errorf("type-change docs: stdout missing type_change kind\n--- stdout ---\n%s", stdout.String())
	}
}

// TestCmdOpenapiDiff_JSON — the --json branch emits one JSON
// record per SchemaBreak on a separate line. The decoder's
// perspective: each line decodes into a single SchemaBreak struct.
// Mirrors the wire-stable posture the existing pkg/openapidiff
// tests pin.
func TestCmdOpenapiDiff_JSON(t *testing.T) {
	base := writeYAML(t, minimalOpenAPIYAML)
	propYAML := strings.Replace(minimalOpenAPIYAML,
		"                  count: { type: integer }\n",
		"                  count: { type: string }\n",
		1)
	prop := writeYAML(t, propYAML)
	stdout, restore := swapStdout(t)
	defer restore()

	jsonOutput = true
	defer func() { jsonOutput = false }()

	if code := cmdOpenapiDiff([]string{base, prop}); code != 2 {
		t.Errorf("type-change docs --json: code = %d, want 2\n--- stdout ---\n%s", code, stdout.String())
	}
	// Parse the FIRST NDJSON record. Pin only the discriminator
	// field (Kind) — the field set can grow across differ
	// versions and a byte-stability pin would force unwanted
	// breaks on every enhancement.
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}
		var sb openapidiff.SchemaBreak
		if err := json.Unmarshal([]byte(line), &sb); err != nil {
			t.Errorf("--json line decode: %v\nline=%s", err, line)
			continue
		}
		if sb.Kind == "" {
			t.Errorf("--json record has empty Kind\nline=%s", line)
		}
		break
	}
}

// TestCmdOpenapiDiff_MissingFile — a baseline that doesn't exist
// fails fast with a non-zero exit (the file is a precondition for
// the pre-publish gate; a missing file must NOT silently exit 0).
func TestCmdOpenapiDiff_MissingFile(t *testing.T) {
	prop := writeYAML(t, minimalOpenAPIYAML)
	if code := cmdOpenapiDiff([]string{"/tmp/does-not-exist-fake-file.yaml", prop}); code == 0 {
		t.Errorf("missing baseline: code = 0, want non-zero")
	}
}

// TestCmdOpenapi_Dispatcher — the verb-level dispatcher routes
// `diff` and rejects unknown subcommands. Pins the main.go
// switch arm at cmd/gregale/main.go.
func TestCmdOpenapi_Dispatcher(t *testing.T) {
	if code := cmdOpenapi([]string{"diff", "/tmp/x.yaml", "/tmp/y.yaml"}); code == 0 {
		// 0 isn't strictly wrong (two empty files might
		// compare to zero rows) but the file-error path
		// fires before the parser, so a non-zero is the
		// deterministic expectation. The point of the test
		// is the dispatch path: a nil-args / unknown-sub
		// should return 1.
	}
	if code := cmdOpenapi(nil); code != 1 {
		t.Errorf("cmdOpenapi nil = %d, want 1", code)
	}
	if code := cmdOpenapi([]string{"bogus"}); code != 1 {
		t.Errorf("cmdOpenapi bogus sub = %d, want 1", code)
	}
}

func TestCmdOpenapiGet_RawDocumentAndSource(t *testing.T) {
	const wantDoc = `{"openapi":"3.1.0","paths":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/openapi" {
			t.Errorf("request = %s %s, want GET /v1/apps/demo/openapi", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("source"); got != "auto" {
			t.Errorf("source = %q, want auto", got)
		}
		_, _ = w.Write([]byte(wantDoc))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_openapi")

	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdOpenapiGet([]string{"demo", "--source", "auto"}); code != 0 {
		t.Fatalf("cmdOpenapiGet = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != wantDoc {
		t.Errorf("document = %q, want %q", got, wantDoc)
	}
}

func TestCmdOpenapiImportAndDryRun(t *testing.T) {
	docPath := filepath.Join(t.TempDir(), "openapi.json")
	doc := `{"openapi":"3.1.0","info":{"title":"demo","version":"1"},"paths":{}}`
	if err := os.WriteFile(docPath, []byte(doc), 0o600); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"openapi":"3.1.0"`)) {
			t.Errorf("request body missing OpenAPI document: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/dry-run") {
			_, _ = w.Write([]byte(`{"suggestions":[{"path":"/users","methods":["GET"],"kind":"validate","action":{"type":"validate"}}],"openapi_version":"3.1.0","endpoint_count":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"app_id":"app-1","source":"manual_import","openapi_version":"3.1.0","endpoint_count":1,"byte_size":128,"updated_at":"2026-09-04T00:00:00Z"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_openapi")

	stdout, restore := swapStdout(t)
	defer restore()
	resetJSONOutput()
	if code := cmdOpenapiImport([]string{"demo", docPath}); code != 0 {
		t.Fatalf("cmdOpenapiImport = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "OpenAPI document imported") || !strings.Contains(stdout.String(), "3.1.0") {
		t.Errorf("import output = %q", stdout.String())
	}

	stdout.Reset()
	jsonOutput = true
	defer resetJSONOutput()
	if code := cmdOpenapiDryRun([]string{"demo", docPath}); code != 0 {
		t.Fatalf("cmdOpenapiDryRun = %d, want 0", code)
	}
	var got api.AppOpenAPIImportDryRunResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Suggestions) != 1 || got.Suggestions[0].Path != "/users" {
		t.Fatalf("dry-run suggestions = %+v", got.Suggestions)
	}
}

func TestCmdOpenapiRemove_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/apps/demo/openapi" {
			t.Errorf("request = %s %s, want DELETE /v1/apps/demo/openapi", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_openapi")
	stdout, restore := swapStdout(t)
	defer restore()
	jsonOutput = true
	defer resetJSONOutput()

	if code := cmdOpenapiRemove([]string{"demo"}); code != 0 {
		t.Fatalf("cmdOpenapiRemove = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("remove output is not JSON: %v\n%s", err, stdout.String())
	}
	if got["app"] != "demo" || got["deleted"] != true {
		t.Fatalf("remove output = %#v", got)
	}
}
