// adr: 201
package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRetryBudgetCapsAggregateAmplificationAndResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := NewRetryBudget(10*time.Second, func() time.Time { return now })
	for i := 0; i < 20; i++ {
		budget.ObserveOriginal(t.Context(), "app-1")
	}
	for i := 0; i < 2; i++ {
		if !budget.AllowRetry(t.Context(), "app-1", 10, 1) {
			t.Fatalf("retry %d denied, want 2 retries for 20 originals at 10%%", i+1)
		}
	}
	if budget.AllowRetry(t.Context(), "app-1", 10, 1) {
		t.Fatal("third retry allowed; aggregate budget should cap amplification at 10%")
	}

	now = now.Add(11 * time.Second)
	budget.ObserveOriginal(t.Context(), "app-1")
	if !budget.AllowRetry(t.Context(), "app-1", 10, 1) {
		t.Fatal("minimum retry allowance was not restored in the next window")
	}
}

func TestRedisRetryBudgetSharesAllowanceAcrossGateways(t *testing.T) {
	server := miniredis.RunT(t)
	url := "redis://" + server.Addr()
	first, err := NewRedisRetryBudget(context.Background(), url, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := NewRedisRetryBudget(context.Background(), url, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if first.BackendID() == "" || first.BackendID() != second.BackendID() || first.BackendID() == server.Addr() {
		t.Fatalf("backend identities differ or expose endpoint: %q, %q", first.BackendID(), second.BackendID())
	}
	first.ObserveOriginal(t.Context(), "app-1")
	second.ObserveOriginal(t.Context(), "app-1")
	if !first.AllowRetry(t.Context(), "app-1", 10, 1) {
		t.Fatal("shared minimum retry should admit one replay")
	}
	if second.AllowRetry(t.Context(), "app-1", 10, 1) {
		t.Fatal("a second gateway received its own minimum allowance")
	}
	server.Close()
	if first.AllowRetry(t.Context(), "app-1", 100, 32) {
		t.Fatal("shared-backend outage must fail closed for retries")
	}
}

func TestRedisRetryBudgetCapsConcurrentGateways(t *testing.T) {
	server := miniredis.RunT(t)
	url := "redis://" + server.Addr()
	first, err := NewRedisRetryBudget(t.Context(), url, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := NewRedisRetryBudget(t.Context(), url, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	for i := 0; i < 20; i++ {
		first.ObserveOriginal(t.Context(), "app-concurrent")
	}

	results := make(chan bool, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(budget *RetryBudget) {
			defer wg.Done()
			results <- budget.AllowRetry(t.Context(), "app-concurrent", 10, 0)
		}([]*RetryBudget{first, second}[i%2])
	}
	wg.Wait()
	close(results)
	allowed := 0
	for result := range results {
		if result {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("concurrent gateways admitted %d retries for 20 originals at 10%%, want 2", allowed)
	}
}

func TestRedisRetryBudgetReportsBackendFailure(t *testing.T) {
	server := miniredis.RunT(t)
	budget, err := NewRedisRetryBudget(t.Context(), "redis://"+server.Addr(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = budget.Close() }()
	metrics := NewMetrics()
	metrics.PreInstantiateTrafficResilience()
	budget.WithObserver(metrics)
	budget.ObserveOriginal(t.Context(), "app-1")
	if !budget.AllowRetry(t.Context(), "app-1", 10, 1) {
		t.Fatal("first retry denied")
	}
	if budget.AllowRetry(t.Context(), "app-1", 10, 1) {
		t.Fatal("second retry admitted")
	}
	server.Close()
	budget.ObserveOriginal(t.Context(), "app-1")
	if budget.AllowRetry(t.Context(), "app-1", 100, 32) {
		t.Fatal("backend outage admitted a retry")
	}
	for _, tc := range []struct{ operation, result string }{
		{"observe", "ok"}, {"observe", "error"},
		{"admit", "allowed"}, {"admit", "denied"}, {"admit", "error"},
	} {
		found := false
		for _, sample := range gatherNamed(t, metrics.Registry(), "gateway_retry_budget_backend_operations_total") {
			labels := map[string]string{}
			for _, label := range sample.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["operation"] == tc.operation && labels["result"] == tc.result {
				found = sample.GetCounter().GetValue() == 1
			}
		}
		if !found {
			t.Fatalf("missing %s/%s backend result", tc.operation, tc.result)
		}
	}
}

func TestRetryBudgetBoundsScopeCardinality(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := NewRetryBudget(time.Minute, func() time.Time { return now })
	for i := 0; i < maxRetryBudgetScopes+1; i++ {
		budget.ObserveOriginal(t.Context(), fmt.Sprintf("app-%d", i))
	}
	if got := len(budget.buckets); got != maxRetryBudgetScopes {
		t.Fatalf("scope count = %d, want cap %d", got, maxRetryBudgetScopes)
	}
	if budget.AllowRetry(t.Context(), fmt.Sprintf("app-%d", maxRetryBudgetScopes), 100, 1) {
		t.Fatal("retry allowed for scope rejected by cardinality cap")
	}
}
