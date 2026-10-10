package edgewaf

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// workerBudget is one app's share of WAF worker time, in milliseconds
// (ADR-831 amendment 2). It refills at api.EdgeWAFWorkerMsPerAppPerSecond up
// to api.EdgeWAFWorkerMsPerAppBurst. A sample is admitted only while the
// balance is positive, charged an estimate on admission, and trued up to its
// measured time once inspected. The balance may go negative, down to minus
// one burst: an app whose samples cost more than estimated waits longer for
// its next inspection instead of having the overrun forgiven.
type workerBudget struct {
	balance float64
	last    time.Time
}

func newWorkerBudget(now time.Time) *workerBudget {
	return &workerBudget{balance: api.EdgeWAFWorkerMsPerAppBurst, last: now}
}

func (b *workerBudget) refill(now time.Time) {
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.balance = min(b.balance+elapsed*api.EdgeWAFWorkerMsPerAppPerSecond, api.EdgeWAFWorkerMsPerAppBurst)
		b.last = now
	}
}

// admit charges estimateMs and reports whether the sample may be inspected.
func (b *workerBudget) admit(now time.Time, estimateMs float64) bool {
	b.refill(now)
	if b.balance <= 0 {
		return false
	}
	b.balance -= estimateMs
	return true
}

// settle replaces the estimate charged at admission with the measured time.
func (b *workerBudget) settle(now time.Time, estimateMs, actualMs float64) {
	b.refill(now)
	b.balance = max(b.balance+estimateMs-actualMs, -api.EdgeWAFWorkerMsPerAppBurst)
}

// Inspection cost model from ADR-831 amendment 2 (one 2.8 GHz Xeon core):
// about 2 ms per request at paranoia level 1 plus about 25 µs per inspected
// body byte for many-field JSON, the expensive common case; level 2 costs
// about twice as much. The estimate only has to be close enough that a burst
// of admissions cannot queue far more work than the app's balance; settle
// corrects it with the measured time.
const (
	estimateBaseMs        = 2.0
	estimateMsPerBodyByte = 0.025
)

func estimateMs(paranoiaLevel, bodyBytes int) float64 {
	est := estimateBaseMs + estimateMsPerBodyByte*float64(bodyBytes)
	if paranoiaLevel >= 2 {
		est *= 2
	}
	return est
}
