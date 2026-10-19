package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// Meterd writes decisions atomically with evidence. Schedd alone tears down
// instances and acknowledges targets. These rows never mutate app/account intent.
type FinancialBudgetStopStore interface {
	ActiveFinancialBudgetStops(context.Context, string, time.Time) ([]FinancialBudgetStop, error)
	FinancialBudgetStopTargets(context.Context, string, string) ([]financial.BudgetStopTarget, error)
	RecordFinancialBudgetStopTarget(context.Context, string, string, financial.BudgetStopTarget) error
}

type FinancialBudgetStop struct {
	financial.BudgetStop
	AccountID string
	Spec      financial.BudgetSpec
}

func financialBudgetStop(p FinancialBudget, e financial.BudgetEvaluation) (FinancialBudgetStop, bool) {
	if !financial.PreviewStopping(p.Spec) || !e.Fresh || e.KnownMillicents == nil || *e.KnownMillicents < p.Spec.LimitMillicents {
		return FinancialBudgetStop{}, false
	}
	key := fmt.Sprintf("financial-budget-stop\n%s\n%s\n%d\n%s", p.AccountID, p.ID, p.Revision, e.PeriodStart.UTC().Format("2006-01"))
	return FinancialBudgetStop{AccountID: p.AccountID, Spec: cloneFinancialBudgetSpec(p.Spec), BudgetStop: financial.BudgetStop{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String(), PolicyID: p.ID, Revision: p.Revision,
		PeriodStart: e.PeriodStart, PeriodEnd: e.PeriodEnd, RequestedAt: e.EvaluatedAt,
		DrainDeadline: e.EvaluatedAt.Add(time.Duration(p.Spec.DrainSeconds) * time.Second),
		ResumeRule:    p.Spec.ResumeRule, Evaluation: cloneFinancialBudgetEvaluation(e), Targets: []financial.BudgetStopTarget{},
	}}, true
}

func financialBudgetStopActive(s FinancialBudgetStop, p FinancialBudget, now time.Time) bool {
	return p.AccountID == s.AccountID && p.Revision == s.Revision && p.DeletedAt == nil &&
		p.Spec.Enabled && financial.PreviewStopping(p.Spec) && !now.Before(s.RequestedAt) &&
		(s.ResumeRule == "manual" || now.Before(s.PeriodEnd))
}

// Matches uses persisted preview identity, never a slug, environment name or
// absence of production protection. environmentID is the immutable runtime owner.
func (s FinancialBudgetStop) Matches(app App, environmentID string) bool {
	return app.AccountID == s.AccountID && app.PreviewOfSlug != "" &&
		s.Spec.Scope.Matches(financial.Attribution{AppID: app.ID, ProjectID: app.ProjectID, EnvironmentID: environmentID})
}

func cloneFinancialBudgetStop(s FinancialBudgetStop) FinancialBudgetStop {
	s.Spec = cloneFinancialBudgetSpec(s.Spec)
	s.Evaluation = cloneFinancialBudgetEvaluation(s.Evaluation)
	s.Targets = append([]financial.BudgetStopTarget{}, s.Targets...)
	return s
}

func validFinancialBudgetStopTarget(t financial.BudgetStopTarget) bool {
	if uuid.Validate(t.AppID) != nil || t.EnvironmentID != "" && uuid.Validate(t.EnvironmentID) != nil || t.ObservedAt.IsZero() || t.ResidentCount < 0 || t.ResidentCount > api.FinancialBudgetResidentCountMax {
		return false
	}
	switch t.State {
	case "requested", "draining", "enforced", "failed":
	default:
		return false
	}
	return (t.State != "enforced" || t.ResidentCount == 0) &&
		(t.LastError == "" || t.LastError == "runtime_identity_unavailable" || t.LastError == "vm_teardown_failed" || t.LastError == "instance_settlement_failed")
}
