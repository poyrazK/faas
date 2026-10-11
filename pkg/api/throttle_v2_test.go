package api

import (
	"strings"
	"testing"
)

// adr: 965 — composite keys and count_statuses validation.
func TestThrottleActionCompositeAndCountStatuses(t *testing.T) {
	ctx := ThrottleValidationContext{PlanMaxRPS: 100, PlanMaxBurst: 200, PlanMaxKeysPerRule: 1000}
	base := func() EdgeRuleThrottleAction {
		return EdgeRuleThrottleAction{RequestsPerSecond: 1, Burst: 5}
	}
	cases := []struct {
		name    string
		mutate  func(*EdgeRuleThrottleAction)
		wantErr string
	}{
		{"composite ok", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByComposite, []string{"ip", "path", "header:X-Tenant"}
		}, ""},
		{"composite with claim", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields, a.JWTClaimName = ThrottleKeyByComposite, []string{"jwt_claim", "path"}, "tenant_id"
		}, ""},
		{"count statuses ok", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.CountStatuses = ThrottleKeyByIP, []int{401, 403}
		}, ""},
		{"count statuses on shared bucket", func(a *EdgeRuleThrottleAction) { a.CountStatuses = []int{429} }, ""},
		{"composite without fields", func(a *EdgeRuleThrottleAction) { a.KeyBy = ThrottleKeyByComposite }, "needs 1..4 key_fields"},
		{"too many fields", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByComposite, []string{"ip", "path", "method", "country", "api_key"}
		}, "needs 1..4"},
		{"unknown field", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByComposite, []string{"body"}
		}, "is not one of"},
		{"bad header name", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByComposite, []string{"header:bad name"}
		}, "is not one of"},
		{"repeated field", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByComposite, []string{"header:X-Tenant", "header:x-tenant"}
		}, "repeated"},
		{"claim field without name", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByComposite, []string{"jwt_claim"}
		}, "needs jwt_claim_name"},
		{"claim name without claim field", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields, a.JWTClaimName = ThrottleKeyByComposite, []string{"ip"}, "tenant_id"
		}, "requires the jwt_claim key field"},
		{"fields without composite", func(a *EdgeRuleThrottleAction) {
			a.KeyBy, a.KeyFields = ThrottleKeyByIP, []string{"path"}
		}, "requires key_by"},
		{"status out of range", func(a *EdgeRuleThrottleAction) { a.CountStatuses = []int{99} }, "100..599"},
		{"status repeated", func(a *EdgeRuleThrottleAction) { a.CountStatuses = []int{401, 401} }, "repeated"},
		{"too many statuses", func(a *EdgeRuleThrottleAction) {
			for c := 400; c < 417; c++ {
				a.CountStatuses = append(a.CountStatuses, c)
			}
		}, "at most 16"},
	}
	for _, tc := range cases {
		a := base()
		tc.mutate(&a)
		prob := a.Validate(ctx)
		switch {
		case tc.wantErr == "" && prob != nil:
			t.Errorf("%s: unexpected error %s", tc.name, prob.Detail)
		case tc.wantErr != "" && (prob == nil || !strings.Contains(prob.Detail, tc.wantErr)):
			t.Errorf("%s: got %v, want error containing %q", tc.name, prob, tc.wantErr)
		}
	}
	if !ThrottleKeyByIsPerConsumer(ThrottleKeyByComposite) {
		t.Fatal("composite must be dimensional")
	}
}
