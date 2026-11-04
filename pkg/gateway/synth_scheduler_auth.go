package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// requireSchedulerToken answers 403 unless the request carries a schedd
// internal-service token minted for appID. The synth routes wake and invoke
// any app by ID and return its response; gatewayd-public forwards public
// traffic into the same listener, so "the socket is the auth" did not hold
// (ADR-20261103090000). Every synth route calls this before it acts. With no
// verifier configured (no cluster signing key and no
// FAAS_INTERNAL_SVC_PUBKEYS) nothing can be verified; the public edge's
// refusal of these paths is then the only guard, and the gateway says so at
// boot.
func (s *SynthServer) requireSchedulerToken(w http.ResponseWriter, r *http.Request, appID, from string) bool {
	if s.internalSvcVerifier == nil {
		return false
	}
	reason := "missing_authorization_header"
	if tok, ok := bearerFromHeader(r); ok {
		svc, err := s.internalSvcVerifier.Verify(r.Context(), tok)
		switch {
		case err != nil:
			reason = "invalid_token"
		case svc != "schedd":
			reason = "not_scheduler"
		case internalsvc.StringClaim(tok, "app_id") != appID:
			reason = "app_mismatch"
		default:
			return false
		}
	}
	if s.metrics != nil {
		s.metrics.ObserveInternalAuthMatch("blocked")
	}
	if s.synthAuditEmit != nil {
		s.synthAuditEmit(r.Context(), "instances.synth_unauthorized", nil, map[string]any{
			"app_id": appID,
			"from":   from,
			"reason": reason,
		})
	}
	if s.log != nil {
		s.log.Warn("gateway synth: refused a request without a scheduler token",
			"app_id", logsanitize.Field(appID), "from", from, "reason", reason)
	}
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
		"Scheduler token required", "this endpoint accepts only the Gregale scheduler"))
	return true
}
