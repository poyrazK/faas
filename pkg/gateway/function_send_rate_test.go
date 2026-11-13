package gateway

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestFunctionSendRateHoldsTargetPlanRate: sends to one app are held to its
// plan's RateLimitRPS/RateLimitBurst; another app has its own bucket, and the
// bucket refills with time (ADR-20261022105723 amendment 2026-11-13).
func TestFunctionSendRateHoldsTargetPlanRate(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	now := time.Unix(1_800_000_000, 0)
	sends := &functionSendRate{now: func() time.Time { return now }}
	for i := range limits.RateLimitBurst {
		if _, _, ok := sends.allow("app-a", api.PlanHobby); !ok {
			t.Fatalf("send %d within the burst refused", i)
		}
	}
	limit, wait, ok := sends.allow("app-a", api.PlanHobby)
	if ok || limit != limits.RateLimitRPS || wait <= 0 {
		t.Fatalf("send past the burst = ok %t limit %d wait %s; want refused at %d/s", ok, limit, wait, limits.RateLimitRPS)
	}
	if _, _, ok := sends.allow("app-b", api.PlanHobby); !ok {
		t.Fatal("another app's send shared the full bucket")
	}
	now = now.Add(time.Second)
	if _, _, ok := sends.allow("app-a", api.PlanHobby); !ok {
		t.Fatal("bucket did not refill")
	}
	problem := api.ErrFunctionSendRateLimited(limit, wait)
	if problem.Status != 429 || problem.Code != api.CodeFunctionSendRateLimited {
		t.Fatalf("problem = %d %s", problem.Status, problem.Code)
	}
}
