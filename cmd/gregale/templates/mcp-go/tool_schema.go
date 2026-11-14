package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
)

func compileMCPToolSchema(definition map[string]any, field string) (*jsonschema.Resolved, error) {
	raw, present := definition[field]
	if !present {
		return nil, nil
	}
	if _, ok := raw.(map[string]any); !ok {
		return nil, fmt.Errorf("MCP tool %s must be a JSON Schema object", field)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP tool %s", field)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, fmt.Errorf("invalid MCP tool %s", field)
	}
	switch schema.Schema {
	case "", "http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2020-12/schema":
	default:
		return nil, fmt.Errorf("unsupported MCP tool %s dialect", field)
	}
	// A nil Loader rejects external references; local $defs remain supported.
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("invalid or unresolved MCP tool %s", field)
	}
	return resolved, nil
}

func validateMCPToolOutput(schema *jsonschema.Resolved, encoded []byte) error {
	if schema == nil {
		return nil
	}
	// Validate the JSON representation so application structs and typed maps
	// receive the same checks as equivalent ordinary JSON objects.
	var result struct {
		IsError           bool           `json:"isError"`
		ResultType        string         `json:"resultType"`
		StructuredContent map[string]any `json:"structuredContent"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return errors.New("invalid MCP tool result")
	}
	if result.IsError || result.ResultType == "input_required" {
		return nil
	}
	if result.StructuredContent == nil {
		return errors.New("MCP tool outputSchema requires structuredContent")
	}
	if _, err := normalizeMCPToolNumbers(result.StructuredContent); err != nil {
		return err
	}
	return schema.Validate(result.StructuredContent)
}

// The schema validator's type checks require ordinary Go numeric types.
// Convert only when the JSON value survives that conversion unchanged.
func normalizeMCPToolNumbers(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			return integer, nil
		}
		if integer, err := strconv.ParseUint(value.String(), 10, 64); err == nil {
			return integer, nil
		}
		number, err := value.Float64()
		if err == nil {
			before, beforeOK := new(big.Rat).SetString(value.String())
			after, afterOK := new(big.Rat).SetString(strconv.FormatFloat(number, 'g', -1, 64))
			if beforeOK && afterOK && before.Cmp(after) == 0 {
				return number, nil
			}
		}
		return nil, errors.New("MCP tool output number cannot be represented exactly")
	case map[string]any:
		for key, item := range value {
			normalized, err := normalizeMCPToolNumbers(item)
			if err != nil {
				return nil, err
			}
			value[key] = normalized
		}
	case []any:
		for index, item := range value {
			normalized, err := normalizeMCPToolNumbers(item)
			if err != nil {
				return nil, err
			}
			value[index] = normalized
		}
	}
	return value, nil
}
