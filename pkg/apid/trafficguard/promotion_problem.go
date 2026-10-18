package trafficguard

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func PromotionProblem(err error) *api.Problem {
	switch {
	case state.IsMCPReadinessRequired(err):
		return api.NewProblem(409, api.CodeMCPReadinessRequired, "MCP readiness required", "Run server-side readiness probes for the current deployment and policy, or create an audited emergency override.")
	case errors.Is(err, state.ErrMCPAdmissionPersistence):
		return api.ErrMCPAdmissionEvidenceUnavailable()
	case errors.Is(err, state.ErrMCPAccessPolicyChanged):
		return api.ErrMCPAccessPolicyChanged()
	case errors.Is(err, state.ErrMCPAccountRevisionChanged):
		return api.ErrMCPGatewayPromotion(api.CodeMCPAccountRevisionChanged)
	case errors.Is(err, state.ErrMCPGatewayEvidenceUnavailable):
		return api.ErrMCPGatewayPromotion(api.CodeMCPGatewayEvidenceUnavailable)
	case errors.Is(err, state.ErrMCPGatewayEvidenceStale):
		return api.ErrMCPGatewayPromotion(api.CodeMCPGatewayEvidenceStale)
	case errors.Is(err, state.ErrMCPGatewayReplayPending):
		return api.ErrMCPGatewayPromotion(api.CodeMCPGatewayReplayPending)
	}
	return nil
}
