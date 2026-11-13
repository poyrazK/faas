package outbound

// adr: 20261027132936 — amendment 2: the account-wide AI call rate.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAccountAIRateIsSharedAcrossIntegrations(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok || limits.OutboundAIAccountRatePerSecondMax <= 0 {
		t.Fatalf("free plan has no account AI rate: %+v", limits.OutboundAIAccountRatePerSecondMax)
	}
	burst := 2 * limits.OutboundAIAccountRatePerSecondMax
	var rates aiAccountRates
	now := time.Unix(1_800_000_000, 0)
	for i := range burst {
		if !rates.allow("acct", api.PlanFree, now) {
			t.Fatalf("call %d of the burst refused", i)
		}
	}
	if rates.allow("acct", api.PlanFree, now) {
		t.Fatal("a call past the burst was allowed")
	}
	if !rates.allow("other", api.PlanFree, now) {
		t.Fatal("another account shares the bucket")
	}
	if !rates.allow("acct", api.PlanFree, now.Add(time.Second/time.Duration(limits.OutboundAIAccountRatePerSecondMax))) {
		t.Fatal("the bucket does not refill at the plan rate")
	}
	if !rates.allow("", api.PlanFree, now) || !rates.allow("acct", api.Plan("unknown"), now) {
		t.Fatal("a call without an account or plan ceiling was refused")
	}
}
