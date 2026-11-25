package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appnames"
	"github.com/onebox-faas/faas/pkg/state"
)

// blockedAppNameProblem refuses a valid slug as a new app name when
// app_name_blocklist matches it (appnames.Refusal). Only paths that allocate a
// name call it: POST /v1/apps, the dashboard wizard and rename. Internal
// creates (dev sessions, sandbox templates, cache and pipeline apps) do not.
func (s *server) blockedAppNameProblem(accountID, slug string) *api.Problem {
	return appnames.Refusal(s.log, s.runtimeConfig.appNameBlocklist(), accountID, slug)
}

// appNameReport is GET /v1/admin/app-names/report (appnames.Reporter).
func (s *server) appNameReport(w http.ResponseWriter, r *http.Request, _ state.Account) {
	appnames.Reporter{
		Blocklist:       s.runtimeConfig.appNameBlocklist,
		ListApps:        s.store.ListAllApps,
		ListDeletedApps: s.store.ListDeletedApps,
		Log:             s.log,
	}.ServeHTTP(w, r)
}
