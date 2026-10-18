// adr: 201
// adr: 1133
// adr: 1151
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func TestServiceProxyPreservesFailureWhenBodyReplayFails(t *testing.T) {
	f := newUndeliveredFixture(t, true)
	f.park("instance-1")
	req := serverRequest("payload", 7, "application/json")
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.GetBody = func() (io.ReadCloser, error) { return nil, io.ErrClosedPipe }
	rec := httptest.NewRecorder()
	f.proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "gone\n" {
		t.Errorf("response = %d %q, want original 503 and body", rec.Code, rec.Body.String())
	}
	if got := f.delivered.Load(); got != 0 {
		t.Errorf("delivered = %d, want no attempt with an unreadable body", got)
	}
}

func TestServiceProxyRechecksBudgetAfterReroute(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "remaining budget below minimum"
		if canceled {
			name = "request canceled"
		}
		t.Run(name, func(t *testing.T) {
			f := newUndeliveredFixture(t, true)
			f.park("instance-1")
			req := serverRequest("payload", 7, "application/json")
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			now := time.Unix(1_700_000_000, 0)
			wake := f.proxy.wake
			f.proxy.wake = func(ctx context.Context, appID string) error {
				err := wake(ctx, appID)
				if canceled {
					cancel()
				} else {
					now = now.Add(900 * time.Millisecond)
				}
				return err
			}
			req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
			req = req.WithContext(reqbudget.NewContext(ctx, reqbudget.Budget{
				Started: now, Total: time.Second, Now: func() time.Time { return now },
			}))
			rec := httptest.NewRecorder()
			f.proxy.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "gone\n" {
				t.Errorf("response = %d %q, want original 503 after the retry budget runs low", rec.Code, rec.Body.String())
			}
			if got := f.delivered.Load(); got != 0 {
				t.Errorf("delivered = %d, want no attempt after rerouting exhausted the request", got)
			}
		})
	}
}

// Frozen retries bypass the policy, including its minimum remaining
// threshold, but never the request's actual deadline.
func TestServiceProxyFrozenRetryRespectsDeadline(t *testing.T) {
	for _, tc := range []struct {
		name        string
		afterRefuse time.Duration
		afterThaw   time.Duration
		wantStatus  int
		wantWakes   int32
		wantCalls   int32
	}{
		{name: "expires before thaw", afterRefuse: time.Second, wantStatus: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "expires during thaw", afterThaw: time.Second, wantStatus: http.StatusServiceUnavailable, wantWakes: 1, wantCalls: 1},
		{name: "frozen retry bypasses policy minimum", afterThaw: 900 * time.Millisecond, wantStatus: http.StatusOK, wantWakes: 1, wantCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var frozen atomic.Bool
			var wakes, forwards atomic.Int32
			now := time.Unix(1_700_000_000, 0)
			proxy := frozenServiceProxy(&frozen, &wakes, &forwards, func(r *http.Request) {
				markStaleTargetErr(r.Context(), instanceFrozenStatus())
				now = now.Add(tc.afterRefuse)
			})
			proxy.retryPolicy.MinRemaining = 250 * time.Millisecond
			wake := proxy.wake
			proxy.wake = func(ctx context.Context, appID string) error {
				err := wake(ctx, appID)
				now = now.Add(tc.afterThaw)
				return err
			}
			frozen.Store(true)
			req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/work", nil)
			req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
			req = req.WithContext(reqbudget.NewContext(req.Context(), reqbudget.Budget{
				Started: now, Total: time.Second, Now: func() time.Time { return now },
			}))
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("response = %d %q, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}
			if tc.wantStatus == http.StatusServiceUnavailable && rec.Body.String() != "refused\n" {
				t.Errorf("body = %q, want original frozen refusal", rec.Body.String())
			}
			if wakes.Load() != tc.wantWakes || forwards.Load() != tc.wantCalls {
				t.Errorf("wakes = %d, forwards = %d, want %d, %d", wakes.Load(), forwards.Load(), tc.wantWakes, tc.wantCalls)
			}
		})
	}
}
