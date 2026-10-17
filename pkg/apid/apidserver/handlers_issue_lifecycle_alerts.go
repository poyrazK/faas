package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueLifecycleAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueLifecycleAlertStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, app, false
	}
	st, ok := s.store.(state.IssueLifecycleAlertStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue lifecycle alerts are unavailable"))
	}
	return st, app, ok
}

func (s *server) getIssueLifecycleAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueLifecycleAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.GetIssueLifecycleAlertPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) setIssueLifecycleAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueLifecycleAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.UpdateIssueLifecycleAlertPolicyRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if _, err := api.NormalizeIssueLifecycleAlertPolicy(in.Environments, in.CooldownSeconds); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.SetIssueLifecycleAlertPolicy(r.Context(), app.ID, app.AccountID, in)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) previewIssueLifecycleAlert(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueLifecycleAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.PreviewIssueLifecycleAlertRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	out, err := st.PreviewIssueLifecycleAlert(r.Context(), app.ID, app.AccountID, in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
