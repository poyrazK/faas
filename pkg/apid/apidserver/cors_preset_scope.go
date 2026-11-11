package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
	if !s.corsPresetAppAllowed(r, acct, row.AppID) {
		api.WriteProblem(w, errNoSuchCorsPreset())
		return state.CorsPreset{}, false
	}
	return row, true
}

// corsPresetAppAllowed reports whether the caller may act on a preset scoped
// to appID with this route's permission. An app-scoped preset belongs to its
// app: on the personal path its creator used to read, edit and delete a
// company app's presets after leaving the organization, because only the
// account was checked (ADR-091 amendment 2026-11-04, review round 10).
func (s *server) corsPresetAppAllowed(r *http.Request, acct state.Account, appID string) bool {
	if appID == "" {
		return true
	}
	if app, ok := corsPresetCompanyApp(r); ok && app.ID == appID {
		return true
	}
	return s.gateCorsPresetApp(r, acct, &appID) == nil
}

// visibleCorsPresets drops the presets of apps the caller may not read.
func (s *server) visibleCorsPresets(r *http.Request, acct state.Account, rows []state.CorsPreset) []state.CorsPreset {
	allowed := map[string]bool{}
	out := rows[:0]
	for _, row := range rows {
		ok, seen := allowed[row.AppID]
		if !seen {
			ok = s.corsPresetAppAllowed(r, acct, row.AppID)
			allowed[row.AppID] = ok
		}
		if ok {
			out = append(out, row)
		}
	}
	return out
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

// corsPresetInUseProblem refuses deleting a preset that edge rules use,
// naming them.
func (s *server) corsPresetInUseProblem(ctx context.Context, accountID, id string) *api.Problem {
	rules, err := s.store.ListEdgeRulesUsingCorsPreset(ctx, accountID, id)
	if err != nil {
		return api.ErrCapacity("could not read the edge rules that use the cors preset")
	}
	if len(rules) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "CORS preset in use",
		"edge rules "+strings.Join(ids, ", ")+" use this cors preset; change or delete them first")
}
