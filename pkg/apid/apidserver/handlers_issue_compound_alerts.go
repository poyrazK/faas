package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueCompoundAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueCompoundAlertStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, app, false
	}
	st, ok := s.store.(state.IssueCompoundAlertStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue compound alerts are unavailable"))
	}
	return st, app, ok
}

func (s *server) getIssueCompoundAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueCompoundAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.GetIssueCompoundAlertPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) setIssueCompoundAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueCompoundAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.UpdateIssueCompoundAlertPolicyRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if in.Rules == nil {
		api.WriteProblem(w, api.ErrValidation("rules must be an array; use [] to remove all rules"))
		return
	}
	if _, err := api.NormalizeIssueCompoundAlertRules(in.Rules); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.SetIssueCompoundAlertPolicy(r.Context(), app.ID, app.AccountID, in)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) previewIssueCompoundAlert(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueCompoundAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.PreviewIssueCompoundAlertRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	out, err := st.PreviewIssueCompoundAlert(r.Context(), app.ID, app.AccountID, in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
