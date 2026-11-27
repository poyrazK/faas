package apidserver

// Operator control-plane health report (ADR-20261126120000).
//
//	GET /v1/admin/obs/control-plane

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/controlplane"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsControlPlane(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid control-plane query", "control-plane health accepts no query parameters"))
		return
	}
	now := time.Now().UTC()
	in := controlplane.Input{Now: now, Samples: controlplane.Read(r.Context(), s.promqlClient, now)}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, controlplane.Build(in))
}
