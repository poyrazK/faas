// Closed-set constants for the per-app wire-protocol selector
// (ADR-124 §Decision 1). The set {http1, http2, grpc} is universal —
// http1 and http2 are admitted on every plan; grpc is plan-gated to
// Hobby+/Pro/Scale via Plan.AppProtocolAllowed at
// pkg/api/limits.go.
//
// The constants are the canonical spellings — handlers, validators,
// and SQL column literals all read these values rather than
// hard-coding the string. A new closed-set value requires a new ADR
// (the same convention as DataUpstreamKind and other API enums).
//
// Mirrors the StreamingEnabled boolean taxonomy — the per-app
// AppProtocol field replaces a stack of ad-hoc request-header
// switches that previously had no first-class customer knob.
package api

const (
	// AppProtocolHTTP1 is the legacy buffered H1 framing. The
	// column default for every pre-existing app. Universal,
	// opt-out-by-nothing (a customer may freely PATCH back to
	// this value).
	AppProtocolHTTP1 = "http1"

	// AppProtocolHTTP2 is the H2 (h2c internally, ALPN h2 at
	// the public edge) framing. Universal opt-in — every plan
	// may set this. Uses the existing public→internal H2C hop
	// (ADR-079) plus the vmmd-stream-bridge H2C inner leg
	// (issue #686 / PR #750). The internal hop is uniform;
	// the per-app value controls only the customer's chosen
	// framing at the edge.
	AppProtocolHTTP2 = "http2"

	// AppProtocolGRPC is the gRPC-over-H2 framing. Plan-gated
	// to Hobby+/Pro/Scale (Plan.AppProtocolAllowed returns
	// false on Free). The framing-over-the-wire is H2 with
	// HTTP trailers — every gRPC unary call and most
	// server-streaming RPCs round-trip correctly; long-lived
	// bidirectional RPCs need the follow-on ADR-125
	// (end-to-end H2 inside the guest) to avoid framing
	// artefacts at the bridge boundary.
	AppProtocolGRPC = "grpc"
)

// AppProtocolClosedSet is the canonical {http1, http2, grpc}
// enumeration. Order matches the SQL CHECK constraint
// apps_app_protocol_chk in migrations/00378.
var AppProtocolClosedSet = []string{
	AppProtocolHTTP1,
	AppProtocolHTTP2,
	AppProtocolGRPC,
}

// IsValidAppProtocol reports whether the value is in the closed
// set. Used by apid's request validators (handlers.go::buildApp
// and handlers_ext.go::updateApp), the CLI flag validator
// (commands2.go::cmdApp + commands5.go::cmdAppScale), and the
// deploydiff quota gate before the value reaches SQL — the SQL
// layer also enforces the check via apps_app_protocol_chk, but
// pinning the validation in Go keeps the apid problem code
// (CodeAppProtocolInvalid) deterministic before SQL is touched.
// Iterates AppProtocolClosedSet rather than enumerating the cases
// here so a future closed-set widening (new ADR) only touches the
// slice.
func IsValidAppProtocol(protocol string) bool {
	for _, v := range AppProtocolClosedSet {
		if v == protocol {
			return true
		}
	}
	return false
}
