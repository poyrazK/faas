// adr: 126, 375
package openapiimport

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONDocumentPreservesJSONAndConvertsYAML(t *testing.T) {
	jsonBody := []byte(` {"openapi":"3.1.0", "x-value":1e130000} `)
	got, err := JSONDocument(jsonBody)
	if err != nil || !bytes.Equal(got, jsonBody) {
		t.Fatalf("JSON number/format changed: %v", err)
	}
	yamlBody := []byte("openapi: 3.1.0\ninfo: {title: yaml, version: '1'}\npaths: {}\n")
	got, err = JSONDocument(yamlBody)
	if err != nil || !json.Valid(got) {
		t.Fatalf("YAML did not become JSON: %v", err)
	}
	if version, _, err := ValidateImport(got); err != nil || version != "3.1.0" {
		t.Fatalf("normalized contract lost its meaning: version=%s err=%v", version, err)
	}
	if _, err := JSONDocument([]byte("{broken")); err == nil {
		t.Fatal("malformed document normalized")
	}
}
