package envbatch

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// ConfigRollbackReceipt reads a protected value-free reviewed rollback receipt.
func ConfigRollbackReceipt(store state.ConfigRollbackReceiptReader, resolve func(http.ResponseWriter, *http.Request, state.Account) (state.App, string, bool)) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		app, scope, ok := resolve(w, r, account)
		if !ok {
			return
		}
		if store == nil {
			api.WriteProblem(w, api.ErrCapacity("rollback recovery is unavailable"))
			return
		}
		id, err := uuid.Parse(r.PathValue("request_id"))
		proofs := r.Header.Values(api.EnvBatchReceiptProofHeader)
		if err != nil || id == uuid.Nil || len(proofs) != 1 || !api.ValidEnvBatchReceiptProof(proofs[0]) {
			api.WriteProblem(w, api.ErrValidation("request_id and a single 256-bit receipt proof are required"))
			return
		}
		out, err := store.ConfigRollbackReceipt(r.Context(), account.ID, app.ID, scope, id.String(), proofs[0])
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Receipt not found", "No retained rollback receipt for this target"))
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("rollback recovery is unavailable"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
