package executioncontract_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executioncontract"
)

func TestStandaloneInputAndResultContracts(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["n"],"properties":{"n":{"type":"integer"}}}`)
	request := api.CreateExecutionRequest{InputSchema: schema, ResultSchema: schema, Input: json.RawMessage(`{"n":2}`)}
	if err := executioncontract.CheckRequest(request); err != nil {
		t.Fatal(err)
	}
	valid, err := executioncontract.Validate(request.Input, schema)
	if err != nil || valid == nil || valid.Status != "valid" {
		t.Fatal(valid, err)
	}
	request.Input = json.RawMessage(`{"n":"private-input-value"}`)
	if err := executioncontract.CheckRequest(request); err == nil || strings.Contains(err.Error(), "private-input-value") {
		t.Fatal("input mismatch must be rejected without values")
	}
	invalid, err := executioncontract.Validate(request.Input, schema)
	if err != nil || invalid == nil || invalid.Status != "invalid" || strings.Contains(invalid.Message, "private-input-value") {
		t.Fatal(invalid, err)
	}
	if err := invalid.Validate(); err != nil {
		t.Fatal(err)
	}
	if validation, err := executioncontract.Validate(request.Input, nil); err != nil || validation != nil {
		t.Fatal("legacy Runs acquired a contract", err)
	}
}

func TestStandaloneSchemasAreBoundedAndCannotFetchResources(t *testing.T) {
	for _, schema := range []string{`{"$ref":"https://example.invalid/private.json"}`, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`, `{"type":123}`, `true` + strings.Repeat(" ", api.ExecutionContractSchemaMaxBytes)} {
		if _, err := executioncontract.Compile(json.RawMessage(schema)); err == nil {
			t.Fatalf("accepted unsupported contract %q", schema[:min(len(schema), 90)])
		}
	}
	if err := executioncontract.CheckRequest(api.CreateExecutionRequest{InputSchema: json.RawMessage("false")}); err == nil {
		t.Fatal("false input schema admitted null")
	}
}
