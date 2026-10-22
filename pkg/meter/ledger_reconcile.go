package meter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// ADR-20261019193600 ledger dual-run results for one account-month.
const (
	LedgerMatch      = "match"
	LedgerPending    = "pending"
	LedgerCapped     = "capped"
	LedgerIncomplete = "incomplete"
	LedgerDrift      = "drift"
	LedgerFailed     = "failed"
)

var ledgerResults = []string{LedgerMatch, LedgerPending, LedgerCapped, LedgerIncomplete, LedgerDrift, LedgerFailed}

// LedgerMonthReconciliation is one month's pass: how many eligible accounts
// got each result, and the summed absolute difference of the drifted ones.
type LedgerMonthReconciliation struct {
	Month          string // "current" or "previous"
	Start, End     time.Time
	Results        map[string]int
	DriftMBSeconds int64
}

// LedgerReconciler compares the financial ledger's net compute quantity with
// the compute receipts the pusher recorded for the same hours (ADR-20261019193600). It
// is read-only.
type LedgerReconciler struct {
	pusher   *Pusher
	provider string
	accounts *prometheus.GaugeVec
	drift    *prometheus.GaugeVec
}

// NewLedgerReconciler wires the dual run onto the pusher's store and clock.
// provider is the name the pusher's receipts and billing identities use
// ("polar", "paddle", "stripe"); empty disables the dual run. reg may be nil.
func NewLedgerReconciler(p *Pusher, provider string, reg prometheus.Registerer) *LedgerReconciler {
	r := &LedgerReconciler{
		pusher:   p,
		provider: provider,
		accounts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_financial_ledger_reconcile_accounts",
			Help: "ADR-20261019193600 ledger dual run: paid accounts per result (match, pending, capped, incomplete, drift, failed) in the last pass, for the current and previous UTC month.",
		}, []string{"month", "result"}),
		drift: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_financial_ledger_drift_mb_seconds",
			Help: "ADR-20261019193600 ledger dual run: summed absolute difference in MB-seconds between ledger net compute and pusher receipts across drifted accounts in the last pass.",
		}, []string{"month"}),
	}
	if reg != nil {
		reg.MustRegister(r.accounts, r.drift)
	}
	return r
}

// AccountsGauge exposes the per-result gauge for tests.
func (r *LedgerReconciler) AccountsGauge() *prometheus.GaugeVec { return r.accounts }

// Loop runs a pass every interval until ctx ends.
func (r *LedgerReconciler) Loop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.pusher.log.Warn("meter: ledger reconciliation", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce reconciles the previous and the current UTC month up to the
// pusher's cutoff and publishes the results.
func (r *LedgerReconciler) RunOnce(ctx context.Context) ([]LedgerMonthReconciliation, error) {
	p, provider := r.pusher, r.provider
	deliveries, readable := p.store.(state.BillingDeliveryReader)
	if provider == "" || !readable {
		return nil, nil
	}
	now := p.now().UTC()
	finalThrough, err := p.usageFinalThrough(ctx, now)
	if err != nil {
		return nil, err
	}
	cutoff := now.Truncate(time.Hour)
	if final := finalThrough.Truncate(time.Hour); final.Before(cutoff) {
		cutoff = final
	}
	accounts, err := p.store.ListAllAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("meter: ledger reconciliation accounts: %w", err)
	}
	caps, err := p.store.LoadAllOverageCapCents(ctx)
	if err != nil {
		return nil, fmt.Errorf("meter: ledger reconciliation caps: %w", err)
	}
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var out []LedgerMonthReconciliation
	for _, month := range []struct {
		label string
		start time.Time
	}{{"previous", current.AddDate(0, -1, 0)}, {"current", current}} {
		pass, err := r.reconcileMonth(ctx, deliveries, provider, accounts, caps, month.label, month.start, cutoff)
		if err != nil {
			return out, err
		}
		out = append(out, pass)
		for _, result := range ledgerResults {
			r.accounts.WithLabelValues(pass.Month, result).Set(float64(pass.Results[result]))
		}
		r.drift.WithLabelValues(pass.Month).Set(float64(pass.DriftMBSeconds))
	}
	return out, nil
}

func (r *LedgerReconciler) reconcileMonth(ctx context.Context, deliveries state.BillingDeliveryReader, provider string, accounts []state.Account, caps map[string]int64, label string, start, cutoff time.Time) (LedgerMonthReconciliation, error) {
	end := start.AddDate(0, 1, 0)
	if cutoff.Before(end) {
		end = cutoff
	}
	pass := LedgerMonthReconciliation{Month: label, Start: start, End: end, Results: map[string]int{}}
	if !end.After(start) {
		return pass, nil
	}
	pendingWindows, err := r.pusher.store.PendingBillingUsageWindows(ctx, provider, start, end)
	if err != nil {
		return pass, fmt.Errorf("meter: ledger reconciliation pending windows: %w", err)
	}
	pending := make(map[string]bool, len(pendingWindows))
	for _, w := range pendingWindows {
		pending[w.AccountID] = true
	}
	for _, acct := range accounts {
		if acct.Plan == "free" || acct.Status == state.AccountSuspended || acct.Status == state.AccountDeletedPending {
			continue
		}
		result, diff := r.reconcileAccount(ctx, deliveries, provider, acct, start, end, pending[acct.ID], caps)
		if result == "" {
			continue
		}
		pass.Results[result]++
		if result == LedgerDrift {
			pass.DriftMBSeconds += max(diff, -diff)
		}
	}
	return pass, nil
}

// reconcileAccount returns "" for an account outside the window (no billing
// identity, or one that starts after the cutoff), and otherwise its result
// with the delivered-minus-ledger difference in MB-seconds.
func (r *LedgerReconciler) reconcileAccount(ctx context.Context, deliveries state.BillingDeliveryReader, provider string, acct state.Account, start, end time.Time, pending bool, caps map[string]int64) (string, int64) {
	p := r.pusher
	identity, err := p.store.BillingIdentity(ctx, acct.ID, provider)
	if errors.Is(err, state.ErrNotFound) {
		return "", 0
	}
	if err != nil {
		p.log.Warn("meter: ledger reconciliation identity", "account", acct.ID, "err", err)
		return LedgerFailed, 0
	}
	from := identity.BillingFrom.UTC().Truncate(time.Minute)
	if from.Before(start) {
		from = start
	}
	if !from.Before(end) {
		return "", 0
	}
	if pending {
		return LedgerPending, 0
	}
	// Overage-mode providers (Polar, Paddle) receive MB-seconds above the
	// allowance; raw-mode providers (legacy Stripe) receive all of it.
	net := providerUsageMode(p.pusher) == billing.UsageModeOverage
	atEnd, complete, err := ledgerComputeQuantity(ctx, p.store, acct.ID, start, end, net)
	if err != nil {
		p.log.Warn("meter: ledger reconciliation costs", "account", acct.ID, "err", err)
		return LedgerFailed, 0
	}
	if !complete {
		return LedgerIncomplete, 0
	}
	var atFrom int64
	if from.After(start) {
		if atFrom, _, err = ledgerComputeQuantity(ctx, p.store, acct.ID, start, from, net); err != nil {
			p.log.Warn("meter: ledger reconciliation costs", "account", acct.ID, "err", err)
			return LedgerFailed, 0
		}
	}
	delivered, err := deliveries.DeliveredComputeMBSeconds(ctx, provider, acct.ID, from.Truncate(time.Hour), end)
	if err != nil {
		p.log.Warn("meter: ledger reconciliation receipts", "account", acct.ID, "err", err)
		return LedgerFailed, 0
	}
	expected := atEnd - atFrom
	diff := delivered - expected
	switch {
	case diff == 0:
		return LedgerMatch, 0
	case diff < 0 && hasCap(caps, acct.ID):
		return LedgerCapped, diff
	default:
		p.log.Warn("meter: ledger drift", "account", acct.ID, "provider", provider,
			"from", from, "through", end, "ledger_mb_seconds", expected, "delivered_mb_seconds", delivered)
		return LedgerDrift, diff
	}
}

func hasCap(caps map[string]int64, accountID string) bool {
	_, ok := caps[accountID]
	return ok
}

// ledgerComputeQuantity is the ledger's compute quantity for usage in
// [start, through): above the month's allowance when net, all of it
// otherwise. It also reports whether the evidence behind it is complete.
func ledgerComputeQuantity(ctx context.Context, store state.Store, accountID string, start, through time.Time, net bool) (int64, bool, error) {
	report, err := billing.ReadFinancialCosts(ctx, store, accountID, start, through)
	if err != nil {
		return 0, false, err
	}
	for _, meter := range report.Meters {
		if meter.Meter != string(state.BillingMeterCompute) {
			continue
		}
		quantity := meter.Accrued.Quantity
		if net {
			quantity = max(quantity-meter.Accrued.IncludedQuantity, 0)
		}
		return quantity, meter.Coverage.Complete, nil
	}
	return 0, false, nil
}
