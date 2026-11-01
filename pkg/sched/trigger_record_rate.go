// trigger_record_rate.go — the per-plan cap on the records an app's broker
// triggers take from their brokers each second (ADR-20261101093015).
//
// api.Limits.TriggerRecordsPerSecondPerApp was meant to bound this, but the
// gate that read it was never wired, and its deny path dead-lettered the
// batch (committing past it on Kafka), so a burst would have destroyed
// records the customer still wanted. The cap is now taken before the poll:
// a lane polls at most as many records as the app has budget for, and an app
// out of budget is not polled, so records over the rate stay on the broker
// for a later poll. Nothing is nacked, so no broker delivery count moves.

package sched

import (
	"math"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// triggerRecordRateBucketsMax bounds the limiter's memory; past it, buckets
// idle for a minute are forgotten (they would have refilled anyway).
const triggerRecordRateBucketsMax = 10000

// triggerPollDefaultBatch is what a poller fetches when the trigger sets no
// batch_size_max.
const triggerPollDefaultBatch = 100

// TriggerRecordRateLimiter is a token bucket per app: it refills at the
// plan's TriggerRecordsPerSecondPerApp and holds one second of it. A nil
// limiter takes no budget.
type TriggerRecordRateLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	buckets map[string]*triggerRecordBucket
}

type triggerRecordBucket struct {
	tokens float64
	rate   float64
	last   time.Time
}

// NewTriggerRecordRateLimiter returns a limiter on the wall clock.
func NewTriggerRecordRateLimiter() *TriggerRecordRateLimiter {
	return newTriggerRecordRateLimiter(time.Now)
}

func newTriggerRecordRateLimiter(now func() time.Time) *TriggerRecordRateLimiter {
	return &TriggerRecordRateLimiter{now: now, buckets: map[string]*triggerRecordBucket{}}
}

// Take reserves budget for up to want records of appID's on plan and returns
// how many it reserved: want when the app has the budget, fewer when it has
// some, 0 when it has none or its plan allows no trigger records (an
// unknown plan included).
func (l *TriggerRecordRateLimiter) Take(appID string, plan api.Plan, want int) int {
	if l == nil || want <= 0 {
		return want
	}
	rate := float64(plan.TriggerRecordsPerSecondPerApp())
	if rate <= 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[appID]
	if b == nil {
		if len(l.buckets) >= triggerRecordRateBucketsMax {
			l.forgetIdleLocked(now)
		}
		b = &triggerRecordBucket{tokens: rate, last: now}
		l.buckets[appID] = b
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * rate
		b.last = now
	}
	// A plan change takes effect at once; the bucket never holds more than
	// one second of the current rate.
	b.rate = rate
	b.tokens = math.Min(b.tokens, rate)
	// The epsilon keeps float error in the refill from losing a record.
	n := int(math.Min(float64(want), math.Floor(b.tokens+1e-9)))
	if n < 0 {
		n = 0
	}
	b.tokens -= float64(n)
	return n
}

// Refund returns budget Take reserved and the poll did not use.
func (l *TriggerRecordRateLimiter) Refund(appID string, n int) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if b := l.buckets[appID]; b != nil {
		b.tokens = math.Min(b.tokens+float64(n), b.rate)
	}
}

func (l *TriggerRecordRateLimiter) forgetIdleLocked(now time.Time) {
	for id, b := range l.buckets {
		if now.Sub(b.last) >= time.Minute {
			delete(l.buckets, id)
		}
	}
	if len(l.buckets) >= triggerRecordRateBucketsMax {
		l.buckets = map[string]*triggerRecordBucket{}
	}
}

// WithTriggerRecordRateLimiter caps the records each app's broker triggers
// take per second at its plan's TriggerRecordsPerSecondPerApp. cmd/schedd
// wires it; nil leaves broker polls uncapped.
func (l *Loop) WithTriggerRecordRateLimiter(r *TriggerRecordRateLimiter) *Loop {
	l.recordRate = r
	return l
}

// reserveTriggerRecordRate takes the app's budget for one poll of an
// external broker trigger. It returns the trigger with batch_size_max
// lowered to the budget and a settle func the caller passes the number of
// records the poll returned, so the unused budget comes back. ok is false
// when the app has no budget: the trigger is not polled this time and its
// records stay on the broker. Queue and realtime triggers, exclusive
// bindings and agent targets have admission of their own and pass through.
func (l *Loop) reserveTriggerRecordRate(t sqlc.Trigger, planFor func(string) api.Plan) (sqlc.Trigger, func(int), bool) {
	if l.recordRate == nil || !brokerTriggerLaneEligible(t) {
		return t, func(int) {}, true
	}
	want := int(t.BatchSizeMax)
	if want <= 0 {
		want = triggerPollDefaultBatch
	}
	appID := t.AppID.String()
	// The tick cached the app's plan when it checked ownership; a lane
	// polls every second, so it does not read the account each time.
	plan, cached := l.cachedTriggerAppPlan(appID)
	if !cached {
		plan = api.PlanFree
		if planFor != nil {
			plan = planFor(appID)
		}
	}
	took := l.recordRate.Take(appID, plan, want)
	if took <= 0 {
		return t, func(int) {}, false
	}
	t.BatchSizeMax = int32(took)
	return t, func(polled int) { l.recordRate.Refund(appID, took-polled) }, true
}
