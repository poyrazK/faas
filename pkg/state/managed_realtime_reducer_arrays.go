package state

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
)

const ManagedRealtimeReducerMaxArrayLength = 256
const ManagedRealtimeReducerMaxArrayItems = 32

// Normalize numbers without expanding their exponents. This keeps JSON equality
// exact, including large numbers, while bounding work by the encoded input size.
func canonicalReducerNumber(number json.Number) json.Number {
	text := string(number)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign = "-"
		text = text[1:]
	}
	exponent := new(big.Int)
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent.SetString(text[i+1:], 10)
		text = text[:i]
	}
	if i := strings.IndexByte(text, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(text)-i-1)))
		text = text[:i] + text[i+1:]
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return json.Number("0")
	}
	trimmed := strings.TrimRight(text, "0")
	exponent.Add(exponent, big.NewInt(int64(len(text)-len(trimmed))))
	return json.Number(sign + trimmed + "e" + exponent.String())
}
func normalizeReducerJSON(value any) any {
	switch v := value.(type) {
	case json.Number:
		return canonicalReducerNumber(v)
	case []any:
		for i := range v {
			v[i] = normalizeReducerJSON(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = normalizeReducerJSON(v[k])
		}
	}
	return value
}
func reducerArrayValues(items []json.RawMessage) ([]any, bool) {
	values := make([]any, len(items))
	for i, raw := range items {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&values[i]) != nil {
			return nil, false
		}
		values[i] = normalizeReducerJSON(values[i])
	}
	return values, true
}
func mutateReducerArray(current map[string]json.RawMessage, op, field string, rawItems, rawUnique, rawLimit json.RawMessage) (map[string]json.RawMessage, string) {
	if !reducerKeyValid(field) {
		return nil, "array operation requires a field of 1..128 bytes"
	}
	var items []json.RawMessage
	if json.Unmarshal(rawItems, &items) != nil || items == nil || len(items) < 1 || len(items) > ManagedRealtimeReducerMaxArrayItems {
		return nil, "items must be an array of 1..32 JSON values"
	}
	unique := false
	if len(rawUnique) > 0 {
		if op != "append" || string(rawUnique) == "null" || json.Unmarshal(rawUnique, &unique) != nil {
			return nil, "unique must be a boolean on append"
		}
	}
	limit := ManagedRealtimeReducerMaxArrayLength
	if len(rawLimit) > 0 {
		if string(rawLimit) == "null" || json.Unmarshal(rawLimit, &limit) != nil || limit < 0 || limit > ManagedRealtimeReducerMaxArrayLength {
			return nil, "max_length must be an integer from 0 to 256"
		}
	}
	array := make([]json.RawMessage, 0)
	if raw, exists := current[field]; exists {
		if json.Unmarshal(raw, &array) != nil || array == nil {
			return nil, "array target must be an array"
		}
	}
	if len(array) > ManagedRealtimeReducerMaxArrayLength {
		return nil, "array target exceeds 256 items"
	}
	values, ok := reducerArrayValues(array)
	if !ok {
		return nil, "invalid array target"
	}
	matches, ok := reducerArrayValues(items)
	if !ok {
		return nil, "invalid array items"
	}
	if op == "append" {
		for i, value := range matches {
			found := false
			if unique {
				for _, existing := range values {
					if reflect.DeepEqual(existing, value) {
						found = true
						break
					}
				}
			}
			if found {
				continue
			}
			array = append(array, items[i])
			values = append(values, value)
			if len(array) > limit {
				return nil, "array result exceeds max_length"
			}
		}
	} else {
		remaining := make([]json.RawMessage, 0, len(array))
		for i, value := range values {
			found := false
			for _, match := range matches {
				if reflect.DeepEqual(value, match) {
					found = true
					break
				}
			}
			if !found {
				remaining = append(remaining, array[i])
			}
		}
		array = remaining
	}
	if len(array) > limit {
		return nil, "array result exceeds max_length"
	}
	data, err := json.Marshal(array)
	if err != nil {
		return nil, "invalid array result"
	}
	if current == nil {
		current = make(map[string]json.RawMessage)
	}
	current[field] = data
	return current, ""
}
