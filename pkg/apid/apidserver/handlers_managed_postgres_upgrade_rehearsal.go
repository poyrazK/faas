package apidserver

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedPostgresUpgradeRehearsal(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	start := r.Method == http.MethodPost
	key, result := r.PathValue("rehearsal_id"), http.StatusOK
	var body api.RequestManagedPostgresUpgradeRehearsal
	if start {
		if err := decodeJSONSized(r, &body, 4<<10); err != nil || body.PostgresMajor <= 0 {
			api.WriteProblem(w, api.ErrValidation("request_id and a target postgres_major are required"))
			return
		}
		key, result = body.RequestID, http.StatusAccepted
	}
	id, err := uuid.Parse(key)
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("a nonzero UUID is required"))
		return
	}
	out, err := s.managedPostgres.UpgradeRehearsal(r.Context(), acct.ID, r.PathValue("id"), id.String(), body.PostgresMajor, start)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result, out)
}
