// adr: 201
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// A replacement response does not exist until another attempt actually
// starts. Cancellation or a body rewind failure must preserve the last error.
func TestRetryPreservesFailureWhenPreparationStops(t *testing.T) {
	for _, tc := range []struct {
		name      string
		backoff   time.Duration
		cancel    bool
		bodyError bool
		expire    bool
		reason    string
	}{
		{name: "canceled before backoff", backoff: time.Second, cancel: true, reason: RetrySkipBudget},
		{name: "canceled without backoff", cancel: true, reason: RetrySkipBudget},
		{name: "body rewind fails", bodyError: true, reason: RetrySkipBodyNotReplay},
		{name: "budget consumed while selecting sibling", expire: true, reason: RetrySkipBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := replayableRequest(http.MethodPut, "payload")
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			now := time.Unix(1_700_000_000, 0)
			ctx = reqbudget.NewContext(ctx, reqbudget.Budget{
				Started: now, Total: time.Second, Now: func() time.Time { return now },
			})
			r = r.WithContext(ctx)
			getBody := r.GetBody
			rewinds := 0
			r.GetBody = func() (io.ReadCloser, error) {
				rewinds++
				if tc.bodyError && rewinds > 1 {
					return nil, io.ErrClosedPipe
				}
				return getBody()
			}
			policy := testPolicy()
			policy.Backoff = tc.backoff
			obs := &recordingObserver{}
			calls := 0
			attempt := func(w http.ResponseWriter, req *http.Request, target Target) {
				calls++
				if calls > 1 {
					okAttempt("unexpected replay")(w, req, target)
					return
				}
				w.Header().Set("Retry-After", "5")
				deadTargetAttempt(w, req, target)
			}
			repick := func() (Target, bool) {
				if tc.cancel {
					cancel()
				}
				if tc.expire {
					now = now.Add(900 * time.Millisecond)
				}
				return Target{InstanceID: "sibling"}, true
			}
			rec := httptest.NewRecorder()
			runWithRetry(rec, r, Target{InstanceID: "dead"}, policy, func(Target) {}, attempt, repick, obs)
			if rec.Code != http.StatusBadGateway || rec.Body.String() != "bad gateway" {
				t.Errorf("response = %d %q, want the original 502 and body", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Retry-After"); got != "5" {
				t.Errorf("Retry-After = %q, want original header 5", got)
			}
			if calls != 1 {
				t.Errorf("attempts = %d, want no replacement attempt", calls)
			}
			if len(obs.exhausted) != 1 || obs.exhausted[0] != tc.reason {
				t.Errorf("exhausted = %v, want [%s]", obs.exhausted, tc.reason)
			}
		})
	}
}

func TestRetryRechecksBudgetAfterBackoff(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
	}{
		{name: "remaining budget falls below minimum"},
		{name: "context deadline expires during backoff", timeout: 500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := replayableRequest(http.MethodGet, "")
				ctx := reqbudget.NewContext(r.Context(), reqbudget.Budget{Started: time.Now(), Total: time.Second})
				if tc.timeout != 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.timeout)
					defer cancel()
				}
				r = r.WithContext(ctx)
				policy := testPolicy()
				policy.Backoff = 900 * time.Millisecond
				rec := httptest.NewRecorder()
				obs := &recordingObserver{}
				calls := 0
				runWithRetry(rec, r, Target{InstanceID: "dead"}, policy, func(Target) {},
					func(w http.ResponseWriter, req *http.Request, target Target) {
						calls++
						deadTargetAttempt(w, req, target)
					}, func() (Target, bool) { return Target{InstanceID: "sibling"}, true }, obs)
				if calls != 1 || rec.Code != http.StatusBadGateway || rec.Body.String() != "bad gateway" {
					t.Errorf("attempts = %d, response = %d %q, want only the original failure", calls, rec.Code, rec.Body.String())
				}
				if len(obs.exhausted) != 1 || obs.exhausted[0] != RetrySkipBudget {
					t.Errorf("exhausted = %v, want [%s]", obs.exhausted, RetrySkipBudget)
				}
			})
		})
	}
}

func TestRetryInitialBodyRewindFailureReturnsError(t *testing.T) {
	r := replayableRequest(http.MethodPut, "payload")
	r.GetBody = func() (io.ReadCloser, error) { return nil, io.ErrClosedPipe }
	rec := httptest.NewRecorder()
	obs := &recordingObserver{}
	runWithRetry(rec, r, Target{InstanceID: "first"}, testPolicy(), func(Target) {},
		func(http.ResponseWriter, *http.Request, Target) { t.Fatal("attempt started with an unreadable body") },
		func() (Target, bool) { t.Fatal("repick called before any attempt"); return Target{}, false }, obs)
	if rec.Code != http.StatusInternalServerError || rec.Body.Len() == 0 {
		t.Errorf("response = %d %q, want an explicit 500 problem", rec.Code, rec.Body.String())
	}
	if len(obs.exhausted) != 1 || obs.exhausted[0] != RetrySkipBodyNotReplay {
		t.Errorf("exhausted = %v, want [%s]", obs.exhausted, RetrySkipBodyNotReplay)
	}
}
