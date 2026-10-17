package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueSourceMapAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueSourceMapStore, string, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, "", false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, "", false
	}
	if _, err := uuid.Parse(r.PathValue("deployment_id")); err != nil {
		writeIssueError(w, state.ErrNotFound)
		return nil, "", false
	}
	st, ok := s.store.(state.IssueSourceMapStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("source-map persistence is unavailable"))
	}
	return st, app.ID, ok
}
func (s *server) publishIssueSourceMaps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSourceMapAccess(w, r, acct)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueSourceMapsUploadMaxBytes)
	var in api.PublishIssueSourceMapsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid or oversized source-map manifest"))
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.ErrValidation("expected one manifest object"))
		return
	}
	if _, err := issues.CompileSourceMaps(in); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.PublishIssueSourceMaps(r.Context(), acct.ID, app, r.PathValue("deployment_id"), acct.ID, in)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) getIssueSourceMaps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSourceMapAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.GetIssueSourceMaps(r.Context(), acct.ID, app, r.PathValue("deployment_id"))
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) retireIssueSourceMaps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSourceMapAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.RetireIssueSourceMaps(r.Context(), acct.ID, app, r.PathValue("deployment_id"), acct.ID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
