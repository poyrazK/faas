package apidserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresErrorHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	query := r.URL.Query()
	seconds, limit := 3600, dbqueryerrors.DefaultResultLimit
	var err error
	if query.Has("since_seconds") {
		seconds, err = strconv.Atoi(query.Get("since_seconds"))
		if err != nil || seconds < 1 || seconds > int(dbqueryerrors.Retention/time.Second) {
			api.WriteProblem(w, api.ErrValidation("since_seconds must be from 1 to 86400"))
			return
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > dbqueryerrors.MaxResultLimit {
			api.WriteProblem(w, api.ErrValidation("limit must be from 1 to 100"))
			return
		}
	}
	report, err := s.managedPostgres.ErrorHistory(r.Context(), acct.ID, r.PathValue("id"), time.Duration(seconds)*time.Second, query.Get("sqlstate"), limit)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}

func (s *server) setManagedPostgresErrorHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSONSized(r, &request, 1<<10); err != nil || request.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("enabled is required"))
		return
	}
	report, err := s.managedPostgres.SetErrorHistory(r.Context(), acct.ID, r.PathValue("id"), *request.Enabled)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.error_history.configured", &acct.ID, postgresaccess.AuditData(r.Context(), map[string]any{
		"database_id": report.DatabaseID, "enabled": report.Enabled,
	}))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}
