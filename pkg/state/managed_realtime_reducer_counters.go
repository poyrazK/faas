package state

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Parse exactly before adding: floating-point decoding could round a fractional
// value near the safe-integer limit into an apparently valid counter.
func reducerCounterInteger(raw json.RawMessage) (int64, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || len(text) > 128 || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) {
		return 0, false
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return 0, false
	}
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(text[index+1:])
		if err != nil || exponent < -128 || exponent > 128 {
			return 0, false
		}
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0, false
	}
	integer := value.Num().Int64()
	return integer, integer >= -managedRealtimeMaxEntityVersion && integer <= managedRealtimeMaxEntityVersion
}
func incrementReducerCounter(current map[string]json.RawMessage, field string, delta, minimum, maximum json.RawMessage) (map[string]json.RawMessage, string) {
	if !reducerKeyValid(field) {
		return nil, "increment requires a field of 1..128 bytes"
	}
	amount, ok := reducerCounterInteger(delta)
	if !ok {
		return nil, "increment delta must be a safe integer"
	}
	lower, upper := -managedRealtimeMaxEntityVersion, managedRealtimeMaxEntityVersion
	if len(minimum) > 0 {
		lower, ok = reducerCounterInteger(minimum)
		if !ok {
			return nil, "increment min must be a safe integer"
		}
	}
	if len(maximum) > 0 {
		upper, ok = reducerCounterInteger(maximum)
		if !ok {
			return nil, "increment max must be a safe integer"
		}
	}
	if lower > upper {
		return nil, "increment min must not exceed max"
	}
	previous := int64(0)
	if raw, exists := current[field]; exists {
		previous, ok = reducerCounterInteger(raw)
		if !ok {
			return nil, "increment target must be a safe integer"
		}
	}
	// Both operands are safe integers, so their sum fits int64 exactly.
	next := previous + amount
	if next < -managedRealtimeMaxEntityVersion || next > managedRealtimeMaxEntityVersion {
		return nil, "increment result exceeds the safe integer range"
	}
	if next < lower || next > upper {
		return nil, "increment result is outside min/max bounds"
	}
	if current == nil {
		current = make(map[string]json.RawMessage)
	}
	current[field] = json.RawMessage(strconv.FormatInt(next, 10))
	return current, ""
}
