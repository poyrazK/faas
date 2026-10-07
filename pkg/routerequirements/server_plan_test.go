package routerequirements

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestServerPlanCanonicalArtifactAndAPIActionShape(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id","max_rps":2},"budget":{"max_ms":1000}}`)
	context := requirementContext()
	options := PlanOptions{PlanName: "hobby", ThrottleBurst: 5}
	plan, err := BuildServerPlan(config, context, options)
	if err != nil || plan.Version != 2 || plan.Requirements == nil {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	body, _ := json.Marshal(plan)
	var decoded PolicyPlan
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifact(decoded, context.App.Slug); err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(change.Create.Action, &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw[change.Kind]; ok {
			t.Fatal("generated API action was wrapped in its storage envelope")
		}
		switch change.Kind {
		case "throttle":
			var action api.EdgeRuleThrottleAction
			if err := json.Unmarshal(change.Create.Action, &action); err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(api.PlanHobby)
			if p := action.Validate(api.ThrottleValidationContext{PlanMaxRPS: float64(limits.RateLimitRPS), PlanMaxBurst: limits.RateLimitBurst, PlanMaxKeysPerRule: limits.ThrottleMaxKeysPerRule}); p != nil {
				t.Fatal(p)
			}
		case "budget":
			var action api.EdgeRuleBudgetAction
			if err := json.Unmarshal(change.Create.Action, &action); err != nil {
				t.Fatal(err)
			}
			if p := action.Validate(); p != nil {
				t.Fatal(p)
			}
		}
	}
	decoded.ThrottleBurst++
	if err := ValidateArtifact(decoded, context.App.Slug); err == nil {
		t.Fatal("edited options accepted")
	}
	decoded = plan
	decoded.Version = 1
	if err := ValidateArtifact(decoded, context.App.Slug); err == nil {
		t.Fatal("legacy artifact accepted")
	}
	config.Routes[0].Method = "post"
	repeated, err := BuildServerPlan(config, context, options)
	if err != nil || repeated.SHA256 != plan.SHA256 {
		t.Fatal("method normalization changed plan fingerprint")
	}
}
