// Package operations defines the customer operation contract above execution ledgers.
package operations

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// CanonicalJSON rejects duplicate members and normalizes object ordering and
// equivalent decimal numbers without converting them to lossy float64 values.
func CanonicalJSON(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := readJSONValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("operation JSON must contain exactly one value")
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n")), nil
}

func readJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > api.OperationJSONMaxDepth {
		return nil, fmt.Errorf("operation JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return nil, fmt.Errorf("operation JSON: %w", err)
	}
	if number, ok := t.(json.Number); ok {
		return normalizeNumber(number)
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("operation JSON object key is invalid")
			}
			if _, duplicate := out[name]; duplicate {
				return nil, fmt.Errorf("operation JSON has a duplicate object member")
			}
			value, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out[name] = value
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("operation JSON object is incomplete")
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			value, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("operation JSON array is incomplete")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("operation JSON contains an unexpected delimiter")
	}
}

func normalizeNumber(n json.Number) (json.Number, error) {
	s := string(n)
	if len(s) > api.OperationJSONMaxNumberBytes {
		return "", fmt.Errorf("operation JSON number exceeds limit")
	}
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	exponent := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		exponent, err = strconv.Atoi(s[i+1:])
		if err != nil || exponent < -api.OperationJSONMaxExponent || exponent > api.OperationJSONMaxExponent {
			return "", fmt.Errorf("operation JSON numeric exponent exceeds limit")
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		exponent -= len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(s, "0")
	exponent += len(s) - len(trimmed)
	s = trimmed
	if negative {
		s = "-" + s
	}
	// Ordinary typed Go handlers/control DTOs must still decode integer
	// values. Keep bounded integral values in decimal notation rather than
	// turning 100 into 1e2, which encoding/json rejects for int64 fields.
	if exponent >= 0 && len(s)+exponent <= api.OperationJSONMaxNumberBytes {
		return json.Number(s + strings.Repeat("0", exponent)), nil
	}
	if exponent != 0 {
		s += "e" + strconv.Itoa(exponent)
	}
	return json.Number(s), nil
}

func InputFingerprint(raw []byte) (string, error) {
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// IdentityScope excludes definition revision so deployment does not create
// duplicate work for an existing key. No raw key is stored or logged.
func IdentityScope(account, app, scope, owner, name, key string) string {
	data, _ := json.Marshal([]string{"gregale.operation.v1", account, app, scope, owner, name, key})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
