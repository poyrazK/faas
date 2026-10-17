package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	liveapi "github.com/onebox-faas/faas/pkg/apid/automationlive"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) liveSuiteService(w http.ResponseWriter, r *http.Request, account state.Account) (liveapi.SuiteService, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return liveapi.SuiteService{}, "", false
	}
	store, ok := s.store.(state.AutomationLiveSuiteStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Shared live suite history is unavailable."))
		return liveapi.SuiteService{}, "", false
	}
	return liveapi.SuiteService{Store: store}, app.ID, true
}

func (s *server) saveAutomationLiveSuite(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveSuiteService(w, r, account); ok {
		service.Save(w, r, appID)
	}
}

func (s *server) getAutomationLiveSuite(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveSuiteService(w, r, account); ok {
		service.Get(w, r, appID)
	}
}

func (s *server) listAutomationLiveSuites(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveSuiteService(w, r, account); ok {
		service.List(w, r, appID)
	}
}
