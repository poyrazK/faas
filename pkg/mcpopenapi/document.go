// Package mcpopenapi generates reviewed MCP adapter source without fetching
// documents, resolving remote references or executing application code.
package mcpopenapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

// decodeDocument accepts JSON and YAML but rejects duplicate keys, aliases,
// custom tags and multiple documents before interpreting any OpenAPI fields.
func decodeDocument(data []byte) (map[string]any, error) {
	if len(data) == 0 || len(data) > api.ToolOpenAPIDocumentMaxBytes {
		return nil, fmt.Errorf("document must contain 1 to %d bytes", api.ToolOpenAPIDocumentMaxBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node, extra yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return nil, errors.New("invalid JSON or YAML document")
	}
	if !errors.Is(decoder.Decode(&extra), io.EOF) || len(node.Content) != 1 {
		return nil, errors.New("provide exactly one OpenAPI document")
	}
	budget := api.ToolOpenAPISchemaMaxNodes
	value, err := yamlValue(node.Content[0], 0, &budget)
	if err != nil {
		return nil, err
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("OpenAPI document must be an object")
	}
	version, _ := document["openapi"].(string)
	parts := strings.Split(version, ".")
	if len(parts) != 3 || parts[0] != "3" || parts[1] != "0" && parts[1] != "1" || parts[2] == "" {
		return nil, errors.New("only OpenAPI 3.0 and 3.1 documents are supported")
	}
	for _, char := range parts[2] {
		if char < '0' || char > '9' {
			return nil, errors.New("OpenAPI patch version must be numeric")
		}
	}
	if dialect, exists := document["jsonSchemaDialect"]; exists && dialect != "https://spec.openapis.org/oas/3.1/dialect/base" && dialect != "https://json-schema.org/draft/2020-12/schema" {
		return nil, errors.New("custom JSON Schema dialects are unsupported")
	}
	return document, nil
}

func yamlValue(node *yaml.Node, depth int, budget *int) (any, error) {
	*budget--
	if depth > api.ToolOpenAPISchemaMaxDepth || *budget < 0 {
		return nil, errors.New("document exceeds the nesting or node limit")
	}
	switch node.Kind {
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			break
		}
		out := map[string]any{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			statusKey := key.Tag == "!!int" && len(key.Value) == 3 && key.Value[0] >= '1' && key.Value[0] <= '5' && key.Value[1] >= '0' && key.Value[1] <= '9' && key.Value[2] >= '0' && key.Value[2] <= '9'
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" && !statusKey {
				return nil, errors.New("document object keys must be strings")
			}
			if _, exists := out[key.Value]; exists {
				return nil, fmt.Errorf("duplicate document key %q", key.Value)
			}
			value, err := yamlValue(node.Content[i+1], depth+1, budget)
			if err != nil {
				return nil, err
			}
			out[key.Value] = value
		}
		return out, nil
	case yaml.SequenceNode:
		if node.Tag != "!!seq" {
			break
		}
		out := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlValue(child, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!timestamp":
			return node.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			if value := strings.ToLower(node.Value); value == "true" || value == "false" {
				return value == "true", nil
			}
		case "!!int", "!!float":
			var number any
			decoder := json.NewDecoder(strings.NewReader(node.Value))
			decoder.UseNumber()
			if decoder.Decode(&number) == nil {
				if n, ok := number.(json.Number); ok && json.Valid([]byte(node.Value)) {
					return n, nil
				}
			}
		}
	}
	return nil, errors.New("YAML aliases, custom tags and non-JSON scalar values are unsupported")
}

func object(value any, context string) (map[string]any, error) {
	out, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", context)
	}
	return out, nil
}

func safeName(name string) bool {
	if name == "" || len(name) > 128 || name == "__proto__" || name == "constructor" || name == "prototype" {
		return false
	}
	for _, r := range name {
		if r < '!' || r > '~' {
			return false
		}
	}
	return true
}
