package gregalemanifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

// ParseEnvironment uses the same JSON field names for YAML and JSON. It
// rejects duplicate keys, aliases, merge keys, unknown fields, and multiple
// documents before a desired revision can be persisted. Validation and
// normalization of the resulting contract live in pkg/environmentsync.
func ParseEnvironment(raw []byte) (api.EnvironmentDefinition, error) {
	var out api.EnvironmentDefinition
	if len(raw) == 0 || len(raw) > api.EnvironmentGitOpsMaxDefinitionBytes {
		return out, fmt.Errorf("environment definition must contain 1..%d bytes", api.EnvironmentGitOpsMaxDefinitionBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		return out, fmt.Errorf("parse environment definition: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return out, fmt.Errorf("environment definition must contain one document")
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return out, fmt.Errorf("environment definition must be an object")
	}
	value, err := environmentNodeValue(root.Content[0])
	if err != nil {
		return out, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return out, fmt.Errorf("encode environment definition: %w", err)
	}
	jsonDec := json.NewDecoder(bytes.NewReader(encoded))
	jsonDec.DisallowUnknownFields()
	if err := jsonDec.Decode(&out); err != nil {
		return out, fmt.Errorf("decode environment definition: %w", err)
	}
	return out, nil
}

func environmentNodeValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" {
				return nil, fmt.Errorf("environment definition requires string keys without merges at line %d", key.Line)
			}
			if _, exists := out[key.Value]; exists {
				return nil, fmt.Errorf("duplicate environment definition key %q at line %d", key.Value, key.Line)
			}
			value, err := environmentNodeValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[key.Value] = value
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, child := range n.Content {
			value, err := environmentNodeValue(child)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			return n.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			return strconv.ParseBool(n.Value)
		case "!!int", "!!float":
			// Decode YAML numerics first (including hexadecimal integers), then
			// let encoding/json reject non-finite values. Plain JSON integer
			// values retain exact integer precision through Node.Decode.
			var value any
			if err := n.Decode(&value); err != nil {
				return nil, err
			}
			return value, nil
		default:
			return nil, fmt.Errorf("unsupported scalar type at line %d; quote timestamps and identifiers", n.Line)
		}
	default:
		return nil, fmt.Errorf("aliases are not supported in environment definitions at line %d", n.Line)
	}
}
