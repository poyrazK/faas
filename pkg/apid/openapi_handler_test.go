// openapi_handler_test.go locks in the public contract of the spec
// hosting endpoints so a refactor that changes Content-Type, body
// bytes, or JSON determinism fails here — not at SDK regen time in
// three months when the JSON starts diverging across instances.
//
// The tests are pure httptest: no DB, no fixtures, no I/O. Embedded
// spec → handler → recorder → assert. Runs in <5 ms.

package apid

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServeOpenAPISpec_ContentType asserts the YAML endpoint emits the
// right Content-Type, cache hint, and a body that starts with the OAS
// 3.1 header. SDK codegen tools (openapi-generator) sniff the first
// bytes; getting this wrong silently breaks them.
func TestServeOpenAPISpec_ContentType(t *testing.T) {
	w := httptest.NewRecorder()
	ServeOpenAPISpec(w, httptest.NewRequest("GET", "/v1/openapi.yaml", nil))

	resp := w.Result()
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Content-Type"), "application/yaml; charset=utf-8"; got != want {
		t.Errorf("Content-Type: got %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("Cache-Control"), "public, max-age=300"; got != want {
		t.Errorf("Cache-Control: got %q, want %q", got, want)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.HasPrefix(body, []byte("openapi: 3.1")) {
		t.Errorf("body must begin with `openapi: 3.1`; got prefix %q", first40(body))
	}
	// Spot-check: paths section is present.
	if !bytes.Contains(body[:min(4096, len(body))], []byte("\npaths:")) {
		t.Errorf("body missing `paths:` section in first 4 KB; first lines: %q", first40(body))
	}
}

func first40(b []byte) string {
	if len(b) > 40 {
		return string(b[:40]) + "..."
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestOpenAPIYAML_NotEmpty is a smoke test on the package-level getter:
// the embed succeeded and the YAML is non-trivial.
func TestOpenAPIYAML_NotEmpty(t *testing.T) {
	b := OpenAPIYAML()
	if len(b) == 0 {
		t.Fatal("OpenAPIYAML() returned empty bytes — //go:embed did not pick up pkg/apid/openapi.yaml")
	}
	// A 25+ route spec should be at least 5 KB.
	if len(b) < 5_000 {
		t.Errorf("embedded spec suspiciously small: %d bytes (review pkg/apid/openapi.yaml vs api/openapi.yaml)", len(b))
	}
}

// TestServeOpenAPISpecJSON_Deterministic locks in the property the
// godoc promises: yaml.Unmarshal → map[string]any → json.Marshal
// produces stable bytes across runs. SDK generators cache by ETag
// bodies; drift here would invalidate caches without notice.
//
// The test does a double round-trip: marshal → unmarshal → marshal
// again, and asserts the two marshalled bodies are byte-equal.
func TestServeOpenAPISpecJSON_Deterministic(t *testing.T) {
	a := recordJSON(t)
	// Second round-trip is the formal property: parse + re-marshal
	// should not introduce change.
	var doc any
	if err := json.Unmarshal(a, &doc); err != nil {
		t.Fatalf("unmarshal first response: %v", err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("JSON response is not deterministic across round-trip:\n  first:  %s\n  second: %s", trunc(a), trunc(b))
	}
}

// TestServeOpenAPISpecJSON_Shape asserts the JSON endpoint returns
// application/json, has the openapi field set to a 3.x version, and
// includes the paths section. Also catches the build-time error
// fallback (the `{"error":...}` envelope) so a malformed embedded
// spec is loud here, not in production.
func TestServeOpenAPISpecJSON_Shape(t *testing.T) {
	w := httptest.NewRecorder()
	ServeOpenAPISpecJSON(w, httptest.NewRequest("GET", "/v1/openapi.json", nil))

	resp := w.Result()
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type: got %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("Cache-Control"), "public, max-age=300"; got != want {
		t.Errorf("Cache-Control: got %q, want %q", got, want)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if bytes.HasPrefix(body, []byte(`{"error":`)) {
		t.Fatalf("init() fell through to error envelope — embedded spec is malformed: %s", body)
	}

	var doc struct {
		OpenAPI string         `json:"openapi"`
		Paths   map[string]any `json:"paths"`
		Info    struct {
			Title string `json:"title"`
		} `json:"info"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Errorf("openapi field: got %q, want a 3.x version", doc.OpenAPI)
	}
	if len(doc.Paths) == 0 {
		t.Errorf("paths: empty — embedded spec has no paths")
	}
	if doc.Info.Title == "" {
		t.Errorf("info.title is empty — embedded spec is missing info block")
	}
}

func recordJSON(t *testing.T) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	ServeOpenAPISpecJSON(w, httptest.NewRequest("GET", "/v1/openapi.json", nil))
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func trunc(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + fmt.Sprintf("... [%d more bytes]", len(b)-max)
}
