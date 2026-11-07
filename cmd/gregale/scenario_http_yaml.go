package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decode the surrounding schema normally, then replace JSON values using their
// original YAML nodes. Decoding an interface directly would first round decimal
// and large integer literals to float64. Keep small integers in their existing
// Go representation, and retain other numbers as exact JSON decimal literals.
func (r *testHTTPRequest) UnmarshalYAML(node *yaml.Node) error {
	fields, err := testHTTPYAMLFields(node, "name", "as", "method", "path", "headers", "json", "expect", "capture")
	if err != nil {
		return err
	}
	type plain testHTTPRequest
	if err := node.Decode((*plain)(r)); err != nil {
		return err
	}
	if field, ok := fields["json"]; ok {
		r.JSON, err = decodeTestHTTPJSONYAML(&field, r.JSON)
		r.JSONNull = err == nil && r.JSON == nil
	}
	return err
}

func (e *testHTTPExpect) UnmarshalYAML(node *yaml.Node) error {
	fields, err := testHTTPYAMLFields(node, "status", "content_type", "json")
	if err != nil {
		return err
	}
	type plain testHTTPExpect
	if err := node.Decode((*plain)(e)); err != nil {
		return err
	}
	if field, ok := fields["json"]; ok && e.JSON != nil {
		value, err := decodeTestHTTPJSONYAML(&field, e.JSON)
		if err != nil {
			return err
		}
		e.JSON = value.(map[string]any)
	}
	return nil
}

// Node.Decode does not inherit the outer decoder's KnownFields setting. Check
// these two custom schemas explicitly, including fields supplied by YAML merges.
func testHTTPYAMLFields(node *yaml.Node, allowed ...string) (map[string]yaml.Node, error) {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return nil, err
	}
	for name := range fields {
		known := false
		for _, candidate := range allowed {
			known = known || name == candidate
		}
		if !known {
			return nil, fmt.Errorf("line %d: unknown HTTP field %q", node.Line, name)
		}
	}
	return fields, nil
}

func decodeTestHTTPJSONYAML(node *yaml.Node, value any) (any, error) {
	if node.Kind == yaml.AliasNode {
		return decodeTestHTTPJSONYAML(node.Alias, value)
	}
	switch node.Kind {
	case yaml.MappingNode:
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("JSON object keys must be strings")
		}
		var fields map[string]yaml.Node
		if err := node.Decode(&fields); err != nil {
			return nil, err
		}
		for key, field := range fields {
			exact, err := decodeTestHTTPJSONYAML(&field, object[key])
			if err != nil {
				return nil, err
			}
			object[key] = exact
		}
	case yaml.SequenceNode:
		array, ok := value.([]any)
		if !ok || len(array) != len(node.Content) {
			return nil, errors.New("invalid JSON array")
		}
		for i, field := range node.Content {
			exact, err := decodeTestHTTPJSONYAML(field, array[i])
			if err != nil {
				return nil, err
			}
			array[i] = exact
		}
	case yaml.ScalarNode:
		if node.ShortTag() == "!!int" {
			switch value.(type) {
			case int, uint64:
				return value, nil
			}
			integer, ok := new(big.Int).SetString(strings.ReplaceAll(node.Value, "_", ""), 0)
			if !ok {
				return nil, errors.New("invalid JSON integer")
			}
			return json.Number(integer.String()), nil
		}
		if node.ShortTag() == "!!float" {
			return testHTTPYAMLDecimal(node.Value)
		}
		// YAML's resolver labels decimal exponents outside float64's range as
		// strings. Unquoted JSON number literals still retain numeric meaning.
		if node.ShortTag() == "!!str" && node.Style == 0 && len(node.Value) > 0 && strings.ContainsAny(node.Value[:1], "-0123456789") && json.Valid([]byte(node.Value)) {
			return json.Number(node.Value), nil
		}
	}
	return value, nil
}

func testHTTPYAMLDecimal(literal string) (json.Number, error) {
	number := strings.TrimPrefix(strings.ReplaceAll(literal, "_", ""), "+")
	mantissa, exponent := number, ""
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		mantissa, exponent = number[:i], number[i:]
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	integer, fraction, decimal := strings.Cut(mantissa, ".")
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	number = sign + integer
	if decimal {
		if fraction == "" {
			fraction = "0"
		}
		number += "." + fraction
	}
	number += exponent
	if !json.Valid([]byte(number)) {
		return "", errors.New("value must be JSON-encodable with finite numbers")
	}
	return json.Number(number), nil
}

func (r testHTTPRequest) MarshalYAML() (any, error) {
	type plain testHTTPRequest
	var node yaml.Node
	if err := node.Encode(plain(r)); err != nil {
		return nil, err
	}
	if r.hasJSONBody() {
		if err := setTestHTTPJSONYAML(&node, r.JSON); err != nil {
			return nil, err
		}
	}
	return &node, nil
}

func (e testHTTPExpect) MarshalYAML() (any, error) {
	type plain testHTTPExpect
	var node yaml.Node
	if err := node.Encode(plain(e)); err != nil {
		return nil, err
	}
	if e.JSON != nil {
		if err := setTestHTTPJSONYAML(&node, e.JSON); err != nil {
			return nil, err
		}
	}
	return &node, nil
}

func setTestHTTPJSONYAML(mapping *yaml.Node, value any) error {
	node, err := encodeTestHTTPJSONYAML(value)
	if err != nil {
		return err
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "json" {
			mapping.Content[i+1] = node
			return nil
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "json"}, node)
	return nil
}

func encodeTestHTTPJSONYAML(value any) (*yaml.Node, error) {
	var node yaml.Node
	if number, ok := value.(json.Number); ok {
		// YAML's fixed-width integer resolver cannot decode an explicit !!int
		// above uint64. Decimal nodes retain the original number text instead.
		tag := "!!float"
		if _, err := strconv.ParseFloat(string(number), 64); err != nil {
			// Preserve an unquoted exponent outside YAML's float64 range. The
			// JSON decoder above recognizes its numeric spelling before use.
			tag = "!!str"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(number)}, nil
	}
	if err := node.Encode(value); err != nil {
		return nil, err
	}
	switch value := value.(type) {
	case map[string]any:
		for i := 0; i < len(node.Content); i += 2 {
			child, err := encodeTestHTTPJSONYAML(value[node.Content[i].Value])
			if err != nil {
				return nil, err
			}
			node.Content[i+1] = child
		}
	case []any:
		for i, item := range value {
			child, err := encodeTestHTTPJSONYAML(item)
			if err != nil {
				return nil, err
			}
			node.Content[i] = child
		}
	}
	return &node, nil
}
