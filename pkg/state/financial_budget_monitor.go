package state

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// FinancialBudgetMonitorStore atomically retains an observation and its outbox
// events and stop decisions against an unchanged policy revision. Claims and
// settlements are fenced.
type FinancialBudgetMonitorStore interface {
	RecordFinancialBudgetEvaluation(context.Context, string, string, int64, financial.BudgetEvaluation) error
	FinancialBudgetRuntime(context.Context, string, string, int64, time.Time) (financial.BudgetRuntime, error)
	ClaimFinancialBudgetAlert(context.Context, time.Time) (FinancialBudgetAlertDelivery, error)
	SettleFinancialBudgetAlert(context.Context, string, string, time.Time, bool) error
}

type FinancialBudgetAlertDelivery struct {
	financial.BudgetAlert
	AccountID  string
	PolicyID   string
	LeaseToken string
	Spec       financial.BudgetSpec
}

func validFinancialBudgetEvaluation(e financial.BudgetEvaluation) bool {
	start := e.PeriodStart.UTC()
	return !start.IsZero() && start.Day() == 1 && start.Equal(start.Truncate(24*time.Hour)) &&
		e.PeriodEnd.Equal(start.AddDate(0, 1, 0)) && !e.EvaluatedAt.IsZero() &&
		!e.EvaluatedAt.Before(start) && e.EvaluatedAt.Before(e.PeriodEnd) &&
		!e.AsOf.Before(start) && !e.AsOf.After(e.EvaluatedAt) &&
		e.EvidenceThroughID >= 0 && (e.KnownMillicents == nil || *e.KnownMillicents >= 0) &&
		(e.ProjectedMillicents == nil || *e.ProjectedMillicents >= 0 && e.KnownMillicents != nil) &&
		(e.KnownMillicents != nil || (!e.Fresh && !e.CoverageComplete))
}

func financialBudgetAlerts(p FinancialBudget, e financial.BudgetEvaluation) []FinancialBudgetAlertDelivery {
	if e.KnownMillicents == nil || !e.Fresh {
		return nil
	}
	thresholds := append(slices.Clone(p.Spec.NotifyMillicents), p.Spec.LimitMillicents)
	slices.Sort(thresholds)
	thresholds = slices.Compact(thresholds)
	out := []FinancialBudgetAlertDelivery{}
	for _, threshold := range thresholds {
		if *e.KnownMillicents < threshold {
			continue
		}
		key := fmt.Sprintf("financial-budget-alert\n%s\n%s\n%d\n%s\n%d", p.AccountID, p.ID, p.Revision, e.PeriodStart.UTC().Format("2006-01"), threshold)
		out = append(out, FinancialBudgetAlertDelivery{AccountID: p.AccountID, PolicyID: p.ID, Spec: cloneFinancialBudgetSpec(p.Spec), BudgetAlert: financial.BudgetAlert{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String(), PolicyRevision: p.Revision, ThresholdMillicents: threshold, Evaluation: cloneFinancialBudgetEvaluation(e), State: "pending", NextAttemptAt: e.EvaluatedAt.UTC()}})
	}
	return out
}

// FinancialBudgetProjectionStore delivers projected-overrun alerts to the
// account email (ADR-20261019193500), fenced like threshold alerts.
// RecordFinancialBudgetEvaluation records them; FinancialBudgetRuntime
// reports the month's projection.
type FinancialBudgetProjectionStore interface {
	ClaimFinancialBudgetProjection(context.Context, time.Time) (FinancialBudgetAlertDelivery, error)
	SettleFinancialBudgetProjection(context.Context, string, string, time.Time, bool) error
}

// financialBudgetProjectionAlert raises one alert per policy revision and UTC
// month when complete, fresh evidence projects the limit to be reached while
// known usage is still below it.
func financialBudgetProjectionAlert(p FinancialBudget, e financial.BudgetEvaluation) (FinancialBudgetAlertDelivery, bool) {
	limit := p.Spec.LimitMillicents
	if e.KnownMillicents == nil || e.ProjectedMillicents == nil || !e.Fresh || !e.CoverageComplete || limit <= 0 || *e.KnownMillicents >= limit || *e.ProjectedMillicents < limit {
		return FinancialBudgetAlertDelivery{}, false
	}
	key := fmt.Sprintf("financial-budget-projection\n%s\n%s\n%d\n%s", p.AccountID, p.ID, p.Revision, e.PeriodStart.UTC().Format("2006-01"))
	return FinancialBudgetAlertDelivery{AccountID: p.AccountID, PolicyID: p.ID, Spec: cloneFinancialBudgetSpec(p.Spec), BudgetAlert: financial.BudgetAlert{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String(), Kind: financial.BudgetAlertProjection, PolicyRevision: p.Revision, ThresholdMillicents: limit, Evaluation: cloneFinancialBudgetEvaluation(e), State: "pending", NextAttemptAt: e.EvaluatedAt.UTC()}}, true
}

func cloneFinancialBudgetEvaluation(e financial.BudgetEvaluation) financial.BudgetEvaluation {
	e.Reasons = append([]string{}, e.Reasons...)
	if e.KnownMillicents != nil {
		amount := *e.KnownMillicents
		e.KnownMillicents = &amount
	}
	if e.ProjectedMillicents != nil {
		amount := *e.ProjectedMillicents
		e.ProjectedMillicents = &amount
	}
	return e
}

func cloneFinancialBudgetAlert(a FinancialBudgetAlertDelivery) FinancialBudgetAlertDelivery {
	a.Spec = cloneFinancialBudgetSpec(a.Spec)
	a.Evaluation = cloneFinancialBudgetEvaluation(a.Evaluation)
	if a.DeliveredAt != nil {
		at := *a.DeliveredAt
		a.DeliveredAt = &at
	}
	return a
}

func financialBudgetAlertRetry(attempts int) time.Duration {
	delay := api.FinancialBudgetAlertRetryBase
	for i := 1; i < attempts && delay < api.FinancialBudgetAlertRetryMax; i++ {
		delay = min(delay*2, api.FinancialBudgetAlertRetryMax)
	}
	return delay
}
