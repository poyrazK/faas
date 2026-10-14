package outbound

// adr: 998 — the staged rollout admits listed accounts, or every account
// when the list is empty or "*", and plan entitlement follows the capability
// registry.

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseAIGatewayRollout(t *testing.T) {
	listed, other := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name       string
		configured []string
		override   string
		allowsAll  bool
		allows     string
		err        bool
	}{
		{name: "unset admits everyone", allowsAll: true},
		{name: "star admits everyone", configured: []string{listed, "*"}, allowsAll: true},
		{name: "configured list", configured: []string{" " + strings.ToUpper(listed) + " "}, allows: listed},
		{name: "override wins", configured: []string{other}, override: listed + ", ", allows: listed},
		{name: "override star", configured: []string{other}, override: "*", allowsAll: true},
		{name: "not a UUID", configured: []string{"acme"}, err: true},
	} {
		rollout, err := ParseAIGatewayRollout(tc.configured, tc.override)
		if (err != nil) != tc.err {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if tc.err {
			continue
		}
		if tc.allowsAll != (rollout == nil) {
			t.Fatalf("%s: rollout = %v", tc.name, rollout)
		}
		if tc.allows != "" && (!rollout.Allows(tc.allows) || rollout.Allows(uuid.NewString()) || rollout.Allows("not-a-uuid")) {
			t.Fatalf("%s: Allows is wrong for %v", tc.name, rollout)
		}
	}
}

func TestAIGatewayEntitlementFollowsTheRegistry(t *testing.T) {
	for plan, want := range map[api.Plan]bool{api.PlanFree: false, api.PlanHobby: true, api.PlanPro: true, api.PlanScale: true, "": false} {
		if got := AIGatewayEntitled(plan); got != want {
			t.Errorf("AIGatewayEntitled(%q) = %v, want %v", plan, got, want)
		}
	}
	handler := &Handler{AIGatewayRollout: AIGatewayRollout{}}
	operator := Integration{OwnerKind: IntegrationOwnerOperator}
	customer := Integration{OwnerKind: IntegrationOwnerCustomer, AccountID: uuid.NewString(), AccountPlan: api.PlanPro}
	if !handler.aiGatewayAdmitsAccount(operator) || handler.aiGatewayAdmitsAccount(customer) {
		t.Fatal("an empty rollout set must refuse customers and admit operator integrations")
	}
	handler.AIGatewayRollout = nil
	customer.AccountPlan = api.PlanFree
	if handler.aiGatewayAdmitsAccount(customer) {
		t.Fatal("a Free account was admitted")
	}
}
