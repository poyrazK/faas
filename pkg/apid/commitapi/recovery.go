package commitapi

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func LoadCommitRecoverySource(backend state.Store, w http.ResponseWriter, r *http.Request, acct state.Account) (state.CommitStore, string, bool) {
	store, ok := backend.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return nil, "", false
	}
	source := r.PathValue("source")
	if _, err := uuid.Parse(source); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid source ID"))
		return nil, "", false
	}
	_, err := store.CommitSourceByID(r.Context(), acct.ID, source)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
		return nil, "", false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read commit source"))
		return nil, "", false
	}
	return store, source, true
}
