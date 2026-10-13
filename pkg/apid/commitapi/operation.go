package commitapi

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

// Retained Commit facts remain readable after the managed owner prunes results.
func GetCommitOperation(backend state.Store, w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := backend.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid operation ID"))
		return
	}
	operation, err := store.CommitOperationByID(r.Context(), acct.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "operation_not_found", "Operation not found", "operation is unavailable"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read operation"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, operation)
}
