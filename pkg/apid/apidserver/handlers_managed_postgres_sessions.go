package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listManagedPostgresSessions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	report, err := s.managedPostgres.ListSessions(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}

func (s *server) cancelManagedPostgresQuery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var req api.CancelManagedPostgresQueryRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil || !dbquerycontrol.ValidID(req.TargetID) {
		api.WriteProblem(w, api.ErrValidation("target_id must be a nonzero UUID from the sessions view"))
		return
	}
	report, err := s.managedPostgres.CancelQuery(r.Context(), acct.ID, r.PathValue("id"), req.TargetID)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}
