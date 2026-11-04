package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

// corsPresetCompanyApp is the company app a CORS preset request names
// (?app=<slug> on /v1/cors-presets, or a dashboard app page). Its
// organization's members manage that app's presets with their organization
// role on it, and see only the presets scoped to it: never the account-wide
// presets of the member whose account owns the app's resources (ADR-091
// amendment 2026-11-03, review round 9).
func corsPresetCompanyApp(r *http.Request) (state.App, bool) {
	grant, ok := appaccess.From(r)
	if !ok || grant.App.OrgID == "" {
		return state.App{}, false
	}
	return grant.App, true
}

// corsPresetInScope reports whether a request may manage the preset: any of
// the account's presets on the personal path, the named app's own presets
// on a company app's path.
func corsPresetInScope(r *http.Request, appID string) bool {
	app, ok := corsPresetCompanyApp(r)
	return !ok || appID == app.ID
}

func errNoSuchCorsPreset() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such cors preset", "no cors preset with that id is visible to this account")
}

func errNoSuchCorsPresetApp() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such app", "no app with that id is visible to this account")
}

// loadScopedCorsPreset loads the {id} preset when the request may manage it,
// and otherwise writes the 404 another account's preset gets.
func (s *server) loadScopedCorsPreset(w http.ResponseWriter, r *http.Request, acct state.Account) (state.CorsPreset, bool) {
	row, err := s.store.GetCorsPresetByID(r.Context(), acct.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, state.ErrNotFound) || err == nil && !corsPresetInScope(r, row.AppID):
		api.WriteProblem(w, errNoSuchCorsPreset())
		return state.CorsPreset{}, false
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not load cors preset"))
		return state.CorsPreset{}, false
	}
	return row, true
}

// scopeCorsPresetAppID points a company app's request at that app: a preset
// it creates or lists is scoped to the app, and naming another app is
// refused.
func scopeCorsPresetAppID(r *http.Request, appID *string) (*string, *api.Problem) {
	app, ok := corsPresetCompanyApp(r)
	if !ok {
		return appID, nil
	}
	if appID != nil && *appID != app.ID {
		return nil, errNoSuchCorsPresetApp()
	}
	id := app.ID
	return &id, nil
}
