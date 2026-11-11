package environmentsync

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func canonicalJSONNumbers(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		number, err := canonicalJSONNumber(v.String())
		return json.Number(number), err
	case map[string]any:
		for key, child := range v {
			normalized, err := canonicalJSONNumbers(child)
			if err != nil {
				return nil, err
			}
			v[key] = normalized
		}
	case []any:
		for i, child := range v {
			normalized, err := canonicalJSONNumbers(child)
			if err != nil {
				return nil, err
			}
			v[i] = normalized
		}
	}
	return value, nil
}

// PostgreSQL jsonb expands exponents and preserves decimal scale. Normalize
// exact decimal digits before hashing, using encoding/json's usual exponent
// thresholds without passing through float64 and rounding large integers.
func canonicalJSONNumber(number string) (string, error) {
	sign := ""
	if strings.HasPrefix(number, "-") {
		sign, number = "-", number[1:]
	}
	coefficient := number
	exponent := int64(0)
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		coefficient = number[:i]
		var err error
		exponent, err = strconv.ParseInt(number[i+1:], 10, 32)
		if err != nil {
			return "", fmt.Errorf("JSON number exponent exceeds the supported range")
		}
	}
	if i := strings.IndexByte(coefficient, '.'); i >= 0 {
		exponent -= int64(len(coefficient) - i - 1)
		coefficient = coefficient[:i] + coefficient[i+1:]
	}
	coefficient = strings.TrimLeft(coefficient, "0")
	if coefficient == "" {
		return "0", nil
	}
	digits := strings.TrimRight(coefficient, "0")
	exponent += int64(len(coefficient) - len(digits))
	point := int64(len(digits)) + exponent
	if point > -6 && point <= 21 {
		switch {
		case point <= 0:
			return sign + "0." + strings.Repeat("0", int(-point)) + digits, nil
		case point >= int64(len(digits)):
			return sign + digits + strings.Repeat("0", int(point)-len(digits)), nil
		default:
			return sign + digits[:point] + "." + digits[point:], nil
		}
	}
	mantissa := digits[:1]
	if len(digits) > 1 {
		mantissa += "." + digits[1:]
	}
	exponentText := strconv.FormatInt(point-1, 10)
	if point > 0 {
		exponentText = "+" + exponentText
	}
	return sign + mantissa + "e" + exponentText, nil
}
