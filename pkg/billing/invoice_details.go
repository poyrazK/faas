package billing

import (
	"encoding/json"
	"math/big"
	"strconv"
	"time"
)

// InvoiceMinorUnits accepts exact provider integer representations only.
// Webhook decoders must use json.Decoder.UseNumber; fractional/overflowing
// amounts are missing data, never rounded monetary facts.
func InvoiceMinorUnits(value any) (int64, bool) {
	var raw string
	switch v := value.(type) {
	case json.Number:
		raw = v.String()
	case string:
		raw = v
	case int64:
		return v, true
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil
}

func InvoiceDifference(amount, deduction int64) (int64, bool) {
	n := new(big.Int).Sub(big.NewInt(amount), big.NewInt(deduction))
	return n.Int64(), n.IsInt64()
}

func InvoiceDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
