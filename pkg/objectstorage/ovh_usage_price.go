package objectstorage

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Native per-resource prices are decimal EUR numbers. Convert exactly and
// round a positive remainder up to one millicent; never pass money via float64.
func priceToMillicents(raw json.RawMessage) (int64, error) {
	raw = bytes.TrimSpace(raw)
	var number json.Number
	if len(raw) == 0 || len(raw) > api.MaxObjectStorageUsagePriceBytes || raw[0] == '"' || raw[0] == '-' || json.Unmarshal(raw, &number) != nil || number == "" {
		return 0, ErrOVHUsageInvalid
	}
	mantissa, exp, hasExp := strings.Cut(strings.ToLower(number.String()), "e")
	var exponent int64
	if hasExp {
		var err error
		exponent, err = strconv.ParseInt(exp, 10, 32)
		if err != nil {
			return 0, ErrOVHUsageInvalid
		}
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return 0, nil
	}
	// One EUR is 100,000 millicents. The exponent moves the decimal point;
	// very small values round to one without allocating powers of ten.
	shift := exponent + 5 - int64(len(fraction))
	cut := int64(len(digits)) + shift
	if cut > 19 {
		return 0, ErrOVHUsageInvalid
	}
	if cut <= 0 {
		return 1, nil
	}
	integer, remainder := digits, ""
	if shift >= 0 {
		integer += strings.Repeat("0", int(shift))
	} else {
		integer, remainder = digits[:cut], digits[cut:]
	}
	value, err := strconv.ParseInt(integer, 10, 64)
	if err != nil || value > api.MaxObjectStoragePolicyValue {
		return 0, ErrOVHUsageInvalid
	}
	if strings.Trim(remainder, "0") != "" {
		if value == api.MaxObjectStoragePolicyValue {
			return 0, ErrOVHUsageInvalid
		}
		value++
	}
	return value, nil
}
