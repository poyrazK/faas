package meter

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metering review r10 (2026-11-23): a subject on a paid plan with no
// complete billing identity for the provider and no complimentary grant is
// never billed. PushPending lists only subjects with a billing_identities
// row, so its held gauges never saw them; the hourly pass only logged a
// warning. meterd_billing_paid_subjects_without_identity counts them per plan
// on every hourly pass, so an operator comps, downgrades or completes them.

// unbillablePlans are the paid plans the gauge always reports, zero included.
var unbillablePlans = []string{"hobby", "pro", "scale"}

func (p *Pusher) paidWithoutIdentityGauge() *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "meterd_billing_paid_subjects_without_identity",
		Help: "Subjects on a paid plan, not complimentary and not suspended or deleting, with no complete billing identity for the configured provider at the last hourly push pass. Their usage is never billed (metering review r10).",
	}, []string{"plan"})
	if err := p.ops.Registry().Register(g); err != nil {
		if dup, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := dup.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing
			}
		}
	}
	return g
}

// publishPaidWithoutIdentity sets the gauge from one hourly pass's counts.
func (p *Pusher) publishPaidWithoutIdentity(counts map[string]int) {
	g := p.paidWithoutIdentityGauge()
	for _, plan := range unbillablePlans {
		g.WithLabelValues(plan).Set(float64(counts[plan]))
	}
	for plan, n := range counts {
		known := false
		for _, k := range unbillablePlans {
			known = known || k == plan
		}
		if !known {
			g.WithLabelValues(plan).Set(float64(n))
		}
	}
}
