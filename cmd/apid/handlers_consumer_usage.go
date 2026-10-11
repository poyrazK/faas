package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAPIConsumerUsage serves the first customer-monetization read surface.
// The minute ledger is deliberately separate from request_telemetry: debug
// telemetry can be sampled, rate-limited, and expired, while these counters
// are idempotent financial facts. The endpoint returns raw usage only; the
// companion /usage/quote endpoint applies the app's customer-defined prices.
func (s *server) getAPIConsumerUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	consumer, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || consumer.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	since, until, windowErr := parseUsageWindow(r, time.Now().UTC())
	if windowErr != nil {
		api.WriteProblem(w, windowErr)
		return
	}
	rows, ok := s.consumerUsageRows(w, r, acct.ID, app.ID, consumer.ID, since, until)
	if !ok {
		return
	}
	out := api.APIConsumerUsageResponse{
		ConsumerID: consumer.ID, PeriodStart: since.UTC(), PeriodEnd: until.UTC(),
		Buckets: make([]api.APIConsumerUsageBucketResponse, 0, len(rows)),
		AsOf:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	for _, row := range rows {
		out.RequestCount += row.RequestCount
		out.ErrorCount += row.ErrorCount
		out.BillableUnits += row.BillableUnits
		out.Buckets = append(out.Buckets, api.APIConsumerUsageBucketResponse{
			WindowStart: row.WindowStart.UTC(), RequestCount: row.RequestCount,
			ErrorCount: row.ErrorCount, BillableUnits: row.BillableUnits,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// consumerUsageRows reads the ledger for one consumer. The window is
// half-open on UTC day boundaries, so since=today is empty; the ledger
// answers that as not-found, which is an empty period here (like the app
// usage summary), not a 500.
func (s *server) consumerUsageRows(w http.ResponseWriter, r *http.Request, accountID, appID, consumerID string, since, until time.Time) ([]state.APIConsumerUsageBucket, bool) {
	usageStore, ok := s.store.(state.ConsumerUsageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer usage ledger is unavailable"))
		return nil, false
	}
	if !until.After(since) {
		return nil, true
	}
	rows, err := usageStore.ListAPIConsumerUsage(r.Context(), accountID, appID, consumerID, since, until)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list api consumer usage", "app_id", appID, "consumer_id", consumerID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage"))
		return nil, false
	}
	return rows, true
}
