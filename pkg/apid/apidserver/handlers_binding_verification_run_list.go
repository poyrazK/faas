package apidserver

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
	"strconv"
)

func verificationRunListOptions(r *http.Request, graph bool) (api.BindingVerificationRunListOptions, error) {
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		return api.BindingVerificationRunListOptions{}, parseErr
	}
	o := api.BindingVerificationRunListOptions{Cursor: q.Get("cursor"), DeploymentID: q.Get("deployment_id"), Scope: q.Get("scope"), Status: q.Get("status")}
	for key, values := range q {
		if len(values) != 1 {
			return o, fmt.Errorf("duplicate query parameter")
		}
		switch key {
		case "limit", "cursor", "deployment_id", "status":
		case "scope", "include_members":
			if graph {
				return o, fmt.Errorf("app-only filter")
			}
		default:
			return o, fmt.Errorf("unknown list filter")
		}
	}
	if value, ok := q["limit"]; ok {
		n, e := strconv.Atoi(value[0])
		if e != nil || n < 1 || n > api.BindingVerificationRunListMaxLimit {
			return o, fmt.Errorf("invalid page size")
		}
		o.Limit = n
	}
	if value, ok := q["include_members"]; ok {
		if value[0] != "true" && value[0] != "false" {
			return o, fmt.Errorf("invalid member filter")
		}
		o.IncludeMembers = value[0] == "true"
	}
	return o, nil
}
func (s *server) listBindingVerificationRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.VerificationRunListStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("verification listings unavailable"))
		return
	}
	o, e := verificationRunListOptions(r, false)
	if e != nil {
		api.WriteProblem(w, api.ErrValidation("invalid verification list options"))
		return
	}
	out, e := store.ListBindingVerificationRuns(r.Context(), acct.ID, app.ID, o)
	if problem := bindings.VerificationRunProblem(e); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func (s *server) listProjectBindingVerificationRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.VerificationRunListStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("verification listings unavailable"))
		return
	}
	o, e := verificationRunListOptions(r, true)
	if e != nil {
		api.WriteProblem(w, api.ErrValidation("invalid verification list options"))
		return
	}
	out, e := store.ListProjectBindingVerificationRuns(r.Context(), acct.ID, project.ID, r.PathValue("environment"), o)
	if problem := bindings.VerificationRunProblem(e); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
