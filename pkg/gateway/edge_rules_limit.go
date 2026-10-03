package gateway

import "net/http"

// Edge rule kind=limit subset (ADR-091 D24, see
// migrations/00219_edge_rules_kind_limit.sql).
//
// kind=limit is the standalone body-size primitive: a customer who
// only wants per-route body-size protection ("POST /upload ≤ 5 MB,
// POST /users ≤ 1 MB, POST /webhooks ≤ 2 MB") declares this kind
// without shipping a JSON Schema. The applier (handler.go's
// applyEdgeRuleLimit, §4.1.2.8c) installs http.MaxBytesReader on
// r.Body at the per-rule cap and short-circuits oversized requests
// with 413 request_too_large — and, more importantly, performs a
// Content-Length fast-path deny so a 30 MB body on a 5 MB cap costs
// zero bytes of buffering (a MaxBytesReader alone only trips when
// something reads the body, and on this hot path nothing reads it
// until the proxy leg).
//
// MaxBodyBytes is the buffered-path cap (≤ api.MaxRequestBodyBytes,
// 25 MiB); MaxBodyBytesStreaming is the streaming opt-in cap (≤
// api.MaxBodyBytesStreaming, 100 MiB). The streaming field is 0
// when the customer didn't set it; the applier falls back to
// MaxBodyBytes for streaming requests in that case. The streaming
// detection lives in `streamingFor(h, r, app)` at handler.go —
// pkg/gateway keeps the matcher free of io.Reader juggling.

// EdgeRuleLimitResolved is the kind=limit subset the gateway matcher
// reads on every request. Mirrors EdgeRuleValidateResolved
// (pkg/gateway/edge_rules.go:258) shape-for-shape minus the
// JSON-Schema fields the validate applier needs and plus the two
// body caps the limit applier needs.
//
// MaxBodyBytes is always > 0 post-compile — the cmd-side
// compileLimitRules (cmd/gatewayd-internal/edge_rules.go) clamps
// non-positive values to api.MaxRequestBodyBytes as defence-in-depth
// against a direct-DB write that bypassed apid-Validate (the
// e2e helper seedEdgeRuleDirect at
// cmd/e2e/edge_rules_common_test.go:128 does exactly that).
//
// MaxBodyBytesStreaming is the streaming opt-in cap; 0 means "no
// streaming carve-out — fall back to MaxBodyBytes". The applier
// picks the right cap per request based on whether the inbound is
// on the streaming path.
type EdgeRuleLimitResolved struct {
	ID                    string
	AccountID             string
	AppID                 string
	Priority              int
	PathGlob              string          // "" = any path
	Methods               map[string]bool // nil = any method
	MatchHeaders          map[string]string
	MaxBodyBytes          int // always > 0 post-compile
	MaxBodyBytesStreaming int // 0 = no streaming carve-out
}

// PickFirstLimitMatch is the priority-ASC + methods + path-glob
// filter used by cmd/gatewayd-internal/edge_rules.go's MatchLimit
// after the cache returns the priority-ordered slice. Byte-for-byte
// mirror of PickFirstValidateMatch (pkg/gateway/edge_rules.go:920);
// the small copy keeps the per-kind return type precise without
// paying for a runtime-type assertion on every request.
//
// methods filter:
//
//   - empty map = any method matches
//   - non-empty map = request method MUST be present (case-folded
//     to upper by the loader)
//
// path glob: passed through pathGlobMatch (edge_rules.go:1136), the
// same adapter every other per-kind matcher uses — stdlib path.Match
// underneath, with "" and "*" short-circuited to match-all before the
// stdlib call (path.Match itself would return false for "").
// "/api/*" = prefix-wildcard on the second segment.
func PickFirstLimitMatch(rules []EdgeRuleLimitResolved, requestPath, method string, requestHeaders ...http.Header) *EdgeRuleLimitResolved {
	for i := range rules {
		r := &rules[i]
		if len(requestHeaders) > 0 && !RequestHeaderConditionsMatch(r.MatchHeaders, requestHeaders[0]) {
			continue
		}
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, requestPath)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}
