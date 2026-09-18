package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// Subscription is the declarative event target used by the internal router.
// AccountID is part of the match, rather than merely a lookup hint, so a
// subscription can never observe another account's events when a caller hands
// a worker an event from the wrong tenant.
//
// Source and Type accept an exact value or one edge wildcard: "billing.*"
// matches a source with the billing. prefix, while "*.paid" matches a type
// with the .paid suffix. A wildcard at both edges is treated as a contains
// pattern. Interior wildcards are rejected to keep matching deterministic.
type Subscription struct {
	ID        string
	AccountID string
	Source    string
	Type      string
	Filter    json.RawMessage
}

// Match reports whether e satisfies the subscription. A malformed
// subscription or filter returns an error; an account or envelope mismatch is
// a normal non-match and does not reveal whether the other tenant has data.
func (s Subscription) Match(e Envelope) (bool, error) {
	if err := s.Validate(); err != nil {
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("event: invalid envelope: %w", err)
	}
	if s.AccountID != e.AccountID {
		return false, nil
	}
	sourceMatch, err := matchPattern(s.Source, e.Source)
	if err != nil || !sourceMatch {
		return false, err
	}
	typeMatch, err := matchPattern(s.Type, e.Type)
	if err != nil || !typeMatch {
		return false, err
	}
	if len(bytes.TrimSpace(s.Filter)) == 0 || bytes.Equal(bytes.TrimSpace(s.Filter), []byte("null")) {
		return true, nil
	}

	document, err := e.filterDocument()
	if err != nil {
		return false, err
	}
	var filter any
	decoder := json.NewDecoder(bytes.NewReader(s.Filter))
	decoder.UseNumber()
	if err := decoder.Decode(&filter); err != nil {
		return false, fmt.Errorf("event: decode subscription filter: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return false, errors.New("event: subscription filter must contain one JSON value")
		}
		return false, fmt.Errorf("event: decode subscription filter: %w", err)
	}
	if !isObject(filter) {
		return false, errors.New("event: subscription filter must be a JSON object")
	}
	matched, err := matchFilter(filter, document)
	if err != nil {
		return false, fmt.Errorf("event: evaluate subscription filter: %w", err)
	}
	return matched, nil
}

// Validate checks the fields that are needed before a subscription can be
// persisted or evaluated. It deliberately does not validate Filter here so a
// caller can use this method for cheap source/type validation; Match validates
// the filter when it is actually evaluated.
func (s Subscription) Validate() error {
	if strings.TrimSpace(s.AccountID) == "" {
		return errors.New("event: subscription account_id is required")
	}
	if err := validatePattern("source", s.Source); err != nil {
		return err
	}
	if err := validatePattern("type", s.Type); err != nil {
		return err
	}
	return nil
}

func validatePattern(name, pattern string) error {
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

func matchPattern(pattern, value string) (bool, error) {
	if err := validatePattern("pattern", pattern); err != nil {
		return false, err
	}
	leading := strings.HasPrefix(pattern, "*")
	trailing := strings.HasSuffix(pattern, "*")
	core := strings.Trim(pattern, "*")
	switch {
	case leading && trailing:
		return strings.Contains(value, core), nil
	case leading:
		return strings.HasSuffix(value, core), nil
	case trailing:
		return strings.HasPrefix(value, core), nil
	default:
		return value == pattern, nil
	}
}

func (e Envelope) filterDocument() (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(e.Data))
	decoder.UseNumber()
	var data any
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("event: decode envelope data: %w", err)
	}
	return map[string]any{
		"specversion":       e.SpecVersion,
		"id":                e.ID,
		"source":            e.Source,
		"type":              e.Type,
		"time":              e.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		"data_content_type": e.DataContentType,
		"data":              data,
		"account_id":        e.AccountID,
	}, nil
}

// matchFilter treats an object without operator keys as a nested object
// predicate. Every key must exist and match, so filters are conjunctive. An
// object with operator keys applies all operators to the current scalar value.
func matchFilter(expected, actual any) (bool, error) {
	expectedObject, ok := expected.(map[string]any)
	if !ok {
		return equalJSONValue(actual, expected), nil
	}
	for key, operand := range expectedObject {
		if strings.HasPrefix(key, "$") {
			matched, err := matchOperator(key, operand, actual)
			if err != nil || !matched {
				return false, err
			}
			continue
		}
		actualObject, ok := actual.(map[string]any)
		if !ok {
			return false, nil
		}
		actualValue, ok := actualObject[key]
		if !ok {
			return false, nil
		}
		matched, err := matchFilter(operand, actualValue)
		if err != nil || !matched {
			return false, err
		}
	}
	return true, nil
}

func matchOperator(operator string, expected, actual any) (bool, error) {
	switch operator {
	case "$eq":
		return equalJSONValue(actual, expected), nil
	case "$prefix", "$suffix":
		expectedString, ok := expected.(string)
		if !ok {
			return false, fmt.Errorf("%s expects a string", operator)
		}
		actualString, ok := actual.(string)
		if !ok {
			return false, nil
		}
		if operator == "$prefix" {
			return strings.HasPrefix(actualString, expectedString), nil
		}
		return strings.HasSuffix(actualString, expectedString), nil
	case "$gt", "$gte", "$lt", "$lte":
		actualNumber, ok := jsonNumber(actual)
		if !ok {
			return false, nil
		}
		expectedNumber, ok := jsonNumber(expected)
		if !ok {
			return false, fmt.Errorf("%s expects a JSON number", operator)
		}
		comparison := actualNumber.Cmp(expectedNumber)
		switch operator {
		case "$gt":
			return comparison > 0, nil
		case "$gte":
			return comparison >= 0, nil
		case "$lt":
			return comparison < 0, nil
		default:
			return comparison <= 0, nil
		}
	default:
		return false, fmt.Errorf("unsupported operator %q", operator)
	}
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

func equalJSONValue(actual, expected any) bool {
	actualNumber, actualIsNumber := jsonNumber(actual)
	expectedNumber, expectedIsNumber := jsonNumber(expected)
	if actualIsNumber || expectedIsNumber {
		return actualIsNumber && expectedIsNumber && actualNumber.Cmp(expectedNumber) == 0
	}
	return jsonValueEqual(actual, expected)
}

func jsonValueEqual(actual, expected any) bool {
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	return actualErr == nil && expectedErr == nil && bytes.Equal(actualJSON, expectedJSON)
}

func isObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}
