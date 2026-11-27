package apidserver

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedPostgresRestoreDrill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	start := r.Method == http.MethodPost
	key, result := r.PathValue("drill_id"), http.StatusOK
	if start {
		var body api.RequestManagedPostgresRestoreDrill
		if err := decodeJSONSized(r, &body, 4<<10); err != nil {
			api.WriteProblem(w, api.ErrValidation("request_id must be a UUID"))
			return
		}
		key, result = body.RequestID, http.StatusAccepted
	}
	id, err := uuid.Parse(key)
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("a nonzero UUID is required"))
		return
	}
	out, err := s.managedPostgres.RestoreDrill(r.Context(), acct.ID, r.PathValue("id"), id.String(), start)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result, out)
}
