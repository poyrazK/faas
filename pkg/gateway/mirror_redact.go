// mirror_redact.go — issue #72 / ADR-124 / ADR-125 PR-A3
// mirror-invocation header stripping + result classification.
//
// The mirror goroutine (pkg/gateway/mirror_dispatch.go) builds a
// per-request mirror invocation: same path / method / body as the
// source request, but with a stripped header set so customer auth
// material never reaches the customer's own mirror deployment. The
// always-stripped set covers every header a customer authentication
// surface might leak through; the customer-supplied redact_headers
// list lets a customer opt further fields out (typical use:
// custom `X-Tenant-Secret` they want kept out of the mirror even
// though it's not standard auth material).
//
// Result classification: status_diff / schema_diff / body_diff /
// crashed. The (status_diff, crashed) pair drives the dashboard
// chip — "mismatch ratio" is status_diff / total, "crash ratio"
// is crashed / total. schema_diff and bodyDiff are byte-equal
// signatures for A3 (sha256 of the response body); JCS semantic
// diff is a follow-on per ADR-124 §Follow-ons.

package gateway

import (
	"crypto/sha256"
	"net/http"
	"net/textproto"

	"github.com/onebox-faas/faas/pkg/state"
)

// alwaysStrippedHeaders (issue #72 / ADR-125 PR-A3) is the
// header set every mirror invocation strips from the source
// request, regardless of customer configuration. The set covers:
//
//   - Authorization / Proxy-Authorization: standard auth headers
//     (Basic, Bearer, ...); leaking them lets the mirror VM
//     impersonate the source caller.
//   - Cookie / Set-Cookie: session material; cookies can carry
//     auth tokens the customer didn't think of as "auth headers".
//   - X-API-Key: per-vendor auth on top of OAuth/JWT. Many
//     platforms (Stripe-style, GitHub-style) use this header.
//   - WWW-Authenticate: server→client auth challenge; safe to
//     keep out of the mirror probe, the mirror VM never sees a
//     401 anyway because it receives the forwarded 2xx/4xx.
//
// Lower-case, matched case-insensitively against http.Header
// keys per Go's canonicalisation (textproto.CanonicalMIMEHeaderKey
// already lowercases the first letter of each dash-delimited word,
// but callers may pass "AUTHORIZATION" verbatim). http.Header.Del
// uses CanonicalMIMEHeaderKey internally, so a plain Del(key)
// after Set-ing the always-stripped names handles both shapes.
var alwaysStrippedHeaders = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
	"X-API-Key",
	"Proxy-Authorization",
	"WWW-Authenticate",
}

// StrippedRequestHeaders (issue #72 / ADR-124 / ADR-125 PR-A3)
// returns a fresh http.Header carrying every header from src
// EXCEPT those whose name (case-insensitive) appears in
// alwaysStrippedHeaders or in rule.RedactHeaders. The mirror
// goroutine uses the result as the outgoing request's headers.
//
// The src parameter is NOT mutated — the caller keeps the
// customer's original header set for the source VM round-trip.
// Implementation note: build a map of the stripped names first
// (O(s) where s = |alwaysStrippedHeaders| + |rule.RedactHeaders|)
// so the per-header walk is O(1) lookup. A customer with 1000
// headers on the source request still pays linear time.
func StrippedRequestHeaders(rule state.MirrorRule, src http.Header) http.Header {
	stripped := make(map[string]struct{}, len(alwaysStrippedHeaders)+len(rule.RedactHeaders))
	for _, k := range alwaysStrippedHeaders {
		stripped[textproto.CanonicalMIMEHeaderKey(k)] = struct{}{}
	}
	for _, k := range rule.RedactHeaders {
		stripped[textproto.CanonicalMIMEHeaderKey(k)] = struct{}{}
	}
	dst := make(http.Header, len(src))
	for k, vs := range src {
		if _, drop := stripped[textproto.CanonicalMIMEHeaderKey(k)]; drop {
			continue
		}
		// Copy the slice so a downstream mutation doesn't
		// leak back into the source request.
		cp := make([]string, len(vs))
		copy(cp, vs)
		dst[k] = cp
	}
	return dst
}

// ClassifyResult (issue #72 / ADR-124 / ADR-125 PR-A3) takes
// the source and mirror response shapes and returns the four
// outcome flags the ledger row + metric emit:
//
//   - statusDiff:  src/mirror HTTP status codes differ
//   - schemaDiff:  sha256 of the response bodies differs
//     (byte-equal signatures for A3; JCS
//     semantic hash is a follow-on)
//   - bodyDiff:    same predicate as schemaDiff; field is
//     emitted distinct to keep the dashboard
//     schema stable when semantic diff lands in
//     A4.
//   - crashed:     mirror VM couldn't produce a response
//     (status >= 500 OR mirrorStatus == 0
//     meaning the round-trip timed out /
//     failed).
//
// All four flags participate in the ledger row's boolean
// columns and the per-rule metric increments
// (gateway_mirror_dispatched_total{result=...}). Callers MUST
// not assume any ordering between the four — the classification
// is a flat AND of independent predicates.
//
// mirrorStatus == 0 is the goroutine's signal that the round-
// trip produced no HTTP response (transport error, deadline
// exceeded, mirror VM not yet up). A source status of 0 means
// the captured source body was nil (capture failure); that's
// also "we don't know what the source did", and the
// classify emits statusDiff=true so the dashboard surfaces
// "everything looks wrong" rather than a silent no-diff.
func ClassifyResult(srcStatus int, srcBody []byte, mirrorStatus int, mirrorBody []byte) (statusDiff, schemaDiff, bodyDiff, crashed bool) {
	statusDiff, schemaDiff, bodyDiff, crashed, _, _ = ClassifyResultWithHashes(srcStatus, srcBody, mirrorStatus, mirrorBody)
	return
}

// ClassifyResultWithHashes returns the same classification plus the exact
// SHA-256 fingerprints used for the body comparison. Ledger and replay callers
// use these values so response content is hashed only once.
func ClassifyResultWithHashes(srcStatus int, srcBody []byte, mirrorStatus int, mirrorBody []byte) (statusDiff, schemaDiff, bodyDiff, crashed bool, srcFingerprint, mirrorFingerprint [sha256.Size]byte) {
	if srcStatus != mirrorStatus {
		statusDiff = true
	}
	srcHash := sha256.Sum256(srcBody)
	mirrorHash := sha256.Sum256(mirrorBody)
	if srcHash != mirrorHash {
		schemaDiff = true
		bodyDiff = true
	}
	if mirrorStatus == 0 || mirrorStatus >= 500 {
		crashed = true
	}
	return statusDiff, schemaDiff, bodyDiff, crashed, srcHash, mirrorHash
}
