package edgewaf

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// budgetRate is a per-app share of worker time in milliseconds: it refills at
// perSecond up to burst.
type budgetRate struct {
	perSecond float64
	burst     float64
}

// sampleRate bounds off-path inspection (ADR-831 amendment 2); inlineRate
// bounds in-path header/URI checks for warn and block rules (amendment 4).
var (
	sampleRate = budgetRate{perSecond: api.EdgeWAFWorkerMsPerAppPerSecond, burst: api.EdgeWAFWorkerMsPerAppBurst}
	inlineRate = budgetRate{perSecond: api.EdgeWAFInlineMsPerAppPerSecond, burst: api.EdgeWAFInlineMsPerAppBurst}
)

// workerBudget is one app's share of WAF worker time, in milliseconds. A
// check is admitted only while the balance is positive, charged an estimate
// on admission, and trued up to its measured time afterwards. The balance may
// go negative, down to minus one burst: an app whose checks cost more than
// estimated waits longer for its next one instead of having the overrun
// forgiven.
type workerBudget struct {
	rate    budgetRate
	balance float64
	last    time.Time
}

func newWorkerBudget(rate budgetRate, now time.Time) *workerBudget {
	return &workerBudget{rate: rate, balance: rate.burst, last: now}
}

func (b *workerBudget) refill(now time.Time) {
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.balance = min(b.balance+elapsed*b.rate.perSecond, b.rate.burst)
		b.last = now
	}
}

// admit charges estimateMs and reports whether the check may run.
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
	b.balance = min(max(b.balance+estimateMs-actualMs, -b.rate.burst), b.rate.burst)
}

// budgets is a per-app set of workerBudgets sharing one rate.
type budgets struct {
	rate budgetRate
	m    map[string]*workerBudget
}

func newBudgets(rate budgetRate) budgets {
	return budgets{rate: rate, m: map[string]*workerBudget{}}
}

func (bs *budgets) admit(appID string, now time.Time, estimateMs float64) bool {
	b, ok := bs.m[appID]
	if !ok {
		if len(bs.m) >= maxTrackedApps {
			bs.m = map[string]*workerBudget{}
		}
		b = newWorkerBudget(bs.rate, now)
		bs.m[appID] = b
	}
	return b.admit(now, estimateMs)
}

// settle trues up an admitted check. A budget forgotten by a map reset is not
// recreated: the overrun is lost, as the reset already granted a fresh burst.
func (bs *budgets) settle(appID string, now time.Time, estimateMs float64, elapsed time.Duration) {
	if b, ok := bs.m[appID]; ok {
		b.settle(now, estimateMs, float64(elapsed)/float64(time.Millisecond))
	}
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
	// estimateInlineMs is a header/URI check with the in-path rule set
	// (amendment 3 addendum: ~0.9 ms p50, ~1.1 ms p95).
	estimateInlineMs = 1.0
)

func estimateMs(paranoiaLevel, bodyBytes int) float64 {
	est := estimateBaseMs + estimateMsPerBodyByte*float64(bodyBytes)
	if paranoiaLevel >= 2 {
		est *= 2
	}
	return est
}
