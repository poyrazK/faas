package apidserver

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresTableHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["sort"]) > 1 || len(query["limit"]) > 1 {
		api.WriteProblem(w, api.ErrValidation("invalid table health query parameters"))
		return
	}
	options := dbguest.TableHealthOptions{Sort: "size", Limit: dbguest.TableHealthDefaultLimit}
	if query.Has("sort") {
		options.Sort = query.Get("sort")
	}
	if query.Has("limit") {
		options.Limit, _ = strconv.Atoi(query.Get("limit"))
	}
	if err := options.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	report, err := s.managedPostgres.TableHealth(r.Context(), acct.ID, r.PathValue("id"), options)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}
