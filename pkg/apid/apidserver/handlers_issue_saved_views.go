package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueSavedViewsAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueSavedViewStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, app, false
	}
	st, ok := s.store.(state.IssueSavedViewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("saved issue views are unavailable"))
	}
	return st, app, ok
}

func (s *server) listIssueSavedViews(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSavedViewsAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.ListIssueSavedViews(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getIssueSavedView(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSavedViewsAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.GetIssueSavedView(r.Context(), app.ID, app.AccountID, r.PathValue("view_key"))
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) putIssueSavedView(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSavedViewsAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.PutIssueSavedViewRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	in, err := api.NormalizeIssueSavedView(in)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.PutIssueSavedView(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("view_key"), in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteIssueSavedView(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSavedViewsAccess(w, r, acct)
	if !ok {
		return
	}
	if err := st.DeleteIssueSavedView(r.Context(), app.ID, app.AccountID, r.PathValue("view_key")); err != nil {
		writeIssueError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
