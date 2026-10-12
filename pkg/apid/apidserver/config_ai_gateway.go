package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// configureAIGateway reads the AI gateway release gate (ADR-991). It is off
// unless ai_gateway_enabled or FAAS_AI_GATEWAY_ENABLED=1 turns it on, so the
// AI APIs stay closed until an operator enables them.
func (s *server) configureAIGateway(cfg Config, getenv func(string) string) {
	s.aiGatewayEnabled = cfg.AIGatewayEnabled
	if v := getenv("FAAS_AI_GATEWAY_ENABLED"); v != "" {
		s.aiGatewayEnabled = v == "1"
	}
}

func writeAIGatewayUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "outbound_ai_gateway_unavailable",
		"AI gateway unavailable", "The AI gateway is not enabled on this platform"))
}

// aiGateway refuses an AI gateway endpoint while the release gate is off.
// The gate is read per request, so an operator change applies on restart
// without re-registering routes.
func (s *server) aiGateway(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.aiGatewayEnabled {
			writeAIGatewayUnavailable(w)
			return
		}
		next(w, r, acct)
	}
}
