package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Completeness windows (ADR-941): only hours telemetry has had time to
// publish, and only hours request telemetry still retains, are checked.
const (
	completenessSettleDelay   = 10 * time.Minute
	completenessTelemetryDays = 14
)

// completenessWindow clamps [since, until) to whole UTC hours that have
// settled and are still retained. ok is false when nothing can be checked.
func completenessWindow(since, until, now time.Time) (time.Time, time.Time, bool) {
	from := since.UTC().Truncate(time.Hour)
	if since.UTC().After(from) {
		from = from.Add(time.Hour)
	}
	if retained := now.UTC().AddDate(0, 0, -completenessTelemetryDays).Truncate(time.Hour).Add(time.Hour); from.Before(retained) {
		from = retained
	}
	end := until.UTC().Truncate(time.Hour)
	if settled := now.UTC().Add(-completenessSettleDelay).Truncate(time.Hour); end.After(settled) {
		end = settled
	}
	return from, end, end.After(from)
}

func parseCompletenessRange(r *http.Request) (time.Time, time.Time, *api.Problem) {
	invalid := func(detail string) (time.Time, time.Time, *api.Problem) {
		return time.Time{}, time.Time{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid completeness window", detail)
	}
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	if err != nil {
		return invalid("since must be RFC3339")
	}
	until, err := time.Parse(time.RFC3339, r.URL.Query().Get("until"))
	if err != nil {
		return invalid("until must be RFC3339")
	}
	if !until.After(since) || until.Sub(since) > time.Duration(usageMaxWindowDays)*24*time.Hour {
		return invalid("until must be after since, within 90 days")
	}
	return since, until, nil
}

// getAPIConsumerUsageCompleteness compares the consumer's billing ledger
// with request telemetry so missing usage is visible before invoicing.
func (s *server) getAPIConsumerUsageCompleteness(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	since, until, problem := parseCompletenessRange(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	from, end, checkable := completenessWindow(since, until, time.Now())
	result := billing.APIConsumerUsageCompleteness{Status: billing.CompletenessUnverifiable, CheckedFrom: from, CheckedUntil: from}
	if checkable {
		if result, err = s.consumerUsageCompleteness(r, acct.ID, app.ID, consumer.ID, from, end); err != nil {
			api.WriteProblem(w, api.ErrInternal("could not compare consumer usage with request telemetry"))
			return
		}
	}
	writeJSON(w, http.StatusOK, api.APIConsumerUsageCompletenessResponse{ConsumerID: consumer.ID, Status: result.Status,
		CheckedFrom: result.CheckedFrom, CheckedUntil: result.CheckedUntil,
		LedgerRequests: result.LedgerRequests, TelemetryRequests: result.TelemetryRequests,
		ConfirmedRequests: result.ConfirmedRequests, MissingRequests: result.MissingRequests,
		HoursChecked: result.HoursChecked, HoursWithoutTelemetry: result.HoursWithoutTelemetry})
}

func (s *server) consumerUsageCompleteness(r *http.Request, accountID, appID, consumerID string, from, until time.Time) (billing.APIConsumerUsageCompleteness, error) {
	usageStore, okUsage := s.store.(state.ConsumerUsageStore)
	telemetryStore, okTelemetry := s.store.(state.ConsumerUsageTelemetryStore)
	if !okUsage || !okTelemetry {
		return billing.APIConsumerUsageCompleteness{}, fmt.Errorf("consumer usage completeness: store lacks usage or telemetry")
	}
	usage, err := usageStore.ListAPIConsumerUsage(r.Context(), accountID, appID, consumerID, from, until)
	if err != nil {
		return billing.APIConsumerUsageCompleteness{}, fmt.Errorf("list consumer usage: %w", err)
	}
	hours, err := telemetryStore.ListAPIConsumerTelemetryHours(r.Context(), accountID, appID, consumerID, from, until)
	if err != nil {
		return billing.APIConsumerUsageCompleteness{}, fmt.Errorf("list consumer telemetry: %w", err)
	}
	return billing.CheckAPIConsumerUsageCompleteness(usage, hours, from, until), nil
}
