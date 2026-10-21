package appevents

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListEventExecutionLineageRetryJobControls(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	expected, e := time.Parse(time.RFC3339Nano, r.URL.Query().Get("expected_created_at"))
	after, a := strconv.ParseInt(defaultLineageControlQuery(r, "after", "0"), 10, 64)
	limit, l := strconv.Atoi(defaultLineageControlQuery(r, "limit", "0"))
	if e != nil || a != nil || l != nil || expected.IsZero() {
		writeLineageCaseError(w, state.ErrInvalidArgument)
		return
	}
	filter := api.EventExecutionLineageRetryJobControlFilters{Action: r.URL.Query().Get("action"), ActorID: r.URL.Query().Get("actor_id"), CredentialType: r.URL.Query().Get("credential_type")}
	out, err := store.ListEventExecutionLineageRetryJobControls(ctx, acct.ID, app, r.PathValue("subscriptionID"), r.PathValue("requestID"), expected, after, limit, filter)
	if !writeLineageControlHistoryError(w, err) {
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func defaultLineageControlQuery(r *http.Request, name, value string) string {
	if raw := r.URL.Query().Get(name); raw != "" {
		return raw
	}
	return value
}

func writeLineageControlHistoryError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_retry_job_changed", "Lineage retry job changed", "refresh the job and use its original creation timestamp"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Control history timed out", "repeat the read with the same creation guard, sequence cursor and filters"))
	default:
		return writeLineageCaseError(w, err)
	}
	return true
}
