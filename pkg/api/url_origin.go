package api

import (
	"net/url"
	"strings"
)

// SameURLOrigin reports whether two URLs name the same scheme and host, so a
// credential stored for one may be sent to the other. An unparsable URL never
// matches. Updates that move a log drain or a realtime callback to another
// host must supply its credential again (bug hunt 2026-10-28 H107).
func SameURLOrigin(a, b string) bool {
	left, err := url.Parse(strings.TrimSpace(a))
	if err != nil || left.Host == "" {
		return false
	}
	right, err := url.Parse(strings.TrimSpace(b))
	if err != nil || right.Host == "" {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
