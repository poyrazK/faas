package automationchecks

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
)

// Compare JSON numbers exactly without losing integers above 2^53.
func EqualJSON(a, b json.RawMessage) bool {
	normalize := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var number func(any) any
		number = func(v any) any {
			switch v := v.(type) {
			case json.Number:
				return canonicalNumber(string(v))
			case []any:
				for i := range v {
					v[i] = number(v[i])
				}
				return v
			case map[string]any:
				for k := range v {
					v[k] = number(v[k])
				}
				return v
			}
			return v
		}
		return number(value), nil
	}
	x, err := normalize(a)
	if err != nil {
		return false
	}
	y, err := normalize(b)
	return err == nil && reflect.DeepEqual(x, y)
}

// Store a decimal's significant digits and exponent without expanding large powers.
func canonicalNumber(value string) any {
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	exponent := new(big.Int)
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		exponent.SetString(value[i+1:], 10)
		value = value[:i]
	}
	if i := strings.IndexByte(value, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(value)-i-1)))
		value = value[:i] + value[i+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		negative = false
		exponent.SetInt64(0)
		value = "0"
	} else {
		digits := strings.TrimRight(value, "0")
		exponent.Add(exponent, big.NewInt(int64(len(value)-len(digits))))
		value = digits
	}
	return struct {
		Negative         bool
		Digits, Exponent string
	}{negative, value, exponent.String()}
}
