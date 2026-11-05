package logsearch

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// ListSaved serves GET /v1/apps/{slug}/log-searches.
func (s *Service) ListSaved(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.savedApp(w, r, acct)
	if !ok {
		return
	}
	searches, err := s.Host.SavedSearches().ListLogSavedSearches(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.Host.Logger().WarnContext(r.Context(), "list saved log searches failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list saved log searches"))
		return
	}
	resp := api.LogSavedSearchesResponse{Searches: make([]api.LogSavedSearch, 0, len(searches))}
	for _, search := range searches {
		resp.Searches = append(resp.Searches, savedSearchDTO(search))
	}
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

// PutSaved serves PUT /v1/apps/{slug}/log-searches/{name}: create the search
// or replace its query. The query is validated and stored canonically.
func (s *Service) PutSaved(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.savedApp(w, r, acct)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !state.ValidLogSavedSearchName(name) {
		api.WriteProblem(w, invalid("name must be 1..63 lowercase letters, digits and dashes, starting with a letter or digit"))
		return
	}
	var req api.LogSavedSearchRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, invalid("body must be {\"query\": \"...\"}"))
		return
	}
	query, _, err := state.CanonicalLogQuery(req.Query)
	if err != nil {
		api.WriteProblem(w, invalid(err.Error()))
		return
	}
	saved, err := s.Host.SavedSearches().PutLogSavedSearch(r.Context(), state.LogSavedSearch{
		AccountID: acct.ID, AppID: app.ID, Name: name, Query: query,
	}, api.LogSavedSearchesPerApp)
	var quota *state.LogSavedSearchQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrLogSavedSearchQuota(quota.Limit, quota.Observed))
	case err != nil:
		s.Host.Logger().WarnContext(r.Context(), "save log search failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not save the log search"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, savedSearchDTO(saved))
	}
}

// DeleteSaved serves DELETE /v1/apps/{slug}/log-searches/{name}.
func (s *Service) DeleteSaved(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.savedApp(w, r, acct)
	if !ok {
		return
	}
	err := s.Host.SavedSearches().DeleteLogSavedSearch(r.Context(), acct.ID, app.ID, r.PathValue("name"))
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrLogSavedSearchNotFound(r.PathValue("name")))
	case err != nil:
		s.Host.Logger().WarnContext(r.Context(), "delete saved log search failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not delete the saved log search"))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Service) savedApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	if !s.Enabled {
		api.WriteProblem(w, api.ErrLogSearchUnavailable())
		return state.App{}, false
	}
	return s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
}

// withSaved resolves ?saved=NAME into the query: the saved filters apply
// unless the request sets the same parameter itself.
func (s *Service) withSaved(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) (url.Values, bool) {
	query := r.URL.Query()
	name := query.Get("saved")
	if name == "" {
		return query, true
	}
	query.Del("saved")
	saved, err := s.Host.SavedSearches().LogSavedSearchByName(r.Context(), acct.ID, app.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrLogSavedSearchNotFound(name))
		return nil, false
	}
	if err != nil {
		s.Host.Logger().WarnContext(r.Context(), "read saved log search failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not read the saved log search"))
		return nil, false
	}
	stored, _ := url.ParseQuery(saved.Query)
	for key, values := range stored {
		if query.Get(key) == "" {
			query[key] = values
		}
	}
	return query, true
}

func savedSearchDTO(search state.LogSavedSearch) api.LogSavedSearch {
	return api.LogSavedSearch{Name: search.Name, Query: search.Query, CreatedAt: search.CreatedAt, UpdatedAt: search.UpdatedAt}
}
