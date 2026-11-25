package apidserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresQueryInsights(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	since := time.Hour
	if raw := r.URL.Query().Get("since_seconds"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || n > int64(dbqueryinsights.Retention/time.Second) {
			api.WriteProblem(w, api.ErrValidation("since_seconds must be from 1 to 86400"))
			return
		}
		since = time.Duration(n) * time.Second
	}
	report, err := s.managedPostgres.QueryInsights(r.Context(), acct.ID, r.PathValue("id"), since)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}

func (s *server) setManagedPostgresQueryInsights(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSONSized(r, &request, 4<<10); err != nil || request.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("enabled is required"))
		return
	}
	report, err := s.managedPostgres.SetQueryInsights(r.Context(), acct.ID, r.PathValue("id"), *request.Enabled)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}
