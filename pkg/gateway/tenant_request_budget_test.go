package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

type testTenantBudgetStore struct {
	calls atomic.Int64
	fail  atomic.Bool
}

func (s *testTenantBudgetStore) AdmitTenantRequest(_ context.Context, _, _ string) (TenantRequestBudgetDecision, error) {
	s.calls.Add(1)
	if s.fail.Load() {
		return TenantRequestBudgetDecision{}, errors.New("counter unavailable")
	}
	if s.calls.Load() > 1 {
		return TenantRequestBudgetDecision{Scope: "minute", Limit: 1, Observed: 1, RetryAfterSeconds: 37}, nil
	}
	return TenantRequestBudgetDecision{Allowed: true}, nil
}

// adr: 240
func TestTenantRequestBudgetRejectsBeforeWakeWithoutBilling(t *testing.T) {
	accountID, appID, tenantID, surfaceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	var forwarded atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{app: App{ID: appID, AccountID: accountID, Plan: api.PlanPro,
		ConsumerAuthMode: api.ConsumerAuthModeOptional, RoutedSurfaceID: surfaceID, PlatformTenantID: tenantID},
		host: "budget-customer.example", upstream: upstream.Listener.Addr().String()}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	q, err := usageoutbox.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	budget := &testTenantBudgetStore{}
	h := NewHandlerWith(backend, NewMetrics(), nil).WithTenantRequestBudgetStore(budget)
	h.usageOutbox = q
	h.requestTelemetry = makeTestRecorder()
	request := func() *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://budget-customer.example/work", nil))
		return w
	}
	if w := request(); w.Code != http.StatusOK {
		t.Fatalf("first request status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request(); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "37" ||
		w.Header().Get("x-faas-rate-limit-scope") != "platform-tenant" {
		t.Fatalf("budget denial status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
	budget.fail.Store(true)
	if w := request(); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("counter outage status=%d body=%s", w.Code, w.Body.String())
	}
	if got := forwarded.Load(); got != 1 {
		t.Fatalf("forwarded %d requests, want only the admitted one", got)
	}
	if got := q.Stats().PendingRecords; got != 1 {
		t.Fatalf("financial outbox has %d records, want only the admitted request", got)
	}
	rows := h.requestTelemetry.DrainBatch(3)
	if len(rows) != 3 || !rows[1].UsageOutboxed || !rows[2].UsageOutboxed || rows[1].Status != 429 || rows[2].Status != 503 {
		t.Fatalf("request evidence did not suppress denied financial fallback: %+v", rows)
	}
}

func TestTenantRequestBudgetSkipsUnattributedTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{app: App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro},
		host: "plain.example", upstream: upstream.Listener.Addr().String()}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	budget := &testTenantBudgetStore{}
	budget.fail.Store(true)
	h := NewHandlerWith(backend, NewMetrics(), nil).WithTenantRequestBudgetStore(budget)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://plain.example/work", nil))
	if w.Code != http.StatusOK || budget.calls.Load() != 0 {
		t.Fatalf("unattributed request status=%d budget calls=%d", w.Code, budget.calls.Load())
	}
}
