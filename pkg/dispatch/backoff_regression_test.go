package dispatch_test

import (
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dispatch"
)

// adr: 134
func TestRetryPolicyBackoffContinuesUntilConfiguredCap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		policy  dispatch.RetryPolicy
		attempt int
		want    time.Duration
	}{
		{"long delay", dispatch.RetryPolicy{BaseSeconds: 1, MaxSeconds: 3600}, 11, 1024 * time.Second},
		{"long cap", dispatch.RetryPolicy{BaseSeconds: 1, MaxSeconds: 3600}, 25, time.Hour},
		{"fractional base", dispatch.RetryPolicy{BaseSeconds: 0.001, MaxSeconds: 300}, 20, 300 * time.Second},
		{"uncapped", dispatch.RetryPolicy{BaseSeconds: 1}, 12, 2048 * time.Second},
		{"extreme attempt", dispatch.RetryPolicy{BaseSeconds: 1, MaxSeconds: 300}, math.MaxInt, 300 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Backoff(tc.attempt); got != tc.want {
				t.Fatalf("Backoff(%d) = %s, want %s", tc.attempt, got, tc.want)
			}
		})
	}
}

// adr: 134
func TestRetryPolicyBackoffSaturatesDuration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		policy  dispatch.RetryPolicy
		attempt int
	}{
		{"large base", dispatch.RetryPolicy{BaseSeconds: 1e10}, 1},
		{"exponential overflow", dispatch.RetryPolicy{BaseSeconds: 1000}, 25},
		{"float overflow", dispatch.RetryPolicy{BaseSeconds: math.MaxFloat64}, 2},
		{"jitter overflow", dispatch.RetryPolicy{BaseSeconds: 1e100, JitterSeconds: 0.2}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 100 {
				if got := tc.policy.Backoff(tc.attempt); got != time.Duration(math.MaxInt64) {
					t.Fatalf("Backoff(%d) = %s, want saturated duration", tc.attempt, got)
				}
			}
		})
	}
}
