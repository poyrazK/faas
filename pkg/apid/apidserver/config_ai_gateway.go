package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// configureAIGateway seeds the ai_gateway platform flag (ADR-1700) from the
// release gate (ADR-991) and staged rollout (ADR-998) settings. The gate is
// off unless ai_gateway_enabled or FAAS_AI_GATEWAY_ENABLED=1 turns it on;
// ai_gateway_accounts or FAAS_AI_GATEWAY_ACCOUNTS then limits it to listed
// accounts. They are only the boot fallback: an operator value written
// through /v1/admin/config wins and applies without a restart.
func (s *server) configureAIGateway(cfg Config, getenv func(string) string) error {
	enabled := cfg.AIGatewayEnabled
	if v := getenv("FAAS_AI_GATEWAY_ENABLED"); v != "" {
		enabled = v == "1"
	}
	rollout, err := outbound.ParseAIGatewayRollout(cfg.AIGatewayAccounts, getenv("FAAS_AI_GATEWAY_ACCOUNTS"))
	if err != nil {
		return err
	}
	s.seedPlatformFlag(platformflags.AIGateway, outbound.AIGatewayFlagBootstrap(enabled, rollout))
	return nil
}

func writeAIGatewayUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "outbound_ai_gateway_unavailable",
		"AI gateway unavailable", "The AI gateway is not enabled on this platform"))
}

// aiGatewayDenied writes the reason an account cannot use the AI gateway
// (ADR-998): the platform kill switch, the account rollout, or the plan. It
// reports whether it wrote a refusal.
func (s *server) aiGatewayDenied(w http.ResponseWriter, acct state.Account) bool {
	switch {
	case !s.platformFlagOn(platformflags.AIGateway):
		writeAIGatewayUnavailable(w)
	case !s.platformFlagEnabled(platformflags.AIGateway, acct):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "outbound_ai_gateway_unavailable",
			"AI gateway unavailable", "The AI gateway preview is not enabled for this account yet"))
	case !outbound.AIGatewayEntitled(acct.Plan):
		api.WriteProblem(w, api.ErrPlanFeatureGated("ai_gateway", acct.Plan))
	default:
		return false
	}
	return true
}

// aiGateway refuses an AI gateway endpoint for an account the kill switch,
// the rollout or the plan excludes. The flag is read per request, so an
// operator change applies to the next request.
func (s *server) aiGateway(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if s.aiGatewayDenied(w, acct) {
			return
		}
		next(w, r, acct)
	}
}
