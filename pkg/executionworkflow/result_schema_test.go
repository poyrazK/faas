package executionworkflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompileAndValidateResultSchema(t *testing.T) {
	schema, err := CompileResultSchema(json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["count"],"properties":{"count":{"type":"integer"}}}`))
	if err != nil {
		t.Fatalf("compile result schema: %v", err)
	}
	if detail := ValidateResult(json.RawMessage(`{"count":3}`), schema); detail != "" {
		t.Fatalf("valid result rejected: %s", detail)
	}
	if detail := ValidateResult(json.RawMessage(`{"count":"three"}`), schema); detail == "" || !strings.Contains(detail, "/count") {
		t.Fatalf("invalid result detail = %q", detail)
	}
	if detail := ValidateResult(nil, schema); detail != "Run did not return a valid JSON result" {
		t.Fatalf("empty result detail = %q", detail)
	}
}

func TestCompileResultSchemaRejectsUnsupportedDraftAndRemoteReferences(t *testing.T) {
	for _, raw := range []string{
		`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
		`{"$ref":"https://example.com/schema.json"}`,
	} {
		if _, err := CompileResultSchema(json.RawMessage(raw)); err == nil {
			t.Fatalf("unsupported schema was accepted: %s", raw)
		}
	}
}
