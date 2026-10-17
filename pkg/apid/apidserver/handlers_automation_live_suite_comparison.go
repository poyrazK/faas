package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	liveapi "github.com/onebox-faas/faas/pkg/apid/automationlive"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) compareAutomationLiveSuites(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.AutomationLiveSuiteComparisonStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Live suite comparison is unavailable."))
		return
	}
	liveapi.SuiteComparisonService{Store: store}.Compare(w, r, app.ID)
}
