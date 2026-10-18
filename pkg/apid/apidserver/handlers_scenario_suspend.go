package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/rehearsal"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/state"
)

func scenarioSuspendResumeDigest(req api.RegisterScenarioTestRequest) string {
	_, d, _ := api.ScenarioSuspendResumeEnrollment(req)
	return d
}
func (s *server) scenarioSuspendResumeSupported(w http.ResponseWriter, req api.RegisterScenarioTestRequest) bool {
	if p := rehearsal.ValidateSuspendResume(req, s.rehearsalSuspendResumeEnabled, s.rehearsalRunningSuspendEnabled); p != nil {
		api.WriteProblem(w, p)
		return false
	}
	if req.SuspendResume != nil {
		if _, ok := s.store.(rehearsalresume.Store); !ok {
			api.WriteProblem(w, api.ErrCapacity("suspend/resume receipt storage unavailable"))
			return false
		}
	}
	return true
}
func (s *server) scenarioSuspendResume(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run := r.PathValue("run_id")
	if !validDevWorkspaceID(run) || run == "" {
		api.WriteProblem(w, api.ErrValidation("run_id must be 32 lowercase hexadecimal characters"))
		return
	}
	e, err := rehearsal.SuspendResume(r.Context(), s.store, acct.ID, run, r.Method == http.MethodPost)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "suspend/resume run unavailable")
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Suspend/resume checkpoint unavailable", "The selected VMs and original collectors must be running with settled work"))
		default:
			api.WriteProblem(w, api.ErrCapacity("suspend/resume evidence unavailable"))
		}
		return
	}
	writeJSON(w, http.StatusOK, e)
}
