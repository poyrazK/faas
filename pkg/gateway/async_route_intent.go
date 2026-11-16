package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compare the immutable admitted envelope, including legacy receipts. Routing
// pins and transport/tracing metadata may change on a retry; application input
// may not. Do not hash the payload into the receipt ID: changed intent must
// conflict with the old operation, rather than create another operation.
func sameAsyncRouteIntent(a, b state.Invocation) bool {
	method := func(s string) string {
		if s == "" {
			return http.MethodPost
		}
		return s
	}
	path := func(s string) string {
		if s == "" {
			return "/"
		}
		return s
	}
	return method(a.Method) == method(b.Method) && path(a.Path) == path(b.Path) &&
		a.EdgeJWTIssuer == b.EdgeJWTIssuer && a.EdgeJWTSubject == b.EdgeJWTSubject &&
		a.SentByAppID == b.SentByAppID && a.OnCompleteAppID == b.OnCompleteAppID &&
		a.OnSuccessDestinationID == b.OnSuccessDestinationID && a.OnFailureDestinationID == b.OnFailureDestinationID &&
		asyncJSONEqual(a.Payload, b.Payload) && asyncHeadersEqual(a.Headers, b.Headers)
}

func asyncHeadersEqual(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (map[string]string, bool) {
		var headers map[string]string
		if len(raw) != 0 && json.Unmarshal(raw, &headers) != nil {
			return nil, false
		}
		out := make(map[string]string)
		for name, value := range headers {
			name = strings.ToLower(name)
			if name == strings.ToLower(api.ReleaseHeader) || name == strings.ToLower(api.RevisionHeader) ||
				strings.HasPrefix(name, "x-faas-") || strings.HasPrefix(name, "cf-") || strings.HasPrefix(name, "x-forwarded-") {
				continue
			}
			switch name {
			case "idempotency-key", "date", "host", "content-length", "connection", "accept-encoding", "user-agent",
				"forwarded", "via", "x-real-ip", "true-client-ip", "traceparent", "tracestate", "baggage", "x-request-id", "x-gregale-trace-id":
				continue
			}
			out[name] = value
		}
		return out, true
	}
	left, lok := decode(a)
	right, rok := decode(b)
	return lok && rok && reflect.DeepEqual(left, right)
}

func asyncJSONEqual(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		if len(raw) == 0 {
			raw = []byte(`{}`)
		}
		if !json.Valid(raw) {
			return nil, false
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var v any
		err := dec.Decode(&v)
		return v, err == nil
	}
	left, lok := decode(a)
	right, rok := decode(b)
	return lok && rok && sameAsyncJSONValue(left, right)
}

func sameAsyncJSONValue(a, b any) bool {
	switch left := a.(type) {
	case json.Number:
		right, ok := b.(json.Number)
		if !ok {
			return false
		}
		l, lok := asyncDecimal(left.String())
		r, rok := asyncDecimal(right.String())
		return lok && rok && l == r
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, found := right[key]
			if !found || !sameAsyncJSONValue(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i := range left {
			if !sameAsyncJSONValue(left[i], right[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Compare decimal coefficients/exponents without float64 rounding or expanding
// an attacker-controlled exponent. PostgreSQL jsonb normalizes number spelling.
func asyncDecimal(s string) (string, bool) {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	exponent := int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		exponent, err = strconv.ParseInt(s[i+1:], 10, 32)
		if err != nil {
			return "", false
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		exponent -= int64(len(s) - i - 1)
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0", true
	}
	digits := strings.TrimRight(s, "0")
	exponent += int64(len(s) - len(digits))
	return sign + digits + "e" + strconv.FormatInt(exponent, 10), true
}
