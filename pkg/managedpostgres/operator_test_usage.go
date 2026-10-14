package managedpostgres

import (
	"context"
	"time"
)

// NewOperatorTestUsageAdmission permits only explicitly approved, bounded
// production tests to proceed while fully collected retired usage awaits final
// corrections. It does not change stored evidence, diagnostics or collection.
func NewOperatorTestUsageAdmission(registry *Registry, store UsageStore, getenv func(string) string, now func() time.Time) func(context.Context, string, UsageCeilings) error {
	if now == nil {
		now = time.Now
	}
	provisioning := NewProvisioningGate(registry, getenv, now)
	accountGate := NewCanaryAccountGate(getenv)
	var approval *OperatorTestApproval
	if getenv != nil {
		approval, _ = loadOperatorTestApproval(getenv(OperatorTestPathEnv))
	}
	return func(ctx context.Context, accountID string, ceilings UsageCeilings) error {
		if registry == nil {
			return ErrUnavailable
		}
		current := now().UTC()
		allow := approval != nil && approval.AllowPendingFinalCorrections && productionEnvironment(getenv) && provisioning() && accountGate(accountID)
		return registry.UsagePolicy().admitWithCeilings(ctx, store, accountID, current, ceilings, allow)
	}
}

func (p UsageProgress) freshRetiredCoverage(policy UsagePolicy, now time.Time) bool {
	end := usageEnd(p.EndedAt, policy.Window)
	if !p.Terminal || p.Unresolved || p.CreatedAt.IsZero() || p.CreatedAt.After(p.EndedAt) || p.EndedAt.After(now) || end.IsZero() || end.After(now) || !p.CollectedUntil.Equal(end) || p.CollectedFrom.IsZero() || p.CollectedFrom.After(p.CreatedAt.UTC().Truncate(policy.Window)) || p.ObservedAt.Before(end) {
		return false
	}
	for _, observed := range []time.Time{p.ObservedAt, p.CorrectionObservedAt} {
		if observed.IsZero() || observed.After(now) || now.Sub(observed) > policy.StaleAfter {
			return false
		}
	}
	return true
}
