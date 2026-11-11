package edgevalidate

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

var errNumberWork = errors.New("number exceeds validation precision or exponent bounds")

// The schema library converts json.Number to big.Rat. Bound the textual
// representation before that conversion: even a short token can request an
// enormous numerator/denominator through its exponent.
func checkNumberWork(value any, budget int) error {
	return checkNumberWorkRemaining(value, &budget)
}

func checkNumberWorkRemaining(value any, remaining *int) error {
	switch v := value.(type) {
	case json.Number:
		s := v.String()
		if len(s) > MaxNumberBytes {
			return errNumberWork
		}
		cost := len(s)
		if i := strings.IndexAny(s, "eE"); i >= 0 {
			exponent, err := strconv.ParseInt(s[i+1:], 10, 64)
			if err != nil || exponent < -MaxNumberExponent || exponent > MaxNumberExponent {
				return errNumberWork
			}
			if exponent < 0 {
				exponent = -exponent
			}
			cost += int(exponent)
		}
		if cost > *remaining {
			return errNumberWork
		}
		*remaining -= cost
	case map[string]any:
		for _, child := range v {
			if err := checkNumberWorkRemaining(child, remaining); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := checkNumberWorkRemaining(child, remaining); err != nil {
				return err
			}
		}
	}
	return nil
}
