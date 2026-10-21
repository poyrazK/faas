package billingruns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// Item reasons recorded by the worker.
const (
	tenantSkipReason  = "billed on the platform tenant's statement: this consumer's usage this month is attributed to a platform tenant"
	overlapSkipReason = "another statement already handed off part of this usage to an invoice"
	zeroTotalReason   = "nothing to invoice: the statement total is zero"
	// creditCoveredReason: prepaid credit paid the whole statement (ADR-1306,
	// ADR-20261019100331).
	creditCoveredReason = "nothing to invoice: prepaid credit covers the statement"
)

// Start runs the worker until ctx ends: every BillingRunTickInterval it
// starts due scheduled runs and advances one run by one batch of items.
func (s *Service) Start(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.BillingRunStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.BillingRunTickInterval)
		defer ticker.Stop()
		for {
			if err := s.tick(ctx); err != nil && ctx.Err() == nil {
				log.Warn("billing runs failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// RunOnce is one worker tick.
func (s *Service) RunOnce(ctx context.Context) error {
	store, ok := s.Host.Store().(state.BillingRunStore)
	if !ok {
		return nil
	}
	if err := s.startDueSchedules(ctx, store); err != nil {
		return err
	}
	if err := s.advanceRun(ctx, store); err != nil {
		return err
	}
	return s.notifyRuns(ctx)
}

// notifyRuns emails the account about completed runs that need attention,
// and about every scheduled run (ADR-1504). A run is marked notified only
// after the mailer accepts the message; a failed send retries next tick.
func (s *Service) notifyRuns(ctx context.Context) error {
	store, ok := s.Host.Store().(state.BillingRunNotificationStore)
	if !ok {
		return nil
	}
	runs, err := store.ListBillingRunsToNotify(ctx, api.BillingRunBatchSize)
	if err != nil {
		return fmt.Errorf("list billing runs to notify: %w", err)
	}
	for _, run := range runs {
		attention := run.Counts[state.BillingRunItemHeld] + run.Counts[state.BillingRunItemFailed]
		if attention > 0 || run.Trigger == state.BillingRunTriggerSchedule {
			acct, err := s.Host.Store().AccountByID(ctx, run.AccountID)
			if err != nil {
				continue
			}
			subject, body := runNotification(run, attention)
			if err := s.Host.SendEmail(ctx, acct.Email, subject, body, "billing-run-"+run.ID); err != nil {
				continue
			}
		}
		if err := store.MarkBillingRunNotified(ctx, run.ID, s.now()); err != nil {
			return fmt.Errorf("mark billing run notified: %w", err)
		}
	}
	return nil
}

func runNotification(run state.BillingRun, attention int) (string, string) {
	month := run.PeriodStart.Format("2006-01")
	subject := fmt.Sprintf("Billing run for %s completed", month)
	if attention > 0 {
		subject = fmt.Sprintf("Billing run for %s needs attention: %d held, %d failed", month,
			run.Counts[state.BillingRunItemHeld], run.Counts[state.BillingRunItemFailed])
	}
	c := run.Counts
	body := fmt.Sprintf("Gregale finished billing %s (%s run %s).\n\n"+
		"  exported %d · finalized %d · drafted %d · up to date %d\n  held %d · skipped %d · failed %d\n\n",
		month, run.Trigger, run.ID, c[state.BillingRunItemExported], c[state.BillingRunItemFinalized], c[state.BillingRunItemDrafted],
		c[state.BillingRunItemUpToDate], c[state.BillingRunItemHeld], c[state.BillingRunItemSkipped], c[state.BillingRunItemFailed])
	if attention > 0 {
		body += fmt.Sprintf("See who needs attention: gregale account billing-run show %s --outcome held\n"+
			"and: gregale account billing-run show %s --outcome failed\n"+
			"Fix the cause, then start another run for %s; it bills only what is still open.\n", run.ID, run.ID, month)
	} else {
		body += fmt.Sprintf("Details: gregale account billing-run show %s\n", run.ID)
	}
	return subject, body
}

func (s *Service) startDueSchedules(ctx context.Context, store state.BillingRunStore) error {
	due, err := store.ListDueBillingRunSchedules(ctx, s.now(), api.BillingRunScheduleBatchSize)
	if err != nil {
		return fmt.Errorf("list due billing run schedules: %w", err)
	}
	for _, sched := range due {
		start := sched.NextPeriodStart
		next := sched
		next.NextPeriodStart = start.AddDate(0, 1, 0)
		next.NextRunAt = scheduleRunAt(next.NextPeriodStart, sched.DelayHours)
		run, err := store.StartScheduledBillingRun(ctx, sched, state.BillingRun{AccountID: sched.AccountID, PeriodStart: start,
			PeriodEnd: start.AddDate(0, 1, 0), BillingRunOptions: sched.BillingRunOptions, Trigger: state.BillingRunTriggerSchedule}, next)
		if errors.Is(err, state.ErrConflict) {
			continue // another run is active; a later tick starts this one
		}
		if err != nil {
			return fmt.Errorf("start scheduled billing run: %w", err)
		}
		s.Host.Audit(ctx, "billing_run.created", sched.AccountID, map[string]any{"run_id": run.ID,
			"month": start.Format("2006-01"), "trigger": run.Trigger, "export": run.Export})
	}
	return nil
}

func (s *Service) advanceRun(ctx context.Context, store state.BillingRunStore) error {
	owner := uuid.NewString()
	run, err := store.ClaimBillingRun(ctx, owner, s.now(), api.BillingRunLeaseTTL)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim billing run: %w", err)
	}
	if err := s.processBatch(ctx, store, run, owner); err != nil {
		if errors.Is(err, state.ErrBillingRunLeaseLost) {
			return nil
		}
		return err // the lease expires and a later tick resumes the run
	}
	done, err := store.ReleaseBillingRun(ctx, run.ID, owner, s.now())
	if errors.Is(err, state.ErrBillingRunLeaseLost) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release billing run: %w", err)
	}
	if done.Status == state.BillingRunCompleted {
		s.Host.Audit(ctx, "billing_run.completed", done.AccountID, map[string]any{"run_id": done.ID,
			"month": done.PeriodStart.Format("2006-01"), "counts": done.Counts})
	}
	return nil
}

func (s *Service) processBatch(ctx context.Context, store state.BillingRunStore, run state.BillingRun, owner string) error {
	if !run.Discovered() {
		if err := store.DiscoverBillingRunItems(ctx, run.ID, owner, tenantSkipReason, s.now()); err != nil {
			return fmt.Errorf("discover billing run items: %w", err)
		}
	}
	acct, err := s.Host.Store().AccountByID(ctx, run.AccountID)
	if err != nil {
		return fmt.Errorf("load billing run account: %w", err)
	}
	items, err := store.ListDueBillingRunItems(ctx, run.ID, owner, s.now(), api.BillingRunBatchSize)
	if err != nil {
		return fmt.Errorf("list billing run items: %w", err)
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Each customer can take several provider calls; keep the lease so
		// no other worker bills the same customers meanwhile.
		if err := store.RenewBillingRunLease(ctx, run.ID, owner, s.now().Add(api.BillingRunLeaseTTL)); err != nil {
			return fmt.Errorf("renew billing run lease: %w", err)
		}
		if err := store.UpdateBillingRunItem(ctx, owner, s.processItem(ctx, acct, run, item)); err != nil {
			return fmt.Errorf("record billing run item: %w", err)
		}
	}
	return nil
}

// processItem makes one attempt at a customer. A transient problem keeps
// the item pending with exponential backoff until BillingRunItemMaxAttempts.
func (s *Service) processItem(ctx context.Context, acct state.Account, run state.BillingRun, item state.BillingRunItem) state.BillingRunItem {
	item.Attempts++
	item.Reason = ""
	var transient *api.Problem
	switch item.SourceKind {
	case state.StatementSourceAppConsumer:
		transient = s.billConsumer(ctx, acct, run, &item)
	case state.StatementSourceProductControl:
		transient = s.billProductCustomer(ctx, acct, run, &item)
	default:
		transient = s.billTenant(ctx, acct, run, &item)
	}
	if transient == nil {
		return item
	}
	item.Outcome, item.Reason = state.BillingRunItemPending, "retrying: "+problemDetail(transient)
	if item.Attempts >= api.BillingRunItemMaxAttempts {
		item.Outcome, item.Reason = state.BillingRunItemFailed, problemDetail(transient)
	}
	item.NextAttemptAt = s.now().Add(api.BillingRunRetryBase << (item.Attempts - 1))
	return item
}

func problemDetail(p *api.Problem) string {
	detail := p.Title
	if p.Detail != "" {
		detail = p.Title + ": " + p.Detail
	}
	return safetext.Truncate(detail, 1000)
}

// settle records a deterministic problem as the item's outcome and passes
// a transient one (status 500 or above, or an export another attempt is
// still running) back for a retry.
func settle(item *state.BillingRunItem, outcome string, p *api.Problem) *api.Problem {
	if p.Status >= 500 || p.Code == api.CodeStatementInvoiceExportInProgress {
		return p
	}
	item.Outcome, item.Reason = outcome, problemDetail(p)
	return nil
}

func recordStatement(item *state.BillingRunItem, id string, revision int, currency string, amount int64) {
	item.StatementID, item.Revision, item.Currency, item.AmountMillicents = id, revision, currency, amount
}

// handoffBlocked applies ADR-238's overlap rules before anything is
// finalized or invoiced.
func (s *Service) handoffBlocked(ctx context.Context, acct state.Account, kind, statementID string) (bool, *api.Problem) {
	checker, ok := s.Host.Store().(state.StatementHandoffChecker)
	if !ok {
		return false, nil
	}
	blocked, err := checker.StatementHandoffBlocked(ctx, acct.ID, kind, statementID)
	if err != nil {
		return false, api.ErrInternal("could not check the statement handoff")
	}
	return blocked, nil
}

// finish sets the outcome of a customer whose statement needed no hold.
func finish(item *state.BillingRunItem, run state.BillingRun, newUsage bool) {
	switch {
	case item.ExportedCount > 0 && run.Export != state.BillingRunExportNone:
		item.Outcome = state.BillingRunItemExported
	case !newUsage:
		item.Outcome = state.BillingRunItemUpToDate
	case !run.Finalize:
		item.Outcome = state.BillingRunItemDrafted
	default:
		item.Outcome = state.BillingRunItemFinalized
		if run.Export != state.BillingRunExportNone && item.AmountMillicents == 0 {
			item.Reason = zeroTotalReason
		}
	}
}
