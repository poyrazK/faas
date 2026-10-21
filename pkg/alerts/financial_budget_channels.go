package alerts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// DeliverFinancialBudgetChannelsOnce sends due budget threshold alerts to the
// notification channels bound to their budgets (ADR-20261019193500), at most
// FinancialBudgetAlertBatch per pass. Each delivery is claimed, sent and
// settled on its own, so one failing channel never delays another or the
// account email. A nil evaluator or sender leaves deliveries pending.
func (e *Evaluator) DeliverFinancialBudgetChannelsOnce(ctx context.Context) error {
	if e == nil || e.channels == nil {
		return nil
	}
	store, ok := e.store.(state.FinancialBudgetChannelStore)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.FinancialBudgetAlertLease)
	defer cancel()
	ids := e.unsealIdentities()
	var failures []error
	for range api.FinancialBudgetAlertBatch {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		d, err := store.ClaimFinancialBudgetAlertChannel(ctx, e.now().UTC())
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if errors.Is(err, state.ErrConflict) {
			// The policy or channel changed after the claim; the lease
			// lapses and the claim filter no longer selects it.
			continue
		}
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		outcome, errMsg, metric := state.FinancialBudgetChannelDelivered, "", ChannelOutcomeDelivered
		if target, ok := openTarget(ids, d.Channel); !ok {
			outcome, errMsg, metric = state.FinancialBudgetChannelUnavailable, "could not open the channel destination; recreate the channel", ChannelOutcomeUnsealErr
		} else {
			sendCtx, cancelSend := context.WithTimeout(ctx, api.FinancialBudgetAlertSendTimeout)
			err = e.channels.Send(sendCtx, target, FinancialBudgetChannelMessage(d.Alert, d.Channel.ID, e.dashboardBaseURL))
			cancelSend()
			if err != nil {
				outcome, errMsg, metric = state.FinancialBudgetChannelFailed, err.Error(), ChannelOutcomeFailed
			}
		}
		e.recordChannel(ctx, d.Channel, e.now(), errMsg, metric)
		failures = append(failures, store.SettleFinancialBudgetAlertChannel(ctx, d.Alert.ID, d.Channel.ID, d.LeaseToken, e.now().UTC(), outcome))
	}
	return errors.Join(failures...)
}

// FinancialBudgetChannelMessage renders one budget alert for a channel. The
// amounts are the alert's recorded evaluation, formatted from millicents.
// Every threshold of one budget and month shares a PagerDuty incident.
func FinancialBudgetChannelMessage(a state.FinancialBudgetAlertDelivery, channelID, dashboardBaseURL string) alertchannels.Message {
	known := int64(0)
	if a.Evaluation.KnownMillicents != nil {
		known = *a.Evaluation.KnownMillicents
	}
	scope := a.Spec.Scope.Kind
	if a.Spec.Scope.ID != "" {
		scope += " " + a.Spec.Scope.ID
	}
	effect := "Workloads continue running."
	if financial.PreviewStopping(a.Spec) {
		effect = "Matching previews stop at the budget limit; production continues."
	}
	coverage := ""
	if !a.Evaluation.CoverageComplete {
		coverage = " Usage coverage is partial, so more costs may be missing."
	}
	detail := fmt.Sprintf("%s, %s, %s", strings.Join(a.Spec.Meters, " + "), strings.ReplaceAll(a.Spec.Basis, "_", " "), scope)
	summary := fmt.Sprintf("Known usage this month is %s, past the %s threshold of a %s budget (%s).%s %s It covers the selected usage meters, not your total invoice.",
		budgetMoney(a.Spec.Currency, known), budgetMoney(a.Spec.Currency, a.ThresholdMillicents), budgetMoney(a.Spec.Currency, a.Spec.LimitMillicents), detail, coverage, effect)
	severity := "warning"
	if a.ThresholdMillicents >= a.Spec.LimitMillicents {
		severity = "error"
	}
	if a.Kind == financial.BudgetAlertProjection && a.Evaluation.ProjectedMillicents != nil {
		summary = fmt.Sprintf("At the current rate, usage this month is projected to reach %s, above the %s limit (%s). Known usage so far is %s. %s The projection is an estimate from this month's run rate, not an invoice.",
			budgetMoney(a.Spec.Currency, *a.Evaluation.ProjectedMillicents), budgetMoney(a.Spec.Currency, a.Spec.LimitMillicents), detail, budgetMoney(a.Spec.Currency, known), effect)
		severity = "warning"
	}
	month := a.Evaluation.PeriodStart.UTC().Format("2006-01")
	msg := alertchannels.Message{
		Event: alertchannels.EventFire, RuleName: fmt.Sprintf("budget %q", a.Spec.Name), Metric: "budget",
		OccurredAt: a.Evaluation.EvaluatedAt, DedupKey: "budget-" + a.PolicyID + "-" + month,
		Summary: summary, Severity: severity, IdempotencyKey: "financial-budget-channel-" + a.ID + "-" + channelID,
		Footer: "You receive this because a Gregale budget sends its alerts to this channel. Manage it with `gregale billing budgets channels`.",
	}
	if dashboardBaseURL != "" {
		msg.DashboardURL = dashboardBaseURL + "/dashboard/billing"
	}
	return msg
}

// budgetMoney formats millicents with cents precision, keeping any
// sub-cent digits rather than rounding them away.
func budgetMoney(currency string, millicents int64) string {
	unit := 100 * api.MillicentsPerCent
	frac := fmt.Sprintf("%05d", millicents%unit)
	for len(frac) > 2 && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	return fmt.Sprintf("%s %d.%s", currency, millicents/unit, frac)
}
