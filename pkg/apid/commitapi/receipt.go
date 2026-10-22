package commitapi

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func GetCommitReceipt(backend state.Store, w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := backend.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	for _, id := range []string{r.PathValue("source"), r.PathValue("event")} {
		if _, err := uuid.Parse(id); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid commit identity"))
			return
		}
	}
	receipt, err := store.CommitReceiptByEvent(r.Context(), acct.ID, r.PathValue("source"), r.PathValue("event"))
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "commit_event_not_found", "Event not found", "event has not been accepted"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read commit receipt"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, receipt)
}
