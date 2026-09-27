// adr: 201
package gateway

import (
	"context"
	"fmt"
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
