package reqbudget

import (
	"context"
	"testing"
	"time"
)

// Hunt #9 (production-us): under apid's 5 s request budget a project apply
// committed its apps, then failed every build enqueue on the deadline and
// left the apps without a deployment (ADR-093 amendment).
func TestDetachedOutlivesRequestBudgetAndCancellation(t *testing.T) {
	parent, cancelParent, _ := WithRemaining(context.Background(), 5*time.Second, 30*time.Second, "admin", "POST:/v1/projects")
	ctx, cancel := Detached(parent, 10*time.Minute)
	defer cancel()
	cancelParent()

	if err := ctx.Err(); err != nil {
		t.Fatalf("detached context ended with the request budget: %v", err)
	}
	if _, ok := FromContext(ctx); ok {
		t.Fatal("detached context still carries the request budget")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("detached context has no deadline")
	}
	if remaining := time.Until(deadline); remaining < 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("deadline in %v, want about 10m", remaining)
	}
}
