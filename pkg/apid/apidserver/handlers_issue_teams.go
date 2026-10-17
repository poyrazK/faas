package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueTeamsAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueTeamStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, app, false
	}
	st, ok := s.store.(state.IssueTeamStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue teams are unavailable"))
	}
	return st, app, ok
}

func (s *server) listIssueTeams(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTeamsAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.ListIssueTeams(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getIssueTeam(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTeamsAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.GetIssueTeam(r.Context(), app.ID, app.AccountID, r.PathValue("team_key"))
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) putIssueTeam(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTeamsAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.PutIssueTeamRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	in, err := api.NormalizeIssueTeam(in)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.PutIssueTeam(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("team_key"), in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteIssueTeam(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTeamsAccess(w, r, acct)
	if !ok {
		return
	}
	if err := st.DeleteIssueTeam(r.Context(), app.ID, app.AccountID, r.PathValue("team_key")); err != nil {
		writeIssueError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
