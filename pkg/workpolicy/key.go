package workpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

const (
	MaxKeyBytes      = 256
	MaxSelectorBytes = 256
	MaxSelectorDepth = 8
)

var selectorSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Selector is a restricted dot path into a JSON object. Arrays, wildcards,
// filters, and implicit type conversions are intentionally unsupported so a
// policy resolves to the same scalar on every producer and scheduler.
type Selector struct{ segments []string }

func ParseSelector(path string) (Selector, error) {
	if path == "" || len(path) > MaxSelectorBytes {
		return Selector{}, fmt.Errorf("work key selector must be 1..%d bytes", MaxSelectorBytes)
	}
	segments := strings.Split(path, ".")
	if len(segments) > MaxSelectorDepth {
		return Selector{}, fmt.Errorf("work key selector exceeds %d fields", MaxSelectorDepth)
	}
	for _, segment := range segments {
		if !selectorSegmentPattern.MatchString(segment) {
			return Selector{}, fmt.Errorf("work key selector contains invalid field %q", segment)
		}
	}
	return Selector{segments: segments}, nil
}

// Resolve returns a type-prefixed canonical scalar. The prefix prevents the
// string "1", JSON number 1, and boolean true from sharing a lane. JSON
// numbers are reduced exactly, so 1, 1.0, and 1e0 resolve to the same key.
func (s Selector) Resolve(payload json.RawMessage) (string, error) {
	if len(s.segments) == 0 {
		return "", fmt.Errorf("work key selector is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", fmt.Errorf("decode work key payload: %w", err)
	}
	if err := dec.Decode(new(any)); err == nil {
		return "", fmt.Errorf("work key payload contains multiple JSON values")
	} else if err != io.EOF {
		return "", fmt.Errorf("decode trailing work key payload: %w", err)
	}
	for _, segment := range s.segments {
		object, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("work key selector traverses a non-object")
		}
		var found bool
		value, found = object[segment]
		if !found {
			return "", fmt.Errorf("work key field %q is missing", segment)
		}
	}
	var key string
	switch scalar := value.(type) {
	case string:
		if scalar == "" {
			return "", fmt.Errorf("work key is empty")
		}
		key = "s:" + scalar
	case json.Number:
		if len(scalar) > MaxKeyBytes {
			return "", fmt.Errorf("work key number exceeds %d bytes", MaxKeyBytes)
		}
		if pos := strings.IndexAny(string(scalar), "eE"); pos >= 0 {
			exponent, err := strconv.Atoi(string(scalar)[pos+1:])
			if err != nil || exponent < -MaxKeyBytes || exponent > MaxKeyBytes {
				return "", fmt.Errorf("work key number exponent is too large")
			}
		}
		number, ok := new(big.Rat).SetString(string(scalar))
		if !ok {
			return "", fmt.Errorf("work key number is invalid")
		}
		key = "n:" + number.RatString()
	case bool:
		if scalar {
			key = "b:true"
		} else {
			key = "b:false"
		}
	default:
		return "", fmt.Errorf("work key must be a string, number, or boolean")
	}
	if len(key) > MaxKeyBytes {
		return "", fmt.Errorf("work key exceeds %d bytes", MaxKeyBytes)
	}
	return key, nil
}
