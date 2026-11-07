package meter

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// The egress half of the ADR-20261019193600 dual run: the ledger's egress
// evidence, hour by hour, goes through the egress hour rules
// (billEgressHours) the pusher bills by, and what that bills is compared with
// the egress receipts the pusher settled, shadow or live.

// egressLedger is one month's egress dual-run inputs.
type egressLedger struct {
	policies billing.MeterUsagePolicyProvider
	fs       state.FinancialStore
	hourly   state.FinancialHourlyUsageReader
	// complete is whether retention and egress sampling cover the window.
	complete bool
}

// egressLedgerFor returns the egress dual run's inputs for [start, end); ok is
// false when the provider bills no egress for any paid plan or the store
// keeps no hourly ledger.
func (r *LedgerReconciler) egressLedgerFor(ctx context.Context, start, end time.Time) (egressLedger, bool, error) {
	p := r.pusher
	policies, ok := p.pusher.(billing.MeterUsagePolicyProvider)
	if !ok {
		return egressLedger{}, false, nil
	}
	configured := false
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		if _, on := policies.MeterUsagePolicy(plan, state.BillingMeterEgress); on {
			configured = true
			break
		}
	}
	fs, financial := p.store.(state.FinancialStore)
	hourly, readable := p.store.(state.FinancialHourlyUsageReader)
	if !configured || !financial || !readable {
		return egressLedger{}, false, nil
	}
	retained, err := fs.FinancialEvidenceCoverage(ctx)
	if err != nil {
		return egressLedger{}, false, fmt.Errorf("meter: ledger egress retention: %w", err)
	}
	coverage, err := fs.FinancialSamplingCoverage(ctx, start, end)
	if err != nil {
		return egressLedger{}, false, fmt.Errorf("meter: ledger egress coverage: %w", err)
	}
	complete := !start.Before(retained) && coverage.EgressMinutes == int64(end.Sub(start)/time.Minute)
	return egressLedger{policies: policies, fs: fs, hourly: hourly, complete: complete}, true, nil
}

// ledgerEgressHours bills acct's ledger egress evidence in [start, end) by the
// egress hour rules; the hours from from on are the ones the pusher delivers.
// The hour holding billing_from splits at its minute.
func (r *LedgerReconciler) ledgerEgressHours(ctx context.Context, ledger egressLedger, acct state.Account, start, end, from time.Time) ([]egressBilledHour, error) {
	head, err := ledger.fs.FinancialEvidenceHead(ctx, acct.ID, start, start.AddDate(0, 1, 0))
	if err != nil {
		return nil, err
	}
	var before, after []state.HourQuantity
	if from.After(start) {
		if before, err = ledger.hourly.FinancialHourlyUsage(ctx, acct.ID, string(state.BillingMeterEgress), start, from, head); err != nil {
			return nil, err
		}
	}
	if after, err = ledger.hourly.FinancialHourlyUsage(ctx, acct.ID, string(state.BillingMeterEgress), from, end, head); err != nil {
		return nil, err
	}
	return billEgressHours(ctx, r.pusher.store, ledger.policies, r.pusher.newMonthGrants(), acct, append(before, after...), len(before))
}

// reconcileEgress compares the ledger's billed egress for acct in [from, end)
// with the egress receipts, and returns the result and delivered-minus-ledger
// bytes.
func (r *LedgerReconciler) reconcileEgress(ctx context.Context, deliveries state.BillingDeliveryReader, provider string, ledger egressLedger, acct state.Account, start, end, from time.Time, pending bool, caps map[string]int64) (string, int64) {
	p := r.pusher
	if pending {
		return LedgerPending, 0
	}
	if !ledger.complete {
		return LedgerIncomplete, 0
	}
	hours, err := r.ledgerEgressHours(ctx, ledger, acct, start, end, from)
	if err != nil {
		p.log.Warn("meter: ledger egress reconciliation", "account", acct.ID, "err", err)
		return LedgerFailed, 0
	}
	var expected int64
	for _, h := range hours {
		if h.delivered {
			expected += h.billed
		}
	}
	delivered, err := deliveries.DeliveredEgressBytes(ctx, provider, acct.ID, from.Truncate(time.Hour), end)
	if err != nil {
		p.log.Warn("meter: ledger egress reconciliation receipts", "account", acct.ID, "err", err)
		return LedgerFailed, 0
	}
	diff := delivered - expected
	switch {
	case diff == 0:
		return LedgerMatch, 0
	case diff < 0:
		capped, err := r.egressCapExplains(ctx, acct, caps, end, hours, delivered)
		if err != nil {
			p.log.Warn("meter: ledger egress reconciliation cap", "account", acct.ID, "err", err)
			return LedgerFailed, 0
		}
		if capped {
			return LedgerCapped, diff
		}
	}
	p.log.Warn("meter: ledger egress drift", "account", acct.ID, "provider", provider,
		"from", from, "through", end, "ledger_bytes", expected, "delivered_bytes", delivered)
	return LedgerDrift, diff
}

// egressCapExplains reports whether acct's spend cap accounts for delivering
// less egress than the ledger bills. The pusher clamps each live hour to the
// cap less the month's billed compute and object-storage charge at that
// hour; both only grow within a month, so the allowance at the window's last
// hour is the least it ever granted. Clamping every hour to that allowance
// gives the least the pusher could have delivered: delivering at least that
// is the cap at work, delivering less is lost usage.
func (r *LedgerReconciler) egressCapExplains(ctx context.Context, acct state.Account, caps map[string]int64, end time.Time, hours []egressBilledHour, delivered int64) (bool, error) {
	capCents, ok := caps[acct.ID]
	if !ok || len(hours) == 0 {
		return false, nil
	}
	p := r.pusher
	last := end.Add(-time.Hour)
	monthStart := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, time.UTC)
	computeRaw, err := sumUsageRows(ctx, p.store, acct.ID, monthStart, end)
	if err != nil {
		return false, err
	}
	compute, err := p.newMonthGrants().billedBefore(ctx, p.store, acct, end, computeRaw)
	if err != nil {
		return false, err
	}
	objectStorage, err := liveObjectStorageReserved(ctx, p.store, acct.ID, last)
	if err != nil {
		return false, err
	}
	// A paid hour before billing_from was billed by the provider this one
	// replaced and spends the cap too, as the pusher's clamp counts it.
	var floor, billedLive int64
	for _, h := range hours {
		switch {
		case !h.live:
			if h.delivered {
				floor += h.billed // shadow delivery is never clamped
			}
			continue
		case h.delivered:
			floor += clampToAllowance(billedLive, billedLive+h.billed, capEgressAllowance(capCents, compute, objectStorage, h.policy))
		}
		billedLive += h.billed
	}
	return delivered >= floor, nil
}
