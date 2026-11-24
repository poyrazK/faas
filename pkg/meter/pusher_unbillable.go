package meter

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// Metering review r10 (2026-11-23): a subject on a paid plan with no
// complete billing identity for the provider and no complimentary grant is
// never billed. PushPending lists only subjects with a billing_identities
// row, so its held gauges never saw them; the hourly pass only logged a
// warning. meterd_billing_paid_subjects_without_identity counts them per plan
// hourly from PushPending, so an operator comps, downgrades or completes them.

// unbillablePlans are the paid plans the gauge always reports, zero included.
var unbillablePlans = []string{"hobby", "pro", "scale"}

func (p *Pusher) paidWithoutIdentityGauge() *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "meterd_billing_paid_subjects_without_identity",
		Help: "Subjects on a paid plan, not complimentary and not suspended or deleting, with no complete billing identity for the configured provider at the last hourly push pass. Their usage is never billed (metering review r10).",
	}, []string{"plan"})
	if err := p.ops.Registry().Register(g); err != nil {
		var dup prometheus.AlreadyRegisteredError
		if errors.As(err, &dup) {
			if existing, ok := dup.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing
			}
		}
	}
	return g
}

// unbillableRefreshEvery bounds the identity lookups: one per paid subject.
const unbillableRefreshEvery = time.Hour

// reportPaidWithoutIdentity refreshes the gauge from PushPending, meterd's
// billing path, at most hourly. A failure only logs: it never blocks billing,
// and the next pass retries.
func (p *Pusher) reportPaidWithoutIdentity(ctx context.Context, now time.Time) {
	if !p.unbillableAt.IsZero() && now.Sub(p.unbillableAt) < unbillableRefreshEvery {
		return
	}
	accounts, err := state.ListBillingResourceAccounts(ctx, p.store)
	if err != nil {
		p.logUnbillable(err)
		return
	}
	comps, err := p.complimentaryIDs(ctx)
	if err != nil {
		p.logUnbillable(err)
		return
	}
	counts := map[string]int{}
	for _, acct := range accounts {
		if acct.Plan == "free" {
			continue
		}
		if _, comp := comps[acct.ID]; comp {
			continue
		}
		if !usageDeliverable(acct, now) {
			continue // held and reported as suspended or deleting
		}
		acct, err := p.accountForProvider(ctx, acct)
		if err != nil {
			p.logUnbillable(err)
			return
		}
		if acct.ProviderCustomerID == "" || acct.StripeSubscriptionItem == "" {
			counts[string(acct.Plan)]++
			if p.log != nil {
				p.log.Warn("meter: billing identity incomplete", "account", acct.ID, "plan", acct.Plan)
			}
		}
	}
	p.publishPaidWithoutIdentity(counts)
	p.unbillableAt = now
}

func (p *Pusher) logUnbillable(err error) {
	if p.log != nil {
		p.log.Warn("meter: count paid subjects without billing identity", "err", err)
	}
}

// publishPaidWithoutIdentity sets the gauge from one pass's counts.
func (p *Pusher) publishPaidWithoutIdentity(counts map[string]int) {
	g := p.paidWithoutIdentityGauge()
	// Reset drops a plan that no longer has any, so the alert clears.
	g.Reset()
	for _, plan := range unbillablePlans {
		g.WithLabelValues(plan).Set(0)
	}
	for plan, n := range counts {
		g.WithLabelValues(plan).Set(float64(n))
	}
}
