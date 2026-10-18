package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) sealScenarioTestActions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if runID == "" || !validDevWorkspaceID(runID) {
		s.notFound(w, "no such capture run")
		return
	}
	store, ok := s.store.(state.ScenarioActionSnapshotStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("seal scenario actions"))
		return
	}
	snapshot, err := store.SealScenarioTestActions(r.Context(), acct.ID, runID)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such capture run")
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Capture run unavailable", "sealing requires live capture workloads with unchanged membership, or a retained sealed snapshot"))
		default:
			api.WriteProblem(w, api.ErrCapacity("seal scenario actions"))
		}
		return
	}
	status := http.StatusOK
	if snapshot.Status == "pending" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, snapshot)
}
