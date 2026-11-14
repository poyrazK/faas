package mcpopenapi

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

func renderProject(manifest Manifest, encoded []byte) (Project, error) {
	toolScopes := map[string][]string{}
	cases := []map[string]any{}
	for _, operation := range manifest.Operations {
		toolScopes[operation.ID] = []string{}
		cases = append(cases, map[string]any{
			"name": operation.ID, "kind": "tool", "target": operation.ID,
			"arguments": map[string]any{}, "expected_result": nil,
		})
	}
	config := map[string]any{
		"version": 1, "endpoint": "/mcp", "transport": "streamable-http",
		"mode": "stateless", "legacy": true, "allowed_origins": []string{},
		"auth": map[string]any{"mode": "open", "tool_scopes": toolScopes,
			"resource_scopes": map[string]any{}, "prompt_scopes": map[string]any{}},
	}
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Project{}, fmt.Errorf("encode generated hosting policy: %w", err)
	}
	fixturesJSON, err := json.MarshalIndent(map[string]any{"version": 1, "cases": cases}, "", "  ")
	if err != nil {
		return Project{}, fmt.Errorf("encode generated fixture examples: %w", err)
	}
	return Project{Manifest: manifest, Files: map[string][]byte{
		"openapi-tools.json": encoded, "openapi-tools.js": bytes.Clone(adapterSource),
		"openapi-http.js": bytes.Clone(httpSource), "server.js": bytes.Clone(serverSource),
		"gregale-mcp.json":               append(configJSON, '\n'),
		"gregale-mcp.tests.example.json": append(fixturesJSON, '\n'),
		"README.md":                      []byte(generatedGuide),
		"test/openapi-generated.test.js": bytes.Clone(generatedTest),
	}}, nil
}

const generatedGuide = "# Review the generated API tools\n\n" +
	"Generation made no network requests, installed no dependencies, and ran no tools.\n" +
	"openapi-tools.json pins the original document digest, explicit HTTPS server,\n" +
	"selected operations, parameter mapping, schemas and request/response limits.\n" +
	"Document server declarations, defaults, examples and links do not authorize calls.\n\n" +
	"1. Review every generated file, especially the selected server and operation policy.\n" +
	"2. Copy gregale-mcp.tests.example.json to gregale-mcp.tests.json. Supply real\n" +
	"   path/query/body arguments and complete expected MCP results for each case.\n" +
	"   Null expectations are deliberately invalid; examples are not passing evidence.\n" +
	"3. Install the Node starter dependencies with npm ci --ignore-scripts.\n" +
	"   npm test checks catalog wiring and invalid-input refusal, not live API behavior.\n" +
	"4. Use the existing Gregale reviewed publication flow. Fixtures make real API\n" +
	"   calls on the zero-traffic candidate and require explicit execution consent.\n" +
	"5. Grant each exact published tool/version separately before agent invocation.\n\n" +
	"The generated server exposes only the selected tools with open MCP access.\n" +
	"It does not import credentials, forward native Gregale bearers, follow redirects,\n" +
	"retry requests or turn HTTP methods into promises about external side effects.\n" +
	"Adapters validate arguments and responses, refuse private/reserved DNS results,\n" +
	"and bound complete requests. Errors omit upstream response bodies.\n" +
	"Unsupported schemas and authentication require a separately reviewed implementation.\n"

//go:embed runtime/adapter.test.js
var generatedTest []byte
