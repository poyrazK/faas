package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	liveapi "github.com/onebox-faas/faas/pkg/apid/storedworkflowlive"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) liveSuiteExecutionService(w http.ResponseWriter, r *http.Request, account state.Account) (liveapi.SuiteExecutionService, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return liveapi.SuiteExecutionService{}, "", false
	}
	store, ok := s.store.(state.StoredWorkflowLiveSuiteExecutionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Live suite execution is unavailable."))
		return liveapi.SuiteExecutionService{}, "", false
	}
	return liveapi.SuiteExecutionService{Store: store, RuntimeEnabled: s.workflowRuntimeEnabled}, app.ID, true
}

func (s *server) startStoredWorkflowLiveSuite(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveSuiteExecutionService(w, r, account); ok {
		service.Start(w, r, appID)
	}
}

func (s *server) advanceStoredWorkflowLiveSuite(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveSuiteExecutionService(w, r, account); ok {
		service.Advance(w, r, appID)
	}
}
