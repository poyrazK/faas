package state

import "github.com/onebox-faas/faas/pkg/api"

// appLogDrainOwnership is what an enabled drain's delivery depends on beyond
// the drain row: the plan of the account its app's resources belong to (the
// organization's once it adopted billing, else the creator's; see
// app_resource_accounts), the billing subject whose account-wide drain
// allowance the app shares (quotaSubjectSQL), whether the app still spends
// that allowance (a deleted app does not, as CreateAppLogDrainIfUnderQuota
// counts), and whether an organization's application standard installed the
// drain, which the organization governs instead of the per-app allowance.
type appLogDrainOwnership struct {
	Plan     string `json:"plan"`
	Subject  string `json:"subject"`
	Counted  bool   `json:"counted"`
	Standard bool   `json:"standard"`
}

// appLogDrainPlanLedger admits enabled drains, oldest first, while their
// owner's current plan has room for them. A plan's drain allowances are only
// checked when a drain is created, so after a downgrade, a cancellation or a
// lapsed trial an owner can hold more drains than its plan allows, and on
// Free none at all (logs audit 2026-10-31 D3). Delivery keeps the oldest ones
// the plan covers and leaves the rest out until the owner upgrades or
// disables or deletes enough of them; nothing is changed on the drains, so an
// upgrade resumes them all.
type appLogDrainPlanLedger struct {
	apps     map[string]int
	subjects map[string]int
}

func newAppLogDrainPlanLedger() appLogDrainPlanLedger {
	return appLogDrainPlanLedger{apps: map[string]int{}, subjects: map[string]int{}}
}

// admit reports whether the plan covers the drain of appID, and spends its
// slots when it does. Callers pass drains in creation order.
func (l appLogDrainPlanLedger) admit(appID string, o appLogDrainOwnership) bool {
	if o.Standard {
		return true
	}
	limits, ok := api.LimitsFor(api.Plan(o.Plan))
	if !ok || l.apps[appID] >= limits.LogDrainPerApp || o.Counted && l.subjects[o.Subject] >= limits.LogDrainPerAccount {
		return false
	}
	l.apps[appID]++
	if o.Counted {
		l.subjects[o.Subject]++
	}
	return true
}

// planCoveredAppLogDrains keeps the drains whose owner's plan covers them.
// drains are enabled, in creation order, and owners[i] belongs to drains[i].
func planCoveredAppLogDrains(drains []AppLogDrain, owners []appLogDrainOwnership) []AppLogDrain {
	ledger := newAppLogDrainPlanLedger()
	out := make([]AppLogDrain, 0, len(drains))
	for i, d := range drains {
		if ledger.admit(d.AppID, owners[i]) {
			out = append(out, d)
		}
	}
	return out
}
