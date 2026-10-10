package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeConsumerPlanStore struct {
	policy      ConsumerPlanPolicy
	policyErr   error
	decision    ConsumerPlanDecision
	policyCalls int
	admitUnits  []int64
}

func (f *fakeConsumerPlanStore) ConsumerPlanPolicy(context.Context, string, string, string) (ConsumerPlanPolicy, error) {
	f.policyCalls++
	return f.policy, f.policyErr
}

func (f *fakeConsumerPlanStore) AdmitConsumerPlanRequest(_ context.Context, _, _ string, _ ConsumerPlanPolicy, units int64) (ConsumerPlanDecision, error) {
	f.admitUnits = append(f.admitUnits, units)
	return f.decision, nil
}

func planRequest(route string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/generate", nil)
	r = r.WithContext(withAuthenticated(r.Context(), Authenticated{ConsumerID: "consumer-1"}))
	return withBillingRoute(r, route)
}

// adr: 938
func TestEnforceConsumerPlan(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	t.Run("unlimited plans skip admission and cache the policy", func(t *testing.T) {
		store := &fakeConsumerPlanStore{}
		h := (&Handler{}).WithConsumerPlanStore(store)
		for range 3 {
			if !h.enforceConsumerPlan(httptest.NewRecorder(), planRequest("GET /items"), &statusRecorder{}, app, false) {
				t.Fatal("unlimited plan denied")
			}
		}
		if store.policyCalls != 1 || len(store.admitUnits) != 0 {
			t.Fatalf("policy calls = %d, admissions = %d; want 1 cached lookup and no admission", store.policyCalls, len(store.admitUnits))
		}
	})
	t.Run("monthly caps count weighted units", func(t *testing.T) {
		store := &fakeConsumerPlanStore{policy: ConsumerPlanPolicy{MaxUnitsPerMonth: 100, RouteWeights: map[string]int64{"POST /generate": 20}},
			decision: ConsumerPlanDecision{Allowed: true}}
		h := (&Handler{}).WithConsumerPlanStore(store)
		h.enforceConsumerPlan(httptest.NewRecorder(), planRequest("POST /generate"), &statusRecorder{}, app, false)
		h.enforceConsumerPlan(httptest.NewRecorder(), planRequest("GET /items"), &statusRecorder{}, app, false)
		if len(store.admitUnits) != 2 || store.admitUnits[0] != 20 || store.admitUnits[1] != 1 {
			t.Fatalf("admitted units = %v, want [20 1]", store.admitUnits)
		}
	})
	t.Run("denials return 429 and are not billed", func(t *testing.T) {
		store := &fakeConsumerPlanStore{policy: ConsumerPlanPolicy{MaxRequestsPerMinute: 1},
			decision: ConsumerPlanDecision{Scope: "minute", Limit: 1, Observed: 1, RetryAfterSeconds: 30}}
		h := (&Handler{}).WithConsumerPlanStore(store)
		w, r := httptest.NewRecorder(), planRequest("GET /items")
		if h.enforceConsumerPlan(w, r, &statusRecorder{}, app, false) {
			t.Fatal("over-limit request admitted")
		}
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "30" ||
			w.Header().Get("x-faas-rate-limit-scope") != "consumer-plan-minute" || r.Context().Value(suppressFinancialUsageKey{}) != true {
			t.Fatalf("code=%d headers=%v suppressed=%v", w.Code, w.Header(), r.Context().Value(suppressFinancialUsageKey{}))
		}
	})
	t.Run("unverifiable admission fails closed", func(t *testing.T) {
		store := &fakeConsumerPlanStore{policyErr: errors.New("db down")}
		h := (&Handler{}).WithConsumerPlanStore(store)
		w := httptest.NewRecorder()
		if h.enforceConsumerPlan(w, planRequest("GET /items"), &statusRecorder{}, app, false) || w.Code != http.StatusServiceUnavailable {
			t.Fatalf("code=%d, want 503", w.Code)
		}
	})
	t.Run("anonymous traffic and smoke probes are not plan-limited", func(t *testing.T) {
		store := &fakeConsumerPlanStore{policyErr: errors.New("must not be called")}
		h := (&Handler{}).WithConsumerPlanStore(store)
		anonymous := httptest.NewRequest(http.MethodGet, "/", nil)
		if !h.enforceConsumerPlan(httptest.NewRecorder(), anonymous, &statusRecorder{}, app, false) ||
			!h.enforceConsumerPlan(httptest.NewRecorder(), planRequest("GET /items"), &statusRecorder{}, app, true) {
			t.Fatal("anonymous or smoke traffic was plan-limited")
		}
	})
}
