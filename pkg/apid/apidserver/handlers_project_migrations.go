package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectmigration"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) projectMigrationBackend(w http.ResponseWriter, acct state.Account) (projectmigration.Backend, bool) {
	store, ok := s.store.(state.ProjectMigrationStore)
	reader, readOK := s.store.(projectmigration.Reader)
	if !ok || !readOK || hostHMACKey == nil || len(hostHMACKey()) == 0 {
		api.WriteProblem(w, api.ErrCapacity("saved project migrations are unavailable"))
		return projectmigration.Backend{}, false
	}
	return projectmigration.Backend{Store: store, Reader: reader, AccountID: acct.ID, DigestKey: hostHMACKey()}, true
}
func (s *server) createProjectMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.projectMigrationBackend(w, acct)
	if !ok {
		return
	}
	var in api.CreateProjectMigrationRequest
	if err := decodeJSONSized(r, &in, api.ProjectMigrationRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid project migration request"))
		return
	}
	out, err := s.startProjectMigration(r, acct, b, in)
	s.respondProjectMigration(w, out, err)
}
func (s *server) getProjectMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.projectMigrationBackend(w, acct)
	if !ok {
		return
	}
	out, err := b.Get(r.Context(), r.PathValue("migration"))
	s.respondProjectMigration(w, out, err)
}
func (s *server) listProjectMigrations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.projectMigrationBackend(w, acct)
	if !ok {
		return
	}
	out, err := b.List(r.Context(), r.URL.Query().Get("project"))
	s.respondProjectMigration(w, out, err)
}
func (s *server) resumeProjectMigration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.projectMigrationBackend(w, acct)
	if !ok {
		return
	}
	var in api.ResumeProjectMigrationRequest
	if err := decodeJSONSized(r, &in, api.ProjectMigrationRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid project migration resume request"))
		return
	}
	out, err := s.continueProjectMigration(r, acct, b, r.PathValue("migration"), in)
	s.respondProjectMigration(w, out, err)
}
func (s *server) respondProjectMigration(w http.ResponseWriter, out any, err error) {
	if err != nil {
		var p *api.Problem
		switch {
		case errors.As(err, &p):
		case errors.Is(err, state.ErrNotFound):
			p = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Migration unavailable", "No such migration for this account.")
		case errors.Is(err, state.ErrConflict):
			p = api.NewProblem(http.StatusConflict, api.CodeConflict, "Migration changed", "Reload the saved migration before retrying. Another apply may still be running, or this request ID has already been used. Up to 20 migrations are retained per account.")
		default:
			p = api.ErrCapacity("Migration progress could not refresh. Reload the saved migration before trying another deployment.")
		}
		api.WriteProblem(w, p)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
