package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueTriageAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueTriageStore, string, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, "", false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, "", false
	}
	st, ok := s.store.(state.IssueTriageStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue triage persistence is unavailable"))
	}
	return st, app.ID, ok
}

func (s *server) compareIssueReleases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTriageAccess(w, r, acct)
	if !ok {
		return
	}
	env := r.URL.Query().Get("environment")
	if env == "" {
		env = "application"
	}
	if !issueEnvironmentPattern.MatchString(env) {
		api.WriteProblem(w, api.ErrValidation("invalid environment"))
		return
	}
	out, err := st.CompareIssueReleases(r.Context(), acct.ID, app, r.URL.Query().Get("baseline"), r.URL.Query().Get("candidate"), env, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) bulkActOnIssues(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTriageAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.BulkIssueActionRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	now := time.Now().UTC()
	in, err := api.NormalizeBulkIssueAction(in, now)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.BulkActOnIssues(r.Context(), app, acct.ID, in, now)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
