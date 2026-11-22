package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCustomToolInputSchemaHTTP(t *testing.T) {
	var calls atomic.Int32
	definition := map[string]any{"name": "schema_regression", "inputSchema": map[string]any{
		"type": "object", "required": []string{"amount", "tags"}, "additionalProperties": false,
		"$defs": map[string]any{"tag": map[string]any{"type": "string", "enum": []string{"approved"}}},
		"properties": map[string]any{
			"amount":   map[string]any{"type": "integer", "minimum": 1},
			"tags":     map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"$ref": "#/$defs/tag"}},
			"optional": map[string]any{"type": "string", "default": "annotation only"},
		},
	}}
	if err := RegisterMCPTool(definition, func(_ context.Context, arguments map[string]any, _ *MCPRequestContext) (map[string]any, error) {
		calls.Add(1)
		if _, inserted := arguments["optional"]; inserted {
			t.Error("schema validation must not insert defaults")
		}
		return map[string]any{"content": []any{}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveMCPTool("schema_regression") })
	// Callers cannot alter the compiled schema after registration.
	definition["inputSchema"].(map[string]any)["required"] = []string{}
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Auth = json.RawMessage(`{"mode":"open"}`)
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{currentProtocol, legacyProtocol} {
		for _, arguments := range []string{
			`{"amount":"secret-value","tags":["approved"]}`, `{"amount":-5,"tags":["approved"]}`,
			`{"tags":["approved"]}`, `{"amount":true,"tags":["approved"]}`,
			`{"amount":2,"tags":["rejected"]}`, `{"amount":2,"tags":[]}`,
			`{"amount":2,"tags":["approved"],"unexpected":1}`, `{"amount":2,"tags":["approved"]}`,
		} {
			before := calls.Load()
			body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"schema_regression","arguments":` + arguments + `}}`)
			req := httptest.NewRequest(http.MethodPost, config.Endpoint, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", protocol)
			req.Header.Set("Mcp-Method", "tools/call")
			req.Header.Set("Mcp-Name", "schema_regression")
			response := httptest.NewRecorder()
			handleHTTP(config, auth, nil, response, req)
			var message struct {
				Result struct {
					IsError bool `json:"isError"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil || response.Code != 200 {
				t.Fatalf("response %d: %s (%v)", response.Code, response.Body, err)
			}
			valid := arguments == `{"amount":2,"tags":["approved"]}`
			if message.Result.IsError == valid || (calls.Load() > before) != valid {
				t.Fatalf("protocol %s arguments %s: handler calls %d, result %s", protocol, arguments, calls.Load()-before, response.Body)
			}
			if bytes.Contains(response.Body.Bytes(), []byte("secret-value")) {
				t.Fatal("validation failure exposed argument values")
			}
		}
	}
}

func TestCustomToolSchemaRegistrationIsAtomic(t *testing.T) {
	catalog := newMCPCatalog()
	handler := func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) {
		return map[string]any{}, nil
	}
	register := func(schema any, replace bool) error {
		return catalog.register("tool", "", map[string]any{"name": "schema", "inputSchema": schema}, handler, nil, nil, replace, true)
	}
	if err := register(map[string]any{"type": "object"}, false); err != nil {
		t.Fatal(err)
	}
	_, before, err := catalog.contractFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []any{nil, "object", map[string]any{"type": 42}, map[string]any{"$schema": "https://unsupported.example/schema"}, map[string]any{"$ref": "https://provider.example/schema"}} {
		if err := register(schema, true); err == nil {
			t.Fatalf("accepted invalid/unresolved schema: %#v", schema)
		}
		_, after, err := catalog.contractFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatal("failed replacement changed the catalog")
		}
	}
	if err := register(map[string]any{"type": "object", "required": []string{"new"}}, true); err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.tool("schema")
	if entry.inputSchema.Validate(map[string]any{}) == nil {
		t.Fatal("replacement retained old validator")
	}
}

func TestCustomToolOutputSchemaHTTP(t *testing.T) {
	definition := map[string]any{"name": "output_regression", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{
		"type": "object", "required": []string{"amount", "tags"}, "additionalProperties": false,
		"$defs": map[string]any{"tag": map[string]any{"type": "string", "enum": []string{"approved"}}},
		"properties": map[string]any{
			"amount":   map[string]any{"type": "integer", "minimum": 1},
			"tags":     map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"$ref": "#/$defs/tag"}},
			"optional": map[string]any{"type": "string", "default": "annotation only"},
		},
	}}
	var current map[string]any
	handler := func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) { return current, nil }
	if err := RegisterMCPTool(definition, handler); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveMCPTool("output_regression") })
	definition["outputSchema"].(map[string]any)["required"] = []string{}
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Auth = json.RawMessage(`{"mode":"open"}`)
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{currentProtocol, legacyProtocol} {
		for _, tc := range []struct {
			name           string
			output         any
			omitted, valid bool
		}{
			{name: "missing structured content", omitted: true}, {name: "null"}, {name: "array", output: []any{}},
			{name: "wrong type", output: map[string]any{"amount": "secret-output", "tags": []string{"approved"}}},
			{name: "minimum", output: map[string]any{"amount": -1, "tags": []string{"approved"}}},
			{name: "exact non-integer", output: map[string]any{"amount": json.Number("1.0000000000000001"), "tags": []string{"approved"}}},
			{name: "required", output: map[string]any{"tags": []string{"approved"}}},
			{name: "nested reference", output: map[string]any{"amount": 2, "tags": []string{"rejected"}}},
			{name: "extra property", output: map[string]any{"amount": 2, "tags": []string{"approved"}, "extra": true}},
			{name: "valid", output: map[string]any{"amount": 2, "tags": []string{"approved"}}, valid: true},
			{name: "typed JSON object", output: struct {
				Amount int      `json:"amount"`
				Tags   []string `json:"tags"`
			}{2, []string{"approved"}}, valid: true},
		} {
			current = map[string]any{"content": []any{}}
			if !tc.omitted {
				current["structuredContent"] = tc.output
			}
			req := httptest.NewRequest(http.MethodPost, config.Endpoint, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"output_regression","arguments":{}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", protocol)
			req.Header.Set("Mcp-Method", "tools/call")
			req.Header.Set("Mcp-Name", "output_regression")
			response := httptest.NewRecorder()
			handleHTTP(config, auth, nil, response, req)
			var wire struct {
				Result struct {
					IsError bool           `json:"isError"`
					Output  map[string]any `json:"structuredContent"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || wire.Result.IsError == tc.valid {
				t.Fatalf("%s/%s response: %s", protocol, tc.name, response.Body)
			}
			if bytes.Contains(response.Body.Bytes(), []byte("secret-output")) {
				t.Fatal("invalid output leaked into error")
			}
			if _, present := wire.Result.Output["optional"]; present {
				t.Fatal("output validation inserted defaults")
			}
		}
		current = map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": "business rejection"}}}
		response := httptest.NewRecorder()
		response.Header().Set("MCP-Protocol-Version", protocol)
		callTool(response, httptest.NewRequest(http.MethodPost, config.Endpoint, nil), json.RawMessage(`1`), json.RawMessage(`{"name":"output_regression"}`), auth, nil, nil, protocol)
		if !bytes.Contains(response.Body.Bytes(), []byte("business rejection")) {
			t.Fatalf("error result requires structured output: %s", response.Body)
		}
		if protocol == currentProtocol {
			current = map[string]any{"content": []any{}, "resultType": "input_required", "inputRequests": map[string]any{}}
			response = httptest.NewRecorder()
			response.Header().Set("MCP-Protocol-Version", protocol)
			callTool(response, httptest.NewRequest(http.MethodPost, config.Endpoint, nil), json.RawMessage(`1`), json.RawMessage(`{"name":"output_regression"}`), auth, nil, nil, protocol)
			if bytes.Contains(response.Body.Bytes(), []byte(`"isError":true`)) {
				t.Fatal("intermediate input result was validated as final output")
			}
		}
	}
}

func TestCustomToolOutputSchemaReplacementIsAtomic(t *testing.T) {
	catalog := newMCPCatalog()
	handler := func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) {
		return map[string]any{}, nil
	}
	register := func(schema any, replace bool) error {
		return catalog.register("tool", "", map[string]any{"name": "output", "outputSchema": schema}, handler, nil, nil, replace, true)
	}
	if err := register(map[string]any{"required": []string{"old"}}, false); err != nil {
		t.Fatal(err)
	}
	revision, before, err := catalog.contractFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []any{nil, "object", map[string]any{"type": 42}, map[string]any{"$schema": "https://unsupported.example/schema"}, map[string]any{"$ref": "https://provider.example/schema"}} {
		if err := register(schema, true); err == nil {
			t.Fatalf("accepted invalid output schema %#v", schema)
		}
		next, after, err := catalog.contractFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		if revision != next || before != after {
			t.Fatal("invalid output schema changed catalog")
		}
	}
	if err := register(map[string]any{"required": []string{"new"}}, true); err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.tool("output")
	if validateMCPToolOutput(entry.outputSchema, []byte(`{"structuredContent":{"old":true}}`)) == nil {
		t.Fatal("replacement retained old validator")
	}
	if err := validateMCPToolOutput(entry.outputSchema, []byte(`{"structuredContent":{"new":true}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestToolOutputNumericWireValues(t *testing.T) {
	schema, err := compileMCPToolSchema(map[string]any{"outputSchema": map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "number"}},
	}}, "outputSchema")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0.1", "2.0", "1e3", "3.141592653589793", "9007199254740993", "18446744073709551615"} {
		encoded := []byte(`{"structuredContent":{"value":` + value + `}}`)
		if err := validateMCPToolOutput(schema, encoded); err != nil {
			t.Fatalf("valid output %s: %v", value, err)
		}
	}
}
