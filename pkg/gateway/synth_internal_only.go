package gateway

// synth_internal_only.go — ADR-119 ingress-control gate for
// the cron-fired wake path. Schedd cron fires /v1/synthesize
// (synth.go::handleSynthesize, line 213) which dispatches
// directly to s.dispatcher.Wake — bypassing Handler.ServeHTTP
// and therefore bypassing applyIngressInternalSvc
// (handler.go). Without this gate, an attacker who somehow
// obtained access to the unix socket (e.g. compromised member
// of the faas group) could wake an internal_only app by
// posting a /v1/synthesize request with a forged app_id, even
// without a valid JWT. The Handler-side gate covers the
// HTTP-front-door path; this synth-side gate closes the cron
// bypass.
//
// See docs/adr/119-app-public-auth-internal-only.md
// "First caller: schedd → gatewayd-internal via cron" and
// the design-gap note for the full rationale.
//
// The gate reuses the same InternalSvcVerifier as the HTTP-
// front-door gate (cmd/gatewayd-internal/internal_svc_verifier.go
// constructs one and wires it into both Handler and
// SynthServer). Single source of truth on the allowlist.

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// WithInternalSvcVerifier wires the per-service public-key
// allowlist into the SynthServer. Mirrors the Handler-side
// WithInternalSvcVerifier (handler.go). Called from
// cmd/gatewayd-internal/run.go after the bridge is constructed.
// nil = the gate is disabled; an internal_only request that
// reaches handleSynthesize with no verifier wired would 500
// (operator_error), the same loud posture as the HTTP-front-
// door side.
func (s *SynthServer) WithInternalSvcVerifier(v InternalSvcVerifier) {
	s.internalSvcVerifier = v
}

// applyIngressInternalSvc is the synth-side analogue of
// Handler.applyIngressInternalSvc. Reads the inbound
// Authorization: Bearer JWT (the schedd-attached one — see
// cmd/schedd/internal_svc_minter.go) and verifies it against
// the per-service public-key allowlist. On failure: 403.
// On pass-through: returns false.
//
// applySynthIngressPolicy verifies a fresh declared mode before calling this
// token gate on each of the three synthetic routes. Failed or unknown policy
// reads refuse before reaching this helper.
//
// The `from` argument tags the audit payload so dashboards
// can split the three call surfaces. The three current values:
//   - "synth"        — handleSynthesize (legacy wake-only path)
//   - "synth_dispatch" — handleInvocationDispatch (Move 1 single
//     invocation envelope)
//   - "synth_batch"  — handleInvocationDispatchBatch (Move 1 batch)
//
// Failure modes are identical to the HTTP-side gate:
//   - No verifier wired → 500 operator_error
//   - No Authorization header → 403
//     (instances.public_auth_internal_missing audit + outcome=blocked metric)
//   - Token present but verify failed → 403
//     (instances.public_auth_internal_invalid audit + outcome=blocked metric)
//   - Verify success → pass-through (return false)
//
// Audit redaction invariant: the JWT string is NEVER echoed;
// only the error reason code (audience_mismatch | expired |
// not_yet_valid | unknown_service | signature_invalid |
// malformed | empty_allowlist) and app_id flow into the audit.
func (s *SynthServer) applyIngressInternalSvc(w http.ResponseWriter, r *http.Request, appID, mode, from string) bool {
	if mode != publicAuthModeInternalOnly {
		return false
	}
	if s.internalSvcVerifier == nil {
		if s.log != nil {
			s.log.Error("synth: app in internal_only mode but no InternalSvcVerifier wired — refusing",
				"app_id", logsanitize.Field(appID), "from", from)
		}
		if s.metrics != nil {
			s.metrics.ObserveInternalAuthMatch("blocked")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"app is misconfigured",
			"internal_only mode requires the gatewayd-internal verifier; check FAAS_INTERNAL_SVC_PUBKEYS"))
		return true
	}
	tok, ok := bearerFromHeader(r)
	if !ok {
		if s.synthAuditEmit != nil {
			s.synthAuditEmit(r.Context(), "instances.public_auth_internal_missing", nil, map[string]any{
				"app_id": appID,
				"from":   from,
				"reason": "missing_authorization_header",
			})
		}
		if s.metrics != nil {
			s.metrics.ObserveInternalAuthMatch("blocked")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden,
			"internal-only mode requires Authorization: Bearer",
			"this app is reachable only by Gregale daemons presenting a JWT with aud='gregale.internal'"))
		return true
	}
	svcName, err := s.internalSvcVerifier.Verify(r.Context(), tok)
	if err != nil {
		// Map the typed error to a closed reason vocabulary
		// (identical to Handler.applyIngressInternalSvc). The
		// errInternalSvc* aliases live in pkg/gateway so the
		// gate can match without importing pkg/internalsvc.
		reason := "signature_invalid"
		switch {
		case isErrAudience(err):
			reason = "audience_mismatch"
		case isErrExpired(err):
			reason = "expired"
		case isErrNotYetValid(err):
			reason = "not_yet_valid"
		case isErrUnknownService(err):
			reason = "unknown_service"
		case isErrMalformed(err):
			reason = "malformed"
		case isErrEmptyAllowlist(err):
			reason = "empty_allowlist"
		}
		if s.synthAuditEmit != nil {
			s.synthAuditEmit(r.Context(), "instances.public_auth_internal_invalid", nil, map[string]any{
				"app_id":   appID,
				"from":     from,
				"reason":   reason,
				"svc_name": svcName, // "" on signature failure
			})
		}
		if s.metrics != nil {
			s.metrics.ObserveInternalAuthMatch("blocked")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden,
			"internal token verification failed",
			"present a valid Gregale-internal JWT (aud='gregale.internal')"))
		return true
	}
	if s.metrics != nil {
		s.metrics.ObserveInternalAuthMatch("matched")
	}
	_ = svcName // pass-through; the verified svcName is recoverable
	// from r.Context via InternalSvcFromContext if any
	// downstream audit code needs it
	return false
}

// isErr* helpers translate the cmd-side error message strings
// (set by internal_svc_verifier.go's gatewayAudienceMismatch()
// etc.) to the gate's reason mapping. The cmd-side bridge
// can't expose typed sentinels from pkg/internalsvc (that
// would require exporting pkg/gateway's errInternalSvc*
// aliases), so we match by .Error() string. This is the same
// shape ApplyEdgeRuleJWT uses for the kind=jwt gate.
//
// If pkg/internalsvc ever changes its error messages, this
// switch must follow. A unit test in
// pkg/gateway/synth_internal_only_test.go pins the strings.
//
// Why string match vs identity: pkg/gateway's errInternalSvc*
// aliases and pkg/internalsvc's Err* sentinels are distinct
// values; the bridge constructs fresh errors with the same
// .Error() text. Identity match would never fire. String
// match is the only path that works across the bridge.
//
// The errors here are constructed in
// cmd/gatewayd-internal/internal_svc_verifier.go (the
// gatewayAudienceMismatch() etc. closures). They have the
// exact .Error() string from pkg/internalsvc.Err*.String(),
// which is stable per the §3 ADR-119 contract.

func isErrAudience(err error) bool {
	return err != nil && containsToken(err.Error(), "aud claim does not match")
}
func isErrExpired(err error) bool {
	return err != nil && containsToken(err.Error(), "token expired")
}
func isErrNotYetValid(err error) bool {
	return err != nil && containsToken(err.Error(), "token not yet valid")
}
func isErrUnknownService(err error) bool {
	return err != nil && containsToken(err.Error(), "svcName not in per-service allowlist")
}
func isErrMalformed(err error) bool {
	return err != nil && containsToken(err.Error(), "token malformed")
}
func isErrEmptyAllowlist(err error) bool {
	return err != nil && containsToken(err.Error(), "per-service allowlist must not be empty")
}

// containsToken is a tiny substring-match helper to avoid
// pulling in strings.Contains just for these six call sites
// (cmd/gatewayd-internal already imports strings — the gate
// file stays stdlib-light). Returns true when needle appears
// in haystack. Empty needle returns false (no empty match).
func containsToken(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// _ pins context import for future expansion (e.g. ctx-
// cancellation on the gate's verifier call). pkg/gateway
// already imports context elsewhere; this var ensures the
// import survives a future cleanup pass that removes the
// only direct references.
var _ = context.Background
