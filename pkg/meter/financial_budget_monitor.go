package meter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunFinancialBudgetsOnce evaluates supported policies independently of paid
// provider delivery. A failed cost read is retained as unavailable, not zero.
func (l *Loop) RunFinancialBudgetsOnce(ctx context.Context) error {
	monitor, ok := l.store.(state.FinancialBudgetMonitorStore)
	if !ok {
		return nil
	}
	policies, ok := l.store.(state.FinancialBudgetStore)
	if !ok {
		return nil
	}
	accounts, err := l.store.ListAllAccounts(ctx)
	if err != nil {
		return err
	}
	now := l.now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var failures []error
	for _, account := range accounts {
		rows, err := policies.ListFinancialBudgets(ctx, account.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		var active []state.FinancialBudget
		for _, p := range rows {
			if p.DeletedAt == nil && p.Spec.Enabled && financial.Monitored(p.Spec) {
				active = append(active, p)
			}
		}
		if len(active) == 0 {
			continue
		}
		report, readErr := billing.ReadFinancialCosts(ctx, l.store, account.ID, start, now)
		for _, p := range active {
			e := financial.BudgetEvaluation{PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), AsOf: start, EvaluatedAt: now, Reasons: []string{"cost_evaluation_unavailable"}}
			var evalErr error
			if readErr == nil {
				e, evalErr = billing.EvaluateFinancialBudget(report, p.Spec, now)
			}
			if evalErr != nil {
				e = financial.BudgetEvaluation{PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), AsOf: start, EvaluatedAt: now, Reasons: []string{"cost_evaluation_unavailable"}}
			}
			commitErr := monitor.RecordFinancialBudgetEvaluation(ctx, account.ID, p.ID, p.Revision, e)
			if !errors.Is(commitErr, state.ErrConflict) {
				failures = append(failures, commitErr)
			}
			failures = append(failures, evalErr)
		}
		failures = append(failures, readErr)
	}
	return errors.Join(failures...)
}

// RunFinancialBudgetDeliveriesOnce keeps provider latency off the sampling and
// policy-evaluation paths. The durable outbox survives a process or pass timeout.
// Channel deliveries (ADR-20261019193500) run beside the account email, each
// within its own pass bound; without an alert evaluator they stay pending.
func (l *Loop) RunFinancialBudgetDeliveriesOnce(ctx context.Context) error {
	channelErr := l.evaluator.DeliverFinancialBudgetChannelsOnce(ctx)
	return errors.Join(l.runFinancialBudgetEmailsOnce(ctx), channelErr)
}

func (l *Loop) runFinancialBudgetEmailsOnce(ctx context.Context) error {
	ctx, cancelPass := context.WithTimeout(ctx, api.FinancialBudgetAlertLease)
	defer cancelPass()
	var failures []error
	if store, ok := l.store.(state.FinancialBudgetMonitorStore); ok {
		failures = append(failures, l.sendFinancialBudgetEmails(ctx, store.ClaimFinancialBudgetAlert, store.SettleFinancialBudgetAlert))
	}
	// ADR-20261019193500: projected-overrun alerts share the pass bound.
	if store, ok := l.store.(state.FinancialBudgetProjectionStore); ok {
		failures = append(failures, l.sendFinancialBudgetEmails(ctx, store.ClaimFinancialBudgetProjection, store.SettleFinancialBudgetProjection))
	}
	return errors.Join(failures...)
}

func (l *Loop) sendFinancialBudgetEmails(ctx context.Context, claim func(context.Context, time.Time) (state.FinancialBudgetAlertDelivery, error), settle func(context.Context, string, string, time.Time, bool) error) error {
	var failures []error
	for range api.FinancialBudgetAlertBatch {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		a, err := claim(ctx, l.now().UTC())
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		account, err := l.store.AccountByID(ctx, a.AccountID)
		if err == nil {
			sendCtx, cancel := context.WithTimeout(ctx, api.FinancialBudgetAlertSendTimeout)
			_, noop := l.mailer.(noopDunningSender)
			if l.mailer == nil || noop {
				err = errors.New("financial budget mailer unavailable")
			} else {
				err = l.mailer.Send(sendCtx, financialBudgetAlertMail(a, account.Email))
			}
			cancel()
		}
		settleErr := settle(ctx, a.ID, a.LeaseToken, l.now().UTC(), err == nil)
		if err != nil {
			// Never persist provider response text, credentials or recipient data.
			failures = append(failures, errors.New("financial budget alert delivery failed"))
		}
		failures = append(failures, settleErr)
	}
	return errors.Join(failures...)
}

func financialBudgetAlertMail(a state.FinancialBudgetAlertDelivery, recipient string) mail.Message {
	coverage := "Supported usage coverage is complete."
	if !a.Evaluation.CoverageComplete {
		coverage = "Usage coverage is partial; additional costs may be missing."
	}
	amount := func(n int64) string { return fmt.Sprintf("%s %d.%05d", a.Spec.Currency, n/100000, n%100000) }
	effect := "This is a spending notification. Workloads continue running."
	if financial.PreviewStopping(a.Spec) {
		effect = "This policy stops matching previews at its recorded compute limit. Production can continue. Metering, drain and teardown delays can exceed the budget. Check budget runtime for actual stopping status; this email is not a teardown acknowledgement."
	}
	if a.Kind == financial.BudgetAlertProjection && a.Evaluation.ProjectedMillicents != nil {
		body := fmt.Sprintf("At the current rate, your budget %q is projected to reach %s by the end of %s (UTC), above its %s limit.\n\nKnown recorded usage so far: %s\nScope: %s %s\nBasis: %s\nEvidence through: %s\n\nThe projection extends this month's usage rate to the end of the month; it is an estimate, not an invoice, and it covers the selected usage meters only. %s\n\nAlert ID: %s\nPolicy revision: %d\n", a.Spec.Name, amount(*a.Evaluation.ProjectedMillicents), a.Evaluation.PeriodStart.Format("2006-01"), amount(a.ThresholdMillicents), amount(*a.Evaluation.KnownMillicents), a.Spec.Scope.Kind, a.Spec.Scope.ID, a.Spec.Basis, a.Evaluation.AsOf.Format(time.RFC3339), effect, a.ID, a.PolicyRevision)
		return mail.Message{To: []string{recipient}, Subject: "Gregale budget projected to exceed its limit", TextBody: body, MessageID: "financial-budget-" + a.ID}
	}
	body := fmt.Sprintf("Your budget %q reached its %s notification threshold.\n\nKnown recorded usage: %s\nUsage month: %s (UTC)\nScope: %s %s\nBasis: %s\nEvidence through: %s\n%s\n\n%s It covers the selected usage meters, not your total invoice.\n\nAlert ID: %s\nPolicy revision: %d\n", a.Spec.Name, amount(a.ThresholdMillicents), amount(*a.Evaluation.KnownMillicents), a.Evaluation.PeriodStart.Format("2006-01"), a.Spec.Scope.Kind, a.Spec.Scope.ID, a.Spec.Basis, a.Evaluation.AsOf.Format(time.RFC3339), coverage, effect, a.ID, a.PolicyRevision)
	return mail.Message{To: []string{recipient}, Subject: "Gregale budget spending alert", TextBody: body, MessageID: "financial-budget-" + a.ID}
}
