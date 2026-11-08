package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapiruntime"
	"github.com/onebox-faas/faas/pkg/state"
)

// appDataAPIRuntime serves the Data API runtime to the app's owner
// (ADR-20261031095954); the route prefix carries the data-api capability.
func (s *server) appDataAPIRuntime(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.loadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	dataapiruntime.Serve(w, r)
}

// appDataAPIStatus answers what the platform knows about the app's Data API
// without waking it (ADR-20261022121642 amendment 1): the runtime it serves,
// whether runtime reports can reach it, and the change hook delivery the
// runtime last reported.
func (s *server) appDataAPIStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	reports := api.DataAPIRuntimeReportsUnavailable
	if s.workloadVerifier != nil {
		reports = api.DataAPIRuntimeReportsAvailable
	}
	var hooks *api.DataAPIChangeHookStatus
	if s.dataAPIUsage != nil {
		var err error
		if hooks, err = s.dataAPIUsage.ChangeHooks(r.Context(), app.ID); err != nil {
			s.log.Warn("data api change hook report unavailable", "app_id", app.ID, "err", err)
		}
	}
	dataapiruntime.ServeStatus(w, reports, hooks)
}
