package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// hunt #8: `openapi dry-run|import` rejected YAML documents as "not valid
// JSON". YAML is accepted, including unquoted integer response codes.
func TestReadOpenapiDocumentAcceptsYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.yaml")
	if err := os.WriteFile(path, []byte("openapi: 3.0.3\ninfo: {title: demo, version: \"1\"}\npaths:\n  /healthz:\n    get:\n      responses:\n        200: {description: ok}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := readOpenapiDocument(path)
	if err != nil {
		t.Fatalf("readOpenapiDocument(yaml): %v", err)
	}
	if _, err := json.Marshal(doc); err != nil {
		t.Fatalf("YAML document does not encode as JSON: %v", err)
	}
	responses := doc["paths"].(map[string]any)["/healthz"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	if _, ok := responses["200"]; !ok {
		t.Fatalf("integer response key not normalized: %#v", responses)
	}
}
