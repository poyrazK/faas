package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWakeAdmissionPolicyForPlan(t *testing.T) {
	t.Parallel()
	cases := []struct {
		plan     api.Plan
		waiters  int
		wait     time.Duration
		priority int
	}{
		{api.PlanFree, 4, 10 * time.Second, 1},
		{api.PlanHobby, 16, 30 * time.Second, 1},
		{api.PlanPro, 64, 30 * time.Second, 1},
		{api.PlanScale, 128, 30 * time.Second, 1},
	}
	for _, tc := range cases {
		t.Run(string(tc.plan), func(t *testing.T) {
			got := WakeAdmissionPolicyForPlan(tc.plan)
			if got.MaxWaiters != tc.waiters || got.MaxWait != tc.wait || got.Priority != tc.priority {
				t.Fatalf("policy = %+v, want waiters=%d wait=%s priority=%d", got, tc.waiters, tc.wait, tc.priority)
			}
		})
	}
	unknown := WakeAdmissionPolicyForPlan(api.Plan("unknown"))
	if unknown.MaxWaiters != 1 || unknown.MaxWait != 10*time.Second || unknown.Priority != 1 {
		t.Fatalf("unknown policy = %+v, want fail-closed policy", unknown)
	}
}

func TestWakeAdmissionQueueIsFairAcrossPlans(t *testing.T) {
	q := newWakeAdmissionQueue(1, 4, nil)
	block := make(chan struct{})
	started := make(chan struct{})
	firstDone := make(chan struct{})
	var firstErr error
	go func() {
		defer close(firstDone)
		_, _, firstErr = q.Do(context.Background(), "app-hobby", "hobby", WakeAdmissionPolicyForPlan(api.PlanHobby), func(context.Context) error {
			close(started)
			<-block
			return nil
		})
	}()
	<-started

	order := make(chan string, 2)
	var wg sync.WaitGroup
	queue := func(appID, plan string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = q.Do(context.Background(), appID, plan, WakeAdmissionPolicyForPlan(api.Plan(plan)), func(context.Context) error {
				order <- plan
				return nil
			})
		}()
	}
	queue("app-free", string(api.PlanFree))
	waitForAdmissionQueueDepth(t, q, 1)
	queue("app-scale", string(api.PlanScale))
	waitForAdmissionQueueDepth(t, q, 2)

	close(block)
	wg.Wait()
	<-firstDone
	if firstErr != nil {
		t.Fatalf("first admission returned error: %v", firstErr)
	}
	first := <-order
	second := <-order
	if first != string(api.PlanFree) || second != string(api.PlanScale) {
		t.Fatalf("queue order = [%s %s], want FIFO [free scale]", first, second)
	}
}

func TestWakeAdmissionQueueWaitTimeoutDoesNotRunWork(t *testing.T) {
	q := newWakeAdmissionQueue(1, 1, nil)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _, _ = q.Do(context.Background(), "app-1", "hobby", WakeAdmissionPolicyForPlan(api.PlanHobby), func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	called := false
	policy := WakeAdmissionPolicy{MaxWaiters: 1, MaxWait: 15 * time.Millisecond, Priority: 1}
	queued, wait, err := q.Do(context.Background(), "app-2", "free", policy, func(context.Context) error {
		called = true
		return nil
	})
	close(release)
	if !queued || called || wait < policy.MaxWait {
		t.Fatalf("queued=%v called=%v wait=%s, want queued=true, called=false, wait >= %s", queued, called, wait, policy.MaxWait)
	}
	var timeout *WakeAdmissionQueueWaitTimeoutError
	if !errors.As(err, &timeout) || !errors.Is(err, ErrWakeQueueWaitTimeout) {
		t.Fatalf("err = %v, want WakeAdmissionQueueWaitTimeoutError", err)
	}
}

func TestWakeGateWaitWithPolicyUsesPerAppCapAndRetryAfter(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	release := make(chan struct{})
	leaderDone := make(chan error, 1)
	policy := WakeAdmissionPolicy{MaxWaiters: 2, MaxWait: 7 * time.Second, Priority: 1}
	go func() {
		leaderDone <- g.WaitWithPolicy(context.Background(), "app", "acct", policy,
			func() bool { return true },
			func(context.Context) error {
				<-release
				return nil
			}, nil, nil)
	}()
	for g.InflightWaiters("app") < 1 {
		time.Sleep(time.Millisecond)
	}

	followerDone := make(chan error, 1)
	go func() {
		followerDone <- g.WaitWithPolicy(context.Background(), "app", "acct", policy,
			func() bool { return true }, func(context.Context) error { return nil }, nil, nil)
	}()
	for g.InflightWaiters("app") < 2 {
		time.Sleep(time.Millisecond)
	}

	err := g.WaitWithPolicy(context.Background(), "app", "acct", policy,
		func() bool { return true }, func(context.Context) error { return nil }, nil, nil)
	var full *WakeQueueFullError
	if !errors.As(err, &full) || full.Limit != 2 || full.RetryAfter != policy.MaxWait {
		t.Fatalf("err = %v, want per-app cap error with limit=2 retry=%s", err, policy.MaxWait)
	}
	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader returned %v", err)
	}
	if err := <-followerDone; err != nil {
		t.Fatalf("follower returned %v", err)
	}
}

func TestWakeAdmissionMetricsAndRetryAfter(t *testing.T) {
	m := NewMetrics()
	m.ObserveWakeAdmission(string(api.PlanPro), nil, true, 120*time.Millisecond)
	m.SetWakeAdmissionQueueDepth(string(api.PlanPro), 3)

	rec := httptest.NewRecorder()
	writeWakeError(rec, &WakeQueueFullError{Depth: 64, Limit: 64, RetryAfter: 30 * time.Second})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("queue-full response = status=%d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	writeWakeError(rec, &WakeQueueWaitTimeoutError{RetryAfter: 10 * time.Second})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "10" {
		t.Fatalf("timeout response = status=%d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}

	metrics := httptest.NewRecorder()
	m.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	for _, name := range []string{
		"gateway_wake_admission_queue_depth",
		"gateway_wake_admission_total",
		"gateway_wake_admission_wait_seconds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics missing %q:\n%s", name, body)
		}
	}
	if !strings.Contains(body, `gateway_wake_admission_queue_depth{plan="pro"} 3`) {
		t.Errorf("metrics missing pro queue depth:\n%s", body)
	}
}

func waitForAdmissionQueueDepth(t *testing.T, q *wakeAdmissionQueue, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		got := q.waiting.Len()
		q.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("admission queue depth did not reach %d", want)
}
