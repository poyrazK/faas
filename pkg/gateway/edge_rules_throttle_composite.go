package gateway

// ADR-909 composite throttle keys: one bucket per combination of several
// request fields, e.g. client IP and path.

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// throttleCompositeMaxKeyBytes bounds a composite bucket identity; longer
// combinations are hashed so attacker-chosen header values cannot grow the
// limiter's keys.
const throttleCompositeMaxKeyBytes = 128

// resolveCompositeThrottleKey joins the rule's key fields into one identity.
// A field that is unavailable (untrusted client IP, no GeoIP) fails the
// request closed exactly as a single key_by would; a field the request does
// not carry makes the whole identity missing, so missing_key_policy applies.
func (h *Handler) resolveCompositeThrottleKey(r *http.Request, rule *EdgeRuleThrottleResolved) (string, bool, string) {
	parts := make([]string, 0, len(rule.KeyFields))
	for _, field := range rule.KeyFields {
		kind, header, ok := api.ThrottleKeyFieldName(field)
		if !ok {
			return "", false, "throttle_key_field_invalid"
		}
		var value string
		switch kind {
		case "method":
			value = r.Method
		case "path":
			value = r.URL.Path
		case "header":
			value = r.Header.Get(header)
		default:
			v, present, unavailable := h.resolveThrottleField(r, kind, rule.JWTClaimName)
			if unavailable != "" {
				return "", false, unavailable
			}
			if !present {
				return "", false, ""
			}
			value = v
		}
		if value == "" {
			return "", false, ""
		}
		parts = append(parts, field+"="+value)
	}
	return compositeThrottleKey(parts), true, ""
}

func compositeThrottleKey(parts []string) string {
	key := strings.Join(parts, "\x1f")
	if len(key) <= throttleCompositeMaxKeyBytes {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "h:" + hex.EncodeToString(sum[:16])
}
