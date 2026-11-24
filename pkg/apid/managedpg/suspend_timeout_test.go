package managedpg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeSuspendTimeouts struct {
	called bool
	got    *int64
	err    error
}

func (f *fakeSuspendTimeouts) SetSuspendTimeout(_ context.Context, _, id string, seconds *int64) (managedpostgres.SuspendTimeout, error) {
	f.called, f.got = true, seconds
	out := managedpostgres.SuspendTimeout{DatabaseID: id, Seconds: 120}
	if seconds != nil {
		out.Seconds, out.Custom = *seconds, true
	}
	return out, f.err
}

func TestSuspendTimeoutPut(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		err        error
		code       int
		problem    string
		called     bool
		want       *int64
	}{
		{name: "own value", body: `{"seconds":300}`, code: http.StatusOK, called: true, want: ptr(int64(300))},
		{name: "null is the default", body: `{"seconds":null}`, code: http.StatusOK, called: true},
		{name: "missing seconds is refused, not a reset", body: `{}`, code: http.StatusUnprocessableEntity, problem: "invalid_suspend_timeout"},
		{name: "unknown field", body: `{"second":300}`, code: http.StatusUnprocessableEntity, problem: "invalid_suspend_timeout"},
		{name: "not a number", body: `{"seconds":"5m"}`, code: http.StatusUnprocessableEntity, problem: "invalid_suspend_timeout"},
		{name: "below the range", body: `{"seconds":30}`, code: http.StatusUnprocessableEntity, problem: "invalid_suspend_timeout"},
		{name: "above the range", body: `{"seconds":3601}`, code: http.StatusUnprocessableEntity, problem: "invalid_suspend_timeout"},
		{name: "always-on", body: `{"seconds":300}`, err: managedpostgres.ErrSuspendTimeoutAlwaysOn,
			code: http.StatusConflict, problem: "suspend_timeout_always_on", called: true, want: ptr(int64(300))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeSuspendTimeouts{err: tc.err}
			st := SuspendTimeouts{Service: service,
				Problem:       func(w http.ResponseWriter, err error) { w.WriteHeader(http.StatusTeapot) },
				NotConfigured: func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }}
			r := httptest.NewRequest(http.MethodPut, "/v1/postgres/databases/orders/suspend-timeout", strings.NewReader(tc.body))
			r.SetPathValue("id", "orders")
			w := httptest.NewRecorder()
			st.Put(w, r, state.Account{ID: "acct-1", Plan: api.PlanPro})
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
			if tc.problem != "" {
				var problem api.Problem
				if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil || problem.Code != tc.problem {
					t.Fatalf("problem = %s, want %s", w.Body.String(), tc.problem)
				}
			}
			if service.called != tc.called {
				t.Fatalf("service called = %v, want %v", service.called, tc.called)
			}
			if (service.got == nil) != (tc.want == nil) || service.got != nil && *service.got != *tc.want {
				t.Fatalf("service got %v, want %v", service.got, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
