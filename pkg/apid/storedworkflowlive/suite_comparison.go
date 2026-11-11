package storedworkflowlive

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

type SuiteComparisonService struct {
	Store state.StoredWorkflowLiveSuiteComparisonStore
}

func (s SuiteComparisonService) Compare(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := s.Store.CompareStoredWorkflowLiveSuites(r.Context(), appID, r.PathValue("name"), r.PathValue("suite_id"), r.PathValue("baseline_id"))
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
