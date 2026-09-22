// adr: 201
package gateway

import (
	"fmt"
	"testing"
	"time"
)

func TestRetryBudgetCapsAggregateAmplificationAndResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := NewRetryBudget(10*time.Second, func() time.Time { return now })
	for i := 0; i < 20; i++ {
		budget.ObserveOriginal("app-1")
	}
	for i := 0; i < 2; i++ {
		if !budget.AllowRetry("app-1", 10, 1) {
			t.Fatalf("retry %d denied, want 2 retries for 20 originals at 10%%", i+1)
		}
	}
	if budget.AllowRetry("app-1", 10, 1) {
		t.Fatal("third retry allowed; aggregate budget should cap amplification at 10%")
	}

	now = now.Add(11 * time.Second)
	budget.ObserveOriginal("app-1")
	if !budget.AllowRetry("app-1", 10, 1) {
		t.Fatal("minimum retry allowance was not restored in the next window")
	}
}

func TestRetryBudgetBoundsScopeCardinality(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := NewRetryBudget(time.Minute, func() time.Time { return now })
	for i := 0; i < maxRetryBudgetScopes+1; i++ {
		budget.ObserveOriginal(fmt.Sprintf("app-%d", i))
	}
	if got := len(budget.buckets); got != maxRetryBudgetScopes {
		t.Fatalf("scope count = %d, want cap %d", got, maxRetryBudgetScopes)
	}
	if budget.AllowRetry(fmt.Sprintf("app-%d", maxRetryBudgetScopes), 100, 1) {
		t.Fatal("retry allowed for scope rejected by cardinality cap")
	}
}
