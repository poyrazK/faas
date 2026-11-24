package billingruns

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetUsageHold(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.serveUsageHold(w, r, acct, false)
}

func (s *Service) RecheckUsageHold(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.serveUsageHold(w, r, acct, true)
}

func (s *Service) serveUsageHold(w http.ResponseWriter, r *http.Request, acct state.Account, recheck bool) {
	run, store, ok := s.completedDraftRun(w, r, acct)
	if !ok {
		return
	}
	consumer, err := uuid.Parse(r.PathValue("consumer_id"))
	if err != nil {
		httpkit.NotFound(w, "no such billing run usage hold")
		return
	}
	ctx := r.Context()
	item, err := store.GetBillingRunAppConsumerItem(ctx, acct.ID, run.ID, consumer.String())
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no such billing run usage hold")
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not load the billing run item"))
		return
	case item.UsageRecoveredAt == nil && (item.Outcome != state.BillingRunItemHeld || item.Completeness != billing.CompletenessGapsDetected):
		httpkit.NotFound(w, "no such billing run usage hold")
		return
	}
	if recheck && item.UsageRecoveredAt == nil && len(item.UsageGapHours) > 0 {
		var problem *api.Problem
		item, problem = s.recheckUsageHold(ctx, acct, store, run, item)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	s.writeUsageHold(w, ctx, acct.ID, item)
}

func (s *Service) writeUsageHold(w http.ResponseWriter, ctx context.Context, accountID string, item state.BillingRunItem) {
	hours, missing, problem := s.usageHoldHours(ctx, accountID, item)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.BillingRunUsageHold{
		Item: itemResponse(item), Hours: hours, MissingRequests: missing, HeldAt: item.UsageHeldAt,
	})
}

func (s *Service) usageHoldHours(ctx context.Context, accountID string, item state.BillingRunItem) ([]api.BillingRunUsageHoldHour, int64, *api.Problem) {
	hours := make([]api.BillingRunUsageHoldHour, 0, len(item.UsageGapHours))
	if len(item.UsageGapHours) == 0 {
		return hours, 0, nil
	}
	usage, ok := s.Host.Store().(state.ConsumerUsageStore)
	if !ok {
		return nil, 0, api.ErrInternal("the API consumer billing ledger is unavailable")
	}
	from := item.UsageGapHours[0].Hour
	until := item.UsageGapHours[len(item.UsageGapHours)-1].Hour.Add(time.Hour)
	buckets, err := usage.ListAPIConsumerUsage(ctx, accountID, item.AppID, item.SourceID, from, until)
	if err != nil {
		return nil, 0, api.ErrInternal("could not read the billing ledger for the held hours")
	}
	ledger := billing.APIConsumerLedgerRequestsByHour(buckets, from, until)
	var missing int64
	for _, gap := range item.UsageGapHours {
		current := ledger[gap.Hour.Unix()]
		hours = append(hours, api.BillingRunUsageHoldHour{
			Hour: gap.Hour.UTC(), LedgerRequestsAtHold: gap.LedgerRequests, TelemetryRequestsAtHold: gap.TelemetryRequests,
			RequiredRequests: gap.RequiredRequests, LedgerRequests: current,
		})
		missing += max(0, gap.RequiredRequests-current)
	}
	return hours, missing, nil
}

func (s *Service) recheckUsageHold(ctx context.Context, acct state.Account, store state.BillingRunStore,
	run state.BillingRun, item state.BillingRunItem) (state.BillingRunItem, *api.Problem) {
	release, acquired, err := store.TryLockBillingRunUsageRecheck(ctx, run.ID, item.SourceID)
	if err != nil {
		return item, api.ErrInternal("could not lock the billing run usage hold")
	}
	if !acquired {
		return item, draftReviewConflict("another recheck is in progress for this customer")
	}
	defer release(ctx)
	item, err = store.GetBillingRunAppConsumerItem(ctx, acct.ID, run.ID, item.SourceID)
	if err != nil {
		return item, api.ErrInternal("could not reload the billing run item")
	}
	if item.UsageRecoveredAt != nil {
		return item, nil
	}
	report, err := s.Host.ConsumerCompleteness(ctx, acct, item.AppID, item.SourceID, run.PeriodStart, run.PeriodEnd)
	if err != nil {
		return item, api.ErrInternal("could not compare usage with request telemetry")
	}
	gaps := mergeUsageGapHours(item.UsageGapHours, report.GapHours)
	changed := !slices.Equal(gaps, item.UsageGapHours)
	item.UsageGapHours = gaps
	_, missing, problem := s.usageHoldHours(ctx, acct.ID, item)
	if problem != nil {
		return item, problem
	}
	if missing == 0 {
		statement, problem := s.Host.DraftConsumerStatement(ctx, acct, item.AppID, item.SourceID, run.PeriodStart, run.PeriodEnd)
		if problem != nil {
			return item, problem
		}
		if statement.Status != state.APIConsumerUsageStatementDraft {
			return item, draftReviewConflict("the customer's latest statement is already finalized")
		}
		recordStatement(&item, statement.ID, statement.Revision, statement.Currency, statement.AmountMillicents)
		now := s.now()
		item.UsageRecoveredAt = &now
		item.Outcome, item.Reason, item.Completeness = state.BillingRunItemDrafted, "", report.Status
		if report.Status == billing.CompletenessGapsDetected {
			// The ledger caught up between the telemetry comparison and the
			// ledger read. The saved hours are covered. Other hours may be sampled.
			item.Completeness = billing.CompletenessPartial
		}
	} else if !changed {
		return item, nil
	}
	err = store.SaveBillingRunUsageRecheck(ctx, acct.ID, item)
	if err != nil && !errors.Is(err, state.ErrConflict) {
		return item, api.ErrInternal("could not save the billing run usage recheck")
	}
	saved, readErr := store.GetBillingRunAppConsumerItem(ctx, acct.ID, run.ID, item.SourceID)
	if readErr != nil {
		return item, api.ErrInternal("could not reload the billing run item")
	}
	if errors.Is(err, state.ErrConflict) && saved.UsageRecoveredAt == nil {
		return saved, draftReviewConflict("the usage hold changed during the recheck; try again")
	}
	if err == nil && saved.UsageRecoveredAt != nil {
		within48Hours := saved.UsageHeldAt != nil && !saved.UsageRecoveredAt.Before(*saved.UsageHeldAt) &&
			!saved.UsageRecoveredAt.After(saved.UsageHeldAt.Add(48*time.Hour))
		s.Host.Audit(ctx, "billing_run.usage_hold.recovered", acct.ID, map[string]any{
			"run_id": run.ID, "consumer_id": saved.SourceID, "statement_id": saved.StatementID,
			"amount_millicents": saved.AmountMillicents, "currency": saved.Currency,
			"held_at": saved.UsageHeldAt, "recovered_at": saved.UsageRecoveredAt,
			"within_48_hours": within48Hours,
		})
	}
	return saved, nil
}

func mergeUsageGapHours(saved, observed []state.BillingRunUsageGapHour) []state.BillingRunUsageGapHour {
	if len(observed) == 0 {
		return saved
	}
	byHour := make(map[int64]state.BillingRunUsageGapHour, len(saved)+len(observed))
	for _, gap := range saved {
		byHour[gap.Hour.Unix()] = gap
	}
	for _, gap := range observed {
		hour := gap.Hour.Unix()
		if original, ok := byHour[hour]; ok {
			original.RequiredRequests = max(original.RequiredRequests, gap.RequiredRequests)
			gap = original
		}
		byHour[hour] = gap
	}
	merged := make([]state.BillingRunUsageGapHour, 0, len(byHour))
	for _, gap := range byHour {
		merged = append(merged, gap)
	}
	slices.SortFunc(merged, func(a, b state.BillingRunUsageGapHour) int { return a.Hour.Compare(b.Hour) })
	return merged
}
