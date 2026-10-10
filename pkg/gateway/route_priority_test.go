package gateway

// adr: 947

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

func enterClass(t *testing.T, m *vmConcurrencyManager, limit int, class routePriority) *concurrencyWaitTicket {
	t.Helper()
	ticket, _, ok, err := m.enterQueueWithPriority(context.Background(), "app", "pro", limit, time.Second, class)
	if err != nil || !ok {
		t.Fatalf("enter %s: ok=%v err=%v", class, ok, err)
	}
	return ticket
}

func queueClasses(m *vmConcurrencyManager) []routePriority {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	var out []routePriority
	if q := m.queues["app"]; q != nil {
		for _, ticket := range q.tickets {
			out = append(out, ticket.class)
		}
	}
	return out
}

func TestRoutePriorityQueueOrdersBehindHead(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	head := enterClass(t, m, 10, routePriorityBulk)
	enterClass(t, m, 10, routePriorityNormal)
	enterClass(t, m, 10, routePriorityBulk)
	critical := enterClass(t, m, 10, routePriorityCritical)
	enterClass(t, m, 10, routePriorityCritical)
	want := []routePriority{routePriorityBulk, routePriorityCritical, routePriorityCritical, routePriorityNormal, routePriorityBulk}
	if got := queueClasses(m); !equalClasses(got, want) {
		t.Fatalf("queue = %v, want %v (the head never moves, FIFO within a class)", got, want)
	}
	if err := head.leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-critical.ready:
	default:
		t.Fatal("the first critical waiter must become the head")
	}
}

func TestRoutePriorityQueueDisplacesLowerClassWhenFull(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	enterClass(t, m, 3, routePriorityNormal)
	olderBulk := enterClass(t, m, 3, routePriorityBulk)
	newerBulk := enterClass(t, m, 3, routePriorityBulk)
	critical := enterClass(t, m, 3, routePriorityCritical)
	if !critical.displacedOther {
		t.Fatal("critical request must enter by displacement")
	}
	if err := newerBulk.wait(context.Background()); !errors.Is(err, errConcurrencyQueueDisplaced) {
		t.Fatalf("newest bulk waiter = %v, want displaced", err)
	}
	select {
	case <-olderBulk.displaced:
		t.Fatal("only the newest lowest-class waiter is displaced")
	default:
	}
	if got := queueClasses(m); !equalClasses(got, []routePriority{routePriorityNormal, routePriorityCritical, routePriorityBulk}) {
		t.Fatalf("queue = %v", got)
	}
	// A normal request cannot displace a critical one, nor another normal.
	if _, _, ok, _ := m.enterQueueWithPriority(context.Background(), "app", "pro", 3, time.Second, routePriorityNormal); !ok {
		t.Fatal("normal must displace the remaining bulk waiter")
	}
	if _, depth, ok, _ := m.enterQueueWithPriority(context.Background(), "app", "pro", 3, time.Second, routePriorityNormal); ok || depth != 3 {
		t.Fatalf("normal into a queue of normal/critical waiters: ok=%v depth=%d, want rejected", ok, depth)
	}
	if err := newerBulk.leave(context.Background()); err != nil {
		t.Fatal("leaving after displacement must be a no-op:", err)
	}
}

func TestRoutePriorityQueueNeverDisplacesTheHead(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	head := enterClass(t, m, 1, routePriorityBulk)
	if _, _, ok, _ := m.enterQueueWithPriority(context.Background(), "app", "pro", 1, time.Second, routePriorityCritical); ok {
		t.Fatal("the head is already acquiring a slot and must not be displaced")
	}
	select {
	case <-head.displaced:
		t.Fatal("head displaced")
	default:
	}
}

func TestRoutePriorityQueueTransfersFleetPermit(t *testing.T) {
	admission := &fakeFleetQueueAdmission{}
	m := newVMConcurrencyManager(nil)
	m.setQueueAdmission(admission)
	enterClass(t, m, 2, routePriorityNormal)
	bulk := enterClass(t, m, 2, routePriorityBulk)
	bulkLease := bulk.leaseID
	critical := enterClass(t, m, 2, routePriorityCritical)
	if critical.leaseID != bulkLease || bulk.leaseID != "" {
		t.Fatalf("critical lease %q, bulk lease %q; want the bulk permit transferred", critical.leaseID, bulk.leaseID)
	}
	if err := bulk.leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	admission.mu.Lock()
	held := len(admission.leases["app"])
	admission.mu.Unlock()
	if held != 2 {
		t.Fatalf("fleet permits held = %d, want 2 (the displaced waiter must not release the transferred permit)", held)
	}
}

func TestRoutePriorityClassification(t *testing.T) {
	rules := []api.RoutePriorityRule{
		{Method: "POST", Path: "/checkout", Class: api.RoutePriorityCritical},
		{Path: "/exports/*", Class: api.RoutePriorityBulk},
		{Method: "GET", Path: "/users/{id}", Class: api.RoutePriorityCritical},
	}
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), nil)
	loads := 0
	h.SetRoutePrioritySource(func(context.Context, string, string) ([]api.RoutePriorityRule, error) {
		loads++
		return rules, nil
	})
	tests := []struct {
		method, path, ua string
		want             routePriority
	}{
		{"POST", "/checkout", "", routePriorityCritical},
		{"GET", "/checkout", "", routePriorityNormal},
		{"DELETE", "/exports/2026", "", routePriorityBulk},
		{"GET", "/users/42", "", routePriorityCritical},
		{"GET", "/blog", "Googlebot/2.1", routePriorityBulk},
		{"POST", "/checkout", "Googlebot/2.1", routePriorityCritical},
		{"GET", "/health", "UptimeRobot/2.0", routePriorityNormal},
	}
	app := App{ID: "app", AccountID: "acct"}
	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, tt.path, nil)
		r.Header.Set("User-Agent", tt.ua)
		if got := h.routePriorityFor(withRoutePriorityRequest(context.Background(), r), app); got != tt.want {
			t.Errorf("%s %s (%q) = %s, want %s", tt.method, tt.path, tt.ua, got, tt.want)
		}
	}
	if loads != 1 {
		t.Fatalf("rule loads = %d, want 1 within the cache TTL", loads)
	}
	if got := h.routePriorityFor(context.Background(), app); got != routePriorityNormal {
		t.Fatalf("no request in context = %s, want normal", got)
	}
}

func TestRoutePriorityFailedLoadIsNoRules(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), nil)
	h.SetRoutePrioritySource(func(context.Context, string, string) ([]api.RoutePriorityRule, error) {
		return nil, errors.New("db down")
	})
	r := httptest.NewRequest("POST", "/checkout", nil)
	if got := h.routePriorityFor(withRoutePriorityRequest(context.Background(), r), App{ID: "app"}); got != routePriorityNormal {
		t.Fatalf("got %s, want normal when rules cannot be read", got)
	}
}

func TestAcquireVMTargetAnswersDisplacementAsFullQueue(t *testing.T) {
	b := &fakeBackend{app: App{ID: "app", Type: AppTypeFunction, Plan: api.PlanFree, MaxQueueDepth: 2}, targets: []Target{{NodeID: "node", InstanceID: "only"}}}
	h := NewHandlerWith(b, NewMetrics(), nil)
	h.SetRoutePrioritySource(func(context.Context, string, string) ([]api.RoutePriorityRule, error) {
		return []api.RoutePriorityRule{{Path: "/checkout", Class: api.RoutePriorityCritical}}, nil
	})
	held, ok := h.vmConcurrency.tryAcquire("only", "free", 1)
	if !ok {
		t.Fatal("failed to occupy the target")
	}
	defer held()
	pick := PickResult{OK: true, Target: b.targets[0]}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	head := enterClassApp(t, h.vmConcurrency, "app", 2, routePriorityNormal)
	defer func() { _ = head.leave(context.Background()) }()
	bulkErr := make(chan error, 1)
	go func() {
		r := httptest.NewRequest("GET", "/blog", nil)
		r.Header.Set("User-Agent", "Googlebot/2.1")
		_, _, _, err := h.acquireVMTarget(withRoutePriorityRequest(ctx, r), b.app, pick, 1, "", "")
		bulkErr <- err
	}()
	deadline := time.Now().Add(time.Second)
	for h.vmConcurrency.queueDepth("app") < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	go func() {
		r := httptest.NewRequest("POST", "/checkout", nil)
		_, _, _, _ = h.acquireVMTarget(withRoutePriorityRequest(ctx, r), b.app, pick, 1, "", "")
	}()
	var full *ConcurrencyQueueFullError
	if err := <-bulkErr; !errors.As(err, &full) {
		t.Fatalf("displaced crawler request = %v, want ConcurrencyQueueFullError", err)
	}
	if got := testutil.ToFloat64(h.metrics.routePriorityQueue.WithLabelValues("bulk", "displaced")); got != 1 {
		t.Fatalf("displaced metric = %v", got)
	}
}

func enterClassApp(t *testing.T, m *vmConcurrencyManager, appID string, limit int, class routePriority) *concurrencyWaitTicket {
	t.Helper()
	ticket, _, ok, err := m.enterQueueWithPriority(context.Background(), appID, "free", limit, time.Second, class)
	if err != nil || !ok {
		t.Fatalf("enter: ok=%v err=%v", ok, err)
	}
	return ticket
}

func equalClasses(a, b []routePriority) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
