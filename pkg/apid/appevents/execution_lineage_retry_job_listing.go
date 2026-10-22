package appevents

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListEventExecutionLineageRetryJobs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			writeLineageRetryJobListingError(w, state.ErrInvalidArgument)
			return
		}
	}
	out, err := store.ListEventExecutionLineageRetryJobs(ctx, acct.ID, app, r.PathValue("subscriptionID"), r.URL.Query().Get("status"), r.URL.Query().Get("after"), limit, api.EventExecutionLineageRetryJobAttentionOptions{Attention: r.URL.Query().Get("attention")})
	if !writeLineageRetryJobListingError(w, err) {
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func writeLineageRetryJobListingError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid subscription, job status/attention filter or pagination"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no accessible event subscription")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Lineage job listing timed out", "repeat the listing with the same cursor, status and attention filters"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event execution lineage job listing"))
	default:
		return false
	}
	return true
}
