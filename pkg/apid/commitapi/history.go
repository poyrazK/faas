package commitapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func ListCommitPublications(backend state.Store, w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := backend.(state.CommitPublicationHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit history unavailable"))
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > api.CommitPublicationHistoryPageMax {
			api.WriteProblem(w, api.ErrValidation("invalid history limit"))
			return
		}
	}
	out, err := store.ListCommitPublications(r.Context(), acct.ID, r.PathValue("source"), r.URL.Query().Get("before"), limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("history requires an event source and valid pagination"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("read commit history"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
