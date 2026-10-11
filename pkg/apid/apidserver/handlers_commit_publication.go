// adr: 935
package apidserver

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) acceptCommitPublication(w http.ResponseWriter, r *http.Request, acct state.Account, source string, event commitwork.Event) {
	publisher, ok := s.store.(state.CommitPublicationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit publication store unavailable"))
		return
	}
	receipt, err := publisher.AcceptCommitPublication(r.Context(), acct.ID, source, event)
	if appevents.WriteEventStorageCapacity(w, err) {
		return
	}
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "commit_identity_conflict", "Event identity conflict", "event ID was already accepted with different type, schema version or data"))
	case errors.Is(err, state.ErrEventSchemaRequired), errors.Is(err, state.ErrEventSchemaUnknown), errors.Is(err, state.ErrEventDataInvalid):
		api.WriteProblem(w, api.ErrValidation("event does not satisfy the publisher's registered schema contract"))
	case errors.Is(err, state.ErrInvalidArgument), errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrValidation("publication source is paused or unavailable, or publication exceeds its contract"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("publication acceptance could not be confirmed; retry the same source, event ID and content"))
	default:
		_ = s.notif.Notify(r.Context(), db.NotifyEventPublished, "1")
		w.Header().Set("Location", receipt.Publication.ReceiptURL)
		writeJSON(w, http.StatusAccepted, receipt)
	}
}
