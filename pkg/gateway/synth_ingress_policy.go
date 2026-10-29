// adr: 570
package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// WithVerifiedAppModeLookup supplies the fresh, context-aware ingress mode
// projection. It must be configured before serving; nil is fixture compatibility.
func (s *SynthServer) WithVerifiedAppModeLookup(lookup func(context.Context, string) (string, error)) *SynthServer {
	s.appPublicAuthMode = lookup
	return s
}

func (s *SynthServer) applySynthIngressPolicy(w http.ResponseWriter, r *http.Request, appID, from string) bool {
	if s.appPublicAuthMode == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.TrafficServicePolicyReadTimeout)
	defer cancel()
	mode, err := "", ctx.Err()
	if err == nil {
		mode, err = s.appPublicAuthMode(ctx, appID)
	}
	if err != nil || ctx.Err() != nil || !knownSyntheticIngressMode(mode) {
		recordTrafficRefusal(r.Context(), "policy_unavailable")
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeTrafficPolicyUnavailable,
			"Traffic policy unavailable", "Gregale could not verify this app's ingress policy. Retry shortly."))
		return true
	}
	return s.applyIngressInternalSvc(w, r, appID, mode, from)
}

func knownSyntheticIngressMode(mode string) bool {
	switch mode {
	case state.AppPublicAuthModeOpen, state.AppPublicAuthModeBearer, state.AppPublicAuthModeBasic,
		state.AppPublicAuthModeIPAllowlist, state.AppPublicAuthModeInternalOnly:
		return true
	default:
		return false
	}
}
