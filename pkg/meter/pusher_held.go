package meter

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// Reasons the pusher holds a pending usage window back without settling it
// (ADR-20261103121407). A held window stays pending and is retried every pass,
// but only within the billing lookback: past it the window is never billed.
const (
	UsageHeldSuspended          = "suspended"
	UsageHeldDeleting           = "deleting"
	UsageHeldIdentityIncomplete = "identity_incomplete"
	UsageHeldUnknownSubject     = "unknown_subject"
)

var usageHeldReasons = []string{UsageHeldSuspended, UsageHeldDeleting, UsageHeldIdentityIncomplete, UsageHeldUnknownSubject}

// UsageHeldExpiryWarning is how long before a held window leaves the billing
// lookback meterd reports it as expiring, so an operator can bill, settle or
// deliberately waive it first.
const UsageHeldExpiryWarning = 72 * time.Hour

// heldUsage counts one push pass's held windows per (meter, reason).
type heldUsage struct {
	expiresBefore time.Time
	held          map[[2]string]int
	expiring      map[[2]string]int
}

// newHeldUsage starts a pass whose lookback begins at start: a window older
// than start+UsageHeldExpiryWarning leaves the lookback within that warning.
func newHeldUsage(start time.Time) *heldUsage {
	return &heldUsage{expiresBefore: start.Add(UsageHeldExpiryWarning), held: map[[2]string]int{}, expiring: map[[2]string]int{}}
}

func (h *heldUsage) add(meter state.BillingMeter, reason string, hour time.Time) {
	key := [2]string{string(meter), reason}
	h.held[key]++
	if hour.Before(h.expiresBefore) {
		h.expiring[key]++
	}
}

// heldReason is why usageDeliverable refused acct's window.
func heldReason(acct state.Account) string {
	if acct.Status == state.AccountDeletedPending {
		return UsageHeldDeleting
	}
	return UsageHeldSuspended
}

// usageHeldGauges are meterd_billing_usage_held_windows and
// meterd_billing_usage_held_expiring_windows.
type usageHeldGauges struct {
	held, expiring *prometheus.GaugeVec
}

func (p *Pusher) heldGauges() usageHeldGauges {
	return usageHeldGauges{
		held: registerGaugeVec(p.ops.Registry(), prometheus.GaugeOpts{
			Name: "meterd_billing_usage_held_windows",
			Help: "Pending usage windows (account-hours) the last push pass held back without settling, by meter and reason; they are billed once the reason clears, within the billing lookback (ADR-20261103121407).",
		}),
		expiring: registerGaugeVec(p.ops.Registry(), prometheus.GaugeOpts{
			Name: "meterd_billing_usage_held_expiring_windows",
			Help: "Held usage windows that leave the billing lookback within 72 h and are then never billed, by meter and reason (ADR-20261103121407).",
		}),
	}
}

func registerGaugeVec(reg prometheus.Registerer, opts prometheus.GaugeOpts) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(opts, []string{"meter", "reason"})
	if err := reg.Register(g); err != nil {
		var dup prometheus.AlreadyRegisteredError
		if errors.As(err, &dup) {
			if existing, ok := dup.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing
			}
		}
	}
	return g
}

// publishHeld sets the gauges for meter from one pass, zeroing every reason
// the pass did not see.
func (p *Pusher) publishHeld(meter state.BillingMeter, h *heldUsage) {
	gauges := p.heldGauges()
	for _, reason := range usageHeldReasons {
		key := [2]string{string(meter), reason}
		gauges.held.WithLabelValues(string(meter), reason).Set(float64(h.held[key]))
		gauges.expiring.WithLabelValues(string(meter), reason).Set(float64(h.expiring[key]))
	}
	for key, n := range h.expiring {
		if n > 0 && key[0] == string(meter) && p.log != nil {
			p.log.Warn("meter: held usage leaves the billing lookback soon", "meter", key[0], "reason", key[1], "windows", n)
		}
	}
}
