package meter

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Residency computes "resident GB-RAM-hours per paying customer" per
// plan and emits the §12 dashboard panel (ADR-031, PR #141).
//
// Why a per-plan average: the spec line 417 names the row "resident GB
// per paying customer" with a single threshold (> 0.45 warn). The
// number is meaningful per plan because the Hobby plan's 256 MB RAM
// is ~half the Scale plan's 1 GB; a fleet-wide average hides plan
// skew. Splitting by plan lets ops see which segment is migrating
// toward the §12 page threshold (0.45) — the alert rule
// FaasResidentGbPerCustomerHigh fans out via {{ $labels.plan }}.
//
// Definition: Σ(monthly GB-RAM-hours across paying accounts of plan P)
//
//	÷ count(paying accounts of plan P)
//
// "Paying" includes active + past_due + suspended, but NOT
// deleted_pending (a deleted account is no longer billable). This is
// deliberately broader than pkg/state.Account.Active() — which
// excludes past_due and suspended — because suspended accounts still
// have running instances until the reaper parks them, and the resident
// GB-RAM-hours they consume are real platform cost we want the metric
// to reflect.
//
// Per-plan emission contract: a plan with N=0 paying customers emits
// 0 (not raw ΣGB), so the metric name "resident GB per paying customer"
// is honoured even at zero customers. Otherwise a churned customer's
// stale usage rows could keep the gauge non-zero after they've left,
// and FaasResidentGbPerCustomerHigh would false-positive on the
// historical ΣGB. The dashboard and alert rule both treat 0 as
// "no signal" — see the per-plan loop in RunOnce.
//
// Cadence: cfg.ResidencyInterval (default 60 s). Per the §12 alert
// rule's `for: 1h`, the gauge can be wrong for 1 hour before the
// page fires; 60 s is enough resolution without wasted DB scans.
type Residency struct {
	store state.Store
	now   func() time.Time
	log   *slog.Logger
	ops   *wire.OpsMetrics
}

// NewResidency wires the per-tick collaborator. ops and log may be nil;
// the RunOnce path coerces nil to slog.Default and tolerates a nil
// ops (the SetResidentGBPerCustomer method is itself nil-safe).
func NewResidency(store state.Store, now func() time.Time, log *slog.Logger, ops *wire.OpsMetrics) *Residency {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Residency{store: store, now: now, log: log, ops: ops}
}

// Paying reports whether an account's billable GB-RAM-hours should
// contribute to the per-plan average. Extends pkg/state.Active()
// deliberately: suspended accounts are still running instances
// (reaper hasn't parked them yet) and their consumption is real
// platform cost. deleted_pending is excluded because the account is
// pending hard-delete (G6) — its usage rows are stale rows the
// retention sweep hasn't pruned yet.
func Paying(a state.Account) bool {
	switch a.Status {
	case state.AccountActive, state.AccountPastDue, state.AccountSuspended:
		return true
	}
	return false
}

// RunOnce emits one round of per-plan resident GB-per-customer gauges.
// Returns the per-plan paying-customer counts so tests can assert
// "active + past_due counted, suspended counted, deleted_pending
// excluded" without re-querying the Store.
//
// Error handling has two distinct branches inside the per-account
// loop:
//   - state.ErrNotFound from MonthUsageForAccount (no usage row for
//     the current month): the account is billable but has no recorded
//     consumption yet (just signed up, never woken an instance). Count
//     it in the divisor and contribute 0 GB to the numerator — the
//     average stays meaningful and NaN-free.
//   - any OTHER error (Postgres deadlock, connection reset mid-query):
//     a transient backend hiccup. Skip-without-count: do NOT increment
//     the divisor. Counting the account on a partial-failure would
//     drag the per-plan average toward 0 and silence
//     FaasResidentGbPerCustomerHigh exactly when we want it loud. The
//     gauge series stays at its last value (Prometheus semantics) and
//     the next tick re-tries.
//
// On a Store.ListAllAccounts error the function returns (nil, err) so
// the loop can stop and inspect — recovery is a daemon restart for a
// fully-stuck Store, which is heavy enough to warrant the call.
func (r *Residency) RunOnce(ctx context.Context) (map[api.Plan]int, error) {
	accounts, err := r.store.ListAllAccounts(ctx)
	if err != nil {
		return nil, err
	}

	now := r.now()
	totalGB := make(map[api.Plan]float64)
	count := make(map[api.Plan]int)
	for _, acct := range accounts {
		if !Paying(acct) {
			continue
		}
		usages, err := MonthUsageForAccount(ctx, r.store, acct.ID, now)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				// No usage rows for the current month — billable
				// customer with zero consumption. Count in the divisor,
				// contribute 0 GB. NaN-free.
				count[acct.Plan]++
				continue
			}
			// Transient Store error. Skip-without-count: do NOT
			// increment count[acct.Plan]. Otherwise the per-plan average
			// drops on partial failures and FaasResidentGbPerCustomerHigh
			// false-negatives.
			r.log.Warn("meter: residency usage_by_month", "account", acct.ID, "err", err)
			continue
		}
		totalGB[acct.Plan] += MonthlyUsageGB(usages)
		count[acct.Plan]++
	}

	// Emit one gauge sample per plan, including zero-customer plans.
	// N=0 paying customers emits 0 (NOT raw ΣGB) so the metric name
	// "resident GB per paying customer" is honoured when the plan
	// segment is empty. The dashboard and alert rule both treat 0 as
	// "no signal" — see the type doc comment above.
	for _, plan := range api.Plans {
		var avg float64
		if n := count[plan]; n > 0 {
			avg = totalGB[plan] / float64(n)
		}
		r.ops.SetResidentGBPerCustomer(string(plan), avg)
	}
	return count, nil
}
