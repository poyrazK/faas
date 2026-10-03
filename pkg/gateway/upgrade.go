// Upgrade detection helper for the raw-bytes bridge path
// (issue #676 / ADR-080). The detector lives in pkg/gateway so
// both the gatewayd-internal handler (Handler.ServeHTTP) and the
// gatewayd-public → gatewayd-internal hop (internal_proxy.go) can
// share the same case-insensitive RFC 7230 §3.2 check without
// duplicating the parse.
//
// Why the raw-bytes bridge needs Upgrade detection:
//
//   - The plain-HTTP forwarder (pkg/gateway/forwardproxy.go) strips
//     `Connection` + `Upgrade` from inbound headers (RFC 7230 §6.1
//     hop-by-hop). That strip is correct for plain HTTP — it would
//     destroy the WebSocket / h2c / MQTT-over-WS handshake otherwise.
//   - gatewayd-internal must therefore ROUTE upgrade requests to a
//     different forwarder BEFORE the strip fires. The detector here
//     is the single source of truth for "is this an upgrade request?"
//     and is called from both the handler and the internal hop.
//
// What the detector accepts:
//
//   - Connection: Upgrade (case-insensitive per RFC 7230 §3.2). The
//     `Connection` header value is a comma-separated list of tokens;
//     a token equal to `Upgrade` (case-insensitive) is the load-bearing
//     match.
//   - A non-empty Upgrade header. The detector is protocol-agnostic —
//     any value (`websocket`, `h2c`, `mqtt`, ...) flows through the
//     raw-bytes bridge verbatim because the gRPC ForwardRawStream
//     carries bytes end-to-end.
//
// What it rejects:
//
//   - Requests with no Upgrade token (default HTTP/1.1 traffic). The
//     plain-HTTP forwarder keeps handling these.
//   - Requests with an Upgrade token but an empty Upgrade header
//     (malformed; treated as plain HTTP so the customer sees a normal
//     200 / 404 / 502 instead of a confusing 101).
package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// wsContextKey is the unexported context key that the
// cmd/gatewayd-internal three-input gate (handler.go:2899) uses to
// stamp the resolved app.Plan + the per-Handler Metrics pointer
// onto the inbound request context. The raw forwarder
// (pkg/gateway/forwardproxy.go's rawStreamOnceWithEvents) reads
// the pair from the context to label its gateway_ws_*
// observations without having to thread (plan, metrics) through
// the public Handler/ForwardingRawReverseProxy signature.
//
// The metrics pointer is stored as *Metrics (not *wire.OpsMetrics)
// because gatewayd-internal's local Prometheus registry is the
// per-Handler one (pkg/gateway/metrics.go), which already
// registers the rest of the gateway_* series. A separate
// daemon-wide registry would split the WS surface from the wake
// / cold-boot / request-* surface on /metrics, complicating the
// §12 dashboard wiring.
type wsContextKey struct{}

// withWSContext stamps plan + metrics onto a copy of ctx. nil
// metrics is allowed — the raw forwarder checks for nil before
// calling helpers (the helpers themselves are nil-safe). Used by
// the three-input gate in pkg/gateway/handler.go's ServeHTTP.
//
// PR-B code review finding #3: the previous short-circuit
// (`if plan == "" && m == nil { return ctx }`) was asymmetric.
// When the caller passed plan != "" with m == nil (the public
// → internal hop before the handler stamps its own metrics),
// the receiver later read a context carrying only the plan
// with no metrics — defensible but the asymmetry was a
// footgun for future stamp sites. The helper now always
// stamps; the metric helpers + wsContextFrom zero-value
// handling cover the "no real metrics" case uniformly.
func withWSContext(ctx context.Context, plan api.Plan, m *Metrics) context.Context {
	return context.WithValue(ctx, wsContextKey{}, wsContext{plan: plan, metrics: m})
}

// wsContextFrom returns the (plan, metrics) pair stashed by
// withWSContext, or zero values if absent (the pre-PR-B test
// corpus + the public→internal hop before the handler stamps it).
func wsContextFrom(ctx context.Context) (api.Plan, *Metrics) {
	v, ok := ctx.Value(wsContextKey{}).(wsContext)
	if !ok {
		return "", nil
	}
	return v.plan, v.metrics
}

type wsContext struct {
	plan    api.Plan
	metrics *Metrics
}

// isUpgradeRequest reports whether the inbound request carries the
// RFC 7230 §6.1 hop-by-hop Upgrade token. The detector accepts ANY
// value of the Upgrade header (websocket, h2c, mqtt, ...) — the raw
// RPC carries bytes verbatim so a non-WS token simply flows to the
// guest unchanged. Case-insensitive per RFC 7230 §3.2.
//
// Both `Connection: Upgrade` (single token) and
// `Connection: keep-alive, Upgrade` (token list) match; the parser
// splits the value on commas and trims per-spec whitespace.
//
// The `Upgrade:` header MUST be non-empty; a Connection header that
// mentions Upgrade without an actual Upgrade header is a malformed
// request and is treated as plain HTTP so the customer sees a normal
// response. The bridge's load-bearing protocol detection (does the
// guest speak websocket? h2c? mqtt?) is delegated to the guest — the
// gateway is just a transparent byte pipe.
func isUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	// Iterate every Connection header value (multiple Connection
	// headers are allowed per RFC 7230 §3.2 — comma-separated or
	// repeated; net/http's Values() flattens into a slice).
	for _, conn := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(conn, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "Upgrade") {
				return r.Header.Get("Upgrade") != ""
			}
		}
	}
	return false
}

// isWebSocketHandshake narrows the generic Upgrade detector to the browser
// protocol whose native API cannot attach Gregale's release header. Other
// Upgrade protocols continue to use their explicit headers as before.
func isWebSocketHandshake(r *http.Request) bool {
	if !isUpgradeRequest(r) {
		return false
	}
	for _, value := range r.Header.Values("Upgrade") {
		for _, protocol := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(protocol), "websocket") {
				return true
			}
		}
	}
	return false
}
