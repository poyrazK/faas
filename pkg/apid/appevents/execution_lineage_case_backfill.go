package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func writeLineageCaseBackfillError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_case_backfill_changed", "Diagnostic scan changed", "read the latest scan and pass its request UUID as expected_previous_request_id; an active scan cannot be replaced"))
		return true
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no subscription or existing execution coverage for diagnostic backfill")
		return true
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Diagnostic scan request timed out", "repeat the same request UUID and previous-request guard"))
		return true
	default:
		return writeLineageCaseError(w, err)
	}
}
func (s *Service) EventExecutionLineageCaseBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventExecutionLineageCaseBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("lineage diagnostic scans unavailable"))
		return
	}
	var out api.EventExecutionLineageCaseBackfill
	var err error
	if r.Method == http.MethodPost {
		var req api.RequestEventExecutionLineageCaseBackfillRequest
		if !httpkit.DecodeJSONLimit(w, r, &req, api.EventExecutionLineageRetryJobBodyMaxBytes) {
			return
		}
		out, err = store.RequestEventExecutionLineageCaseBackfill(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), req)
	} else {
		out, err = store.GetEventExecutionLineageCaseBackfill(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"))
	}
	if !writeLineageCaseBackfillError(w, err) {
		status := http.StatusOK
		if r.Method == http.MethodPost && (out.Status == "pending" || out.Status == "waiting_for_retry_jobs") {
			status = http.StatusAccepted
		}
		httpkit.WriteJSON(w, status, out)
	}
}
