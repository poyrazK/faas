package rollout

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestInvestigationWaitHonorsRetryAfter(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Millisecond)
			defer cancel()
			seconds := int64(1)
			calls := 0
			out, err := Wait(ctx, newTracker(t), func(context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
				calls++
				return api.EnvironmentGitOpsStatusResponse{}, &api.APIError{Problem: api.Problem{Status: status, Code: "rate_limited", RetryAfterSeconds: &seconds}}
			}, 5*time.Millisecond, nil)
			if err != nil || out.Status != "timed_out" {
				t.Fatalf("wait=%+v err=%v", out, err)
			}
			t.Logf("Retry-After=1 second, overall deadline=45ms, read calls=%d", calls)
			if calls != 1 {
				t.Fatalf("made %d status reads during the server's retry-after window", calls)
			}
		})
	}
}

func TestRetryAfterPauseSupportsHeadersAndBoundsLargeDelays(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	deadline := now.Add(5 * time.Second)
	huge, negative := int64(math.MaxInt64), int64(-1)
	for _, tc := range []struct {
		name    string
		problem api.Problem
		want    time.Duration
	}{
		{"overflow", api.Problem{RetryAfterSeconds: &huge}, 5 * time.Second},
		{"negative", api.Problem{RetryAfterSeconds: &negative}, time.Second},
		{"delta header", *(&api.Problem{}).WithHeader("Retry-After", "3"), 3 * time.Second},
		{"date header", *(&api.Problem{}).WithHeader("Retry-After", now.Add(3*time.Second).Format(http.TimeFormat)), 3 * time.Second},
		{"past date", *(&api.Problem{}).WithHeader("Retry-After", now.Add(-time.Second).Format(http.TimeFormat)), time.Second},
		{"invalid", *(&api.Problem{}).WithHeader("Retry-After", "invalid"), time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterPause(tc.problem, time.Second, now, deadline); got != tc.want {
				t.Fatalf("delay=%s, want %s", got, tc.want)
			}
		})
	}
}
