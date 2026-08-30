package dispatch_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dispatch"
)

// TestRetryPolicy_Zero pins the zero-value contract. Zero() is the
// fallback signal every drain uses to decide "use the producer
// default" — drift here would silently change retry budgets across
// the fleet.
func TestRetryPolicy_Zero(t *testing.T) {
	if !(dispatch.RetryPolicy{}).Zero() {
		t.Fatal("zero-value RetryPolicy must report Zero()=true")
	}
	cases := []dispatch.RetryPolicy{
		{MaxAttempts: 1},
		{BaseSeconds: 1},
		{MaxSeconds: 1},
		{JitterSeconds: 0.01},
	}
	for i, p := range cases {
		if p.Zero() {
			t.Fatalf("case %d: non-zero RetryPolicy reported Zero()=true: %+v", i, p)
		}
	}
}

// TestRetryPolicy_BackoffCurve pins the exponential curve the
// inline scheduler has used since Move 1
// (pkg/sched/dispatch_triggers.go:1009). Drift here would change
// every cron / trigger retry's SLO.
//
// With JitterSeconds=0 the backoff is deterministic; we assert
// the exact value (within float64 round-trip tolerance) for
// attempts 1..5 and the cap-clamp behaviour at attempt 20.
func TestRetryPolicy_BackoffCurve(t *testing.T) {
	p := dispatch.RetryPolicy{MaxAttempts: 25, BaseSeconds: 1, MaxSeconds: 300, JitterSeconds: 0}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{20, 300 * time.Second}, // exp clamps at 9 → 512 → MaxSeconds cap
	}
	for _, c := range cases {
		got := p.Backoff(c.attempt)
		delta := got - c.want
		if delta < 0 {
			delta = -delta
		}
		if delta > time.Microsecond {
			t.Errorf("Backoff(%d) = %v, want %v (±1µs)", c.attempt, got, c.want)
		}
	}
}

// TestRetryPolicy_BackoffAttemptsFloor asserts attempt < 1
// clamps to 1 — matches the inline curve at
// dispatch_triggers.go:1010-1012. Without this clamp a producer
// that increments attempts pre-dispatch would never retry.
func TestRetryPolicy_BackoffAttemptsFloor(t *testing.T) {
	p := dispatch.RetryPolicy{MaxAttempts: 25, BaseSeconds: 1, MaxSeconds: 300, JitterSeconds: 0}
	want := 1 * time.Second
	for _, a := range []int{-5, -1, 0} {
		if got := p.Backoff(a); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", a, got, want)
		}
	}
}

// TestRetryPolicy_BackoffZero pins the "no policy → 0 delay"
// signal. Drains treat 0 as "retry immediately, fall back to
// producer's MaxAttempts budget for the cap".
func TestRetryPolicy_BackoffZero(t *testing.T) {
	if got := (dispatch.RetryPolicy{}).Backoff(3); got != 0 {
		t.Fatalf("Zero().Backoff(3) = %v, want 0", got)
	}
}

// TestRetryPolicy_BackoffJitter pins the ±20% symmetric window
// that the inline curve has used since Move 1. The curve's
// discrete 41-bucket table (0..40 → -1.0..+1.0 in steps of 0.05)
// is what makes dashboards' "retry fired within X" stats stable;
// a uniform float would drift those numbers.
//
// We sample 1000 times and assert every sample is within the
// advertised window. This catches both off-by-one bugs in the
// bucket math and accidental sign flips in the jitter formula.
func TestRetryPolicy_BackoffJitter(t *testing.T) {
	p := dispatch.RetryPolicy{MaxAttempts: 25, BaseSeconds: 1, MaxSeconds: 300, JitterSeconds: 0.2}
	const samples = 1000
	for attempt := 1; attempt <= 5; attempt++ {
		base := math.Pow(2, float64(attempt-1))
		if base > 300 {
			base = 300
		}
		low := base * (1 - 0.2) // ±20% of base (JitterSeconds is a fraction)
		high := base * (1 + 0.2)
		for i := 0; i < samples; i++ {
			got := p.Backoff(attempt).Seconds()
			if got < low-1e-9 || got > high+1e-9 {
				t.Fatalf("attempt=%d sample=%d got=%v out of [%v,%v]",
					attempt, i, got, low, high)
			}
		}
	}
}

// TestRetryPolicy_BackoffExpClamp pins the exp > 9 cap. The
// inline curve at dispatch_triggers.go:1014-1016 has the same
// clamp; removing it would let attempt=64 overflow time.Duration's
// int64 shift.
func TestRetryPolicy_BackoffExpClamp(t *testing.T) {
	p := dispatch.RetryPolicy{MaxAttempts: 25, BaseSeconds: 1, MaxSeconds: 300, JitterSeconds: 0}
	for _, a := range []int{10, 12, 20, 100, 1000} {
		got := p.Backoff(a).Seconds()
		if got < 299.9 || got > 300.1 {
			t.Errorf("Backoff(%d) = %vs, want ~300s (exp clamp)", a, got)
		}
	}
}

// TestRetryPolicy_BackoffJitterMean asserts the curve is centred
// on the base, not biased up or down. A biased jitter
// (e.g. (0.8 + 0.01*rand%41) without the /20 scaling) would
// silently inflate retry SLOs across the fleet.
func TestRetryPolicy_BackoffJitterMean(t *testing.T) {
	p := dispatch.RetryPolicy{MaxAttempts: 25, BaseSeconds: 1, MaxSeconds: 300, JitterSeconds: 0.2}
	const samples = 5000
	var sum float64
	for i := 0; i < samples; i++ {
		sum += p.Backoff(3).Seconds() // base = 4
	}
	mean := sum / samples
	if math.Abs(mean-4.0) > 0.05 {
		t.Errorf("jitter mean = %v, want ~4.0 (±0.05)", mean)
	}
}

// TestDeadlinePolicy_Zero pins the unset signal. Drains skip the
// deadline path when Zero()=true.
func TestDeadlinePolicy_Zero(t *testing.T) {
	if !(dispatch.DeadlinePolicy{}).Zero() {
		t.Fatal("zero-value DeadlinePolicy must report Zero()=true")
	}
}

// TestLease_Expired pins the helper every drain uses to decide
// between renew-and-continue and requeue-the-row.
func TestLease_Expired(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		l    dispatch.Lease
		want bool
	}{
		{"empty token → expired", dispatch.Lease{Token: ""}, true},
		{"future expires_at → not expired", dispatch.Lease{Token: "t", ExpiresAt: now.Add(time.Minute)}, false},
		{"past expires_at → expired", dispatch.Lease{Token: "t", ExpiresAt: now.Add(-time.Second)}, true},
		{"zero expires_at + token → not expired", dispatch.Lease{Token: "t"}, false},
	}
	for _, c := range cases {
		if got := c.l.Expired(now); got != c.want {
			t.Errorf("%s: Expired()=%v, want %v", c.name, got, c.want)
		}
	}
}

// jobImpl is the synthetic adapter that proves dispatch.Job
// compiles. PR-B / PR-C will replace it with the real
// *state.Invocation and *state.TriggerRecord implementations;
// until then it pins the interface shape so a typo in a method
// signature lands in code review, not at runtime.
type jobImpl struct {
	k                          dispatch.JobKind
	id, app, acct, origin, err string
	attempts                   int
	rp                         dispatch.RetryPolicy
	dl                         dispatch.DeadlinePolicy
	snap                       []byte
}

func (j *jobImpl) Kind() dispatch.JobKind { return j.k }
func (j *jobImpl) ID() string             { return j.id }
func (j *jobImpl) AppID() string          { return j.app }
func (j *jobImpl) AccountID() string      { return j.acct }
func (j *jobImpl) Origin() string         { return j.origin }
func (j *jobImpl) RetryPolicy() dispatch.RetryPolicy {
	return j.rp
}
func (j *jobImpl) Deadline() dispatch.DeadlinePolicy { return j.dl }
func (j *jobImpl) CurrentAttempts() int              { return j.attempts }
func (j *jobImpl) ErrorText() string                 { return j.err }
func (j *jobImpl) Snapshot() []byte                  { return j.snap }

// TestJobInterface_Compile is the build-time assertion: jobImpl
// must satisfy dispatch.Job. If a future change to the interface
// drops a method or renames one, this test fails to compile
// instead of failing at runtime in a downstream drain.
func TestJobInterface_Compile(t *testing.T) {
	var _ dispatch.Job = (*jobImpl)(nil)
}

// stubLeaser is the synthetic adapter for dispatch.Leaser[T]. PR-D
// wires the real *lease.Manager[T]; today the stub pins the
// generic method shape so the constraint compiles.
type stubLeaser struct{}

func (s *stubLeaser) Acquire(_ context.Context, _ string, _ string, _ time.Duration) (dispatch.Lease, error) {
	return dispatch.Lease{}, nil
}
func (s *stubLeaser) Renew(_ context.Context, _ string, _ string, _ time.Duration) (dispatch.Lease, error) {
	return dispatch.Lease{}, nil
}
func (s *stubLeaser) Release(_ context.Context, _ string, _ string) error { return nil }

// TestLeaserInterface_Compile is the build-time assertion for the
// generic Leaser[T] shape. If a future change to the interface
// drops a method or changes its signature, this test fails to
// compile.
func TestLeaserInterface_Compile(t *testing.T) {
	var _ dispatch.Leaser[string] = (*stubLeaser)(nil)
}
