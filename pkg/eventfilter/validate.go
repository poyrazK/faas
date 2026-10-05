// Package eventfilter validates the event-router declaration language without
// depending on API or persistence packages.
package eventfilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

func ValidatePattern(pattern string) error {
	return ValidateNamedPattern("pattern", pattern)
}

func ValidateFilter(filter json.RawMessage) error {
	trimmed := bytes.TrimSpace(filter)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("event: decode subscription filter: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("event: subscription filter must contain one JSON value")
		}
		return fmt.Errorf("event: decode subscription filter: %w", err)
	}
	if !isObject(value) {
		return errors.New("event: subscription filter must be a JSON object")
	}
	return validateFilterValue(value)
}

// ValidateNamedPattern validates a pattern and names its field in errors.
func ValidateNamedPattern(name, pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("event: subscription %s is required", name)
	}
	if strings.Count(pattern, "*") > 2 {
		return fmt.Errorf("event: subscription %s has too many wildcards", name)
	}
	if strings.Contains(strings.Trim(pattern, "*"), "*") {
		return fmt.Errorf("event: subscription %s wildcard must be at an edge", name)
	}
	return nil
}

func validateFilterValue(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for key, operand := range object {
		if strings.HasPrefix(key, "$") {
			switch key {
			case "$eq":
				// Any JSON value is a valid exact-match operand.
			case "$prefix", "$suffix":
				if _, ok := operand.(string); !ok {
					return fmt.Errorf("event: %s expects a string", key)
				}
			case "$gt", "$gte", "$lt", "$lte":
				if _, ok := jsonNumber(operand); !ok {
					return fmt.Errorf("event: %s expects a JSON number", key)
				}
			default:
				return fmt.Errorf("event: unsupported operator %q", key)
			}
			continue
		}
		if err := validateFilterValue(operand); err != nil {
			return err
		}
	}
	return nil
}

func jsonNumber(value any) (*big.Rat, bool) {
	var text string
	switch number := value.(type) {
	case json.Number:
		text = number.String()
	case float64:
		text = fmt.Sprintf("%v", number)
	case float32:
		text = fmt.Sprintf("%v", number)
	case int:
		text = fmt.Sprintf("%d", number)
	case int64:
		text = fmt.Sprintf("%d", number)
	default:
		return nil, false
	}
	ratio, ok := new(big.Rat).SetString(text)
	return ratio, ok
}

func isObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}
