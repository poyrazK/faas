package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Only failed authentications were limited, so one valid key could drive any
// endpoint at any rate. Each account now has a plan budget of requests a
// minute (bug hunt 2026-10-28 H42).
func TestAPIRequestBudgetRefusesPastThePlanRate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := apidserver.NewAPIRequestBudgetAt(func() time.Time { return now })
	capacity := api.MustLimitsFor(api.PlanFree).APIRequestsPerMinute
	if capacity <= 0 {
		t.Fatal("the Free plan has no API request budget")
	}
	for i := range capacity {
		if _, ok := b.Take("acct-1", capacity); !ok {
			t.Fatalf("request %d refused inside the budget", i)
		}
	}
	wait, ok := b.Take("acct-1", capacity)
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("request past the budget: ok=%v wait=%s, want a refusal with a sub-second wait", ok, wait)
	}
	if _, ok := b.Take("acct-2", capacity); !ok {
		t.Fatal("another account shares the budget")
	}
	now = now.Add(time.Second)
	if _, ok := b.Take("acct-1", capacity); !ok {
		t.Fatal("the budget did not refill")
	}
	if _, ok := b.Take("acct-3", 0); !ok {
		t.Fatal("a plan without a budget was limited")
	}
}
