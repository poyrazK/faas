package main

import (
	"encoding/json"
	"errors"

	"github.com/google/jsonschema-go/jsonschema"
)

func compileMCPTaskOutput(raw json.RawMessage) (func([]byte) error, error) {
	if len(raw) == 0 {
		return func([]byte) error { return nil }, nil
	}
	var schema *jsonschema.Resolved
	if len(raw) > 0 {
		var definition map[string]any
		if err := json.Unmarshal(raw, &definition); err != nil || definition == nil {
			return nil, errors.New("invalid MCP Task output schema")
		}
		var err error
		schema, err = compileMCPToolSchema(map[string]any{"outputSchema": definition}, "outputSchema")
		if err != nil {
			return nil, err
		}
	}
	return func(encoded []byte) error {
		var result struct {
			Content    []json.RawMessage `json:"content"`
			ResultType string            `json:"resultType"`
		}
		if json.Unmarshal(encoded, &result) != nil || result.Content == nil || result.ResultType != "" && result.ResultType != "complete" {
			return errors.New("invalid MCP Task tool result")
		}
		return validateMCPToolOutput(schema, encoded)
	}, nil
}
