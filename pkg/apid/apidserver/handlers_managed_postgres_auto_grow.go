package apidserver

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) setManagedPostgresAutoGrow(w http.ResponseWriter, r *http.Request, account state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	// Require the field to be present. A missing ceiling must not switch
	// off an existing policy.
	var request struct {
		MaxBytes *int64 `json:"max_bytes"`
	}
	if err := decodeJSON(r, &request); err != nil || request.MaxBytes == nil {
		managedPostgresProblem(w, fmt.Errorf("%w: max_bytes is required and must be an integer", managedpostgres.ErrInvalid))
		return
	}
	result, err := s.managedPostgres.SetAutoGrow(r.Context(), account.ID, r.PathValue("id"), *request.MaxBytes)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
