package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *server) issueTrackerLinksAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueTrackerLinkStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, app, false
	}
	st, ok := s.store.(state.IssueTrackerLinkStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue tracker links are unavailable"))
	}
	return st, app, ok
}
func (s *server) listIssueTrackerLinks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return
	}
	cur, err := state.DecodeIssueCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid tracker-link cursor"))
		return
	}
	out, err := st.ListIssueTrackerLinks(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), cur)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) putIssueTrackerLink(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.PutIssueTrackerLinkRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if _, err := api.NormalizeIssueTrackerLink(in); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.PutIssueTrackerLink(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), acct.ID, in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) deleteIssueTrackerLink(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return
	}
	if err := st.DeleteIssueTrackerLink(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), r.PathValue("link_id"), acct.ID, time.Now().UTC()); err != nil {
		writeIssueError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
