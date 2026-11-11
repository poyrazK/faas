package api

import (
	"net/url"
	"strings"
)

// ParseEdgeRuleQuery parses a raw query string the way the apps behind the
// edge read it: pairs are separated by "&" only, and a ";" stays inside the
// value (Express's qs, Python's parse_qs since 3.10, Rack, PHP). Go's
// url.ParseQuery instead drops every pair that contains ";", while the
// gateway forwards the raw query unchanged, so a validate rule or a
// query:<name> condition saw no value where the app read one: ?id=1;--
// skipped an integer check on id (ADR-20261107190000).
//
// Every pair is returned. A key or value whose percent-escapes do not decode
// is kept as written and reported by the error, so a caller that must be
// exact (a validate rule) can refuse the request.
func ParseEdgeRuleQuery(raw string) (url.Values, error) {
	out := url.Values{}
	var firstErr error
	for raw != "" {
		var pair string
		pair, raw, _ = strings.Cut(raw, "&")
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		k, err := url.QueryUnescape(key)
		if err != nil {
			k = key
			if firstErr == nil {
				firstErr = err
			}
		}
		v, err := url.QueryUnescape(value)
		if err != nil {
			v = value
			if firstErr == nil {
				firstErr = err
			}
		}
		out[k] = append(out[k], v)
	}
	return out, firstErr
}
