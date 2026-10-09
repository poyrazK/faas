package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) getProfileCanaryGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	reader, ok := s.store.(state.ProfileCanaryGateReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("profiling gates are unavailable"))
		return
	}
	out, err := reader.ReadProfileCanaryGate(r.Context(), acct.ID, app.ID, d.ID)
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	if out.Signal != nil {
		s.enrichCanaryProfileSignal(r.Context(), app, out.Signal, &d)
	}
	writeJSON(w, http.StatusOK, out)
}

// Plan binding authorization for the intended traffic change, then let the
// locked state transition revalidate the policy, stage, evidence and lease.
func (s *server) profileGateAdvanceIntent(r *http.Request, acct state.Account, app state.App, d state.Deployment, req api.AdvanceCanaryRequest, worker bool, next int) (int, *api.Problem) {
	if req.ProfileGateRollback && !worker {
		return next, api.ErrValidation("profile rollback requests require the internal worker")
	}
	if worker && req.ProfileGateOverride != nil {
		return next, api.ErrValidation("workers cannot override a profile gate")
	}
	if req.ProfileGateOverride != nil {
		o := req.ProfileGateOverride
		if o.ExpectedPolicyRevision < 0 || len(o.Reason) == 0 || len(o.Reason) > api.ProfileGateOverrideMaxReasonBytes {
			return next, api.ErrValidation("profile override requires a current policy revision and a bounded reason")
		}
	}
	reader, ok := s.store.(state.ProfileCanaryGateReader)
	if !ok {
		return next, api.ErrCapacity("profiling gate decisions are unavailable")
	}
	decision, err := reader.ReadProfileCanaryGate(r.Context(), acct.ID, app.ID, d.ID)
	if err != nil {
		return next, profileInvestigationProblem(err)
	}
	if worker && decision.Status == "regressed" && decision.AutoRollback {
		return 0, nil
	}
	if req.ProfileGateRollback {
		return next, api.NewProblem(http.StatusConflict, api.CodeProfileGateBlocked, "Profile rollback context changed", "The current stage no longer has a confirmed regression with automatic rollback enabled.")
	}
	return next, nil
}
