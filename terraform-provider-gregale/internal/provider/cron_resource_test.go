package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestCronResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newCronResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("cron resource schema diagnostics: %v", diags)
	}
}

func TestCronFailureRulesJSONValidationAndCanonicalization(t *testing.T) {
	valid, diags := cronFailureRulesFromModel(types.StringValue(`{"version":1,"rules":[],"unmatched_failure":"retry","uncertain_outcome":"hold"}`))
	if diags.HasError() {
		t.Fatalf("valid failure rules diagnostics: %v", diags)
	}
	if got := string(valid); got != `{"rules":[],"uncertain_outcome":"hold","unmatched_failure":"retry","version":1}` {
		t.Fatalf("canonical JSON = %s", got)
	}
	_, diags = cronFailureRulesFromModel(types.StringValue(`[]`))
	if !diags.HasError() {
		t.Fatal("expected a JSON array to be rejected")
	}
}

// Removing a schedule policy or failure rules from the configuration plans
// null, so the update clears them; a configured value and a resource being
// created keep the plan as it is (crons audit 2026-11-03).
func TestCronClearWhenUnconfigured(t *testing.T) {
	ctx := context.Background()
	present := tfsdk.State{Raw: tftypes.NewValue(tftypes.String, "present")}
	rules := types.StringValue(`{"version":1}`)
	for _, tc := range []struct {
		name      string
		config    types.String
		state     tfsdk.State
		wantClear bool
	}{
		{"removed from the configuration", types.StringNull(), present, true},
		{"still configured", rules, present, false},
		{"being created", types.StringNull(), tfsdk.State{Raw: tftypes.NewValue(tftypes.String, nil)}, false},
	} {
		req := planmodifier.StringRequest{ConfigValue: tc.config, StateValue: rules, PlanValue: rules, State: tc.state}
		resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
		clearWhenUnconfigured{}.PlanModifyString(ctx, req, resp)
		if resp.PlanValue.IsNull() != tc.wantClear {
			t.Errorf("%s: planned %v, want cleared=%v", tc.name, resp.PlanValue, tc.wantClear)
		}
	}
	policy, diags := cronSchedulePolicyValue(&cronSchedulePolicy{Version: 1, Overlap: "skip", MissedRuns: "skip"})
	if diags.HasError() {
		t.Fatal(diags)
	}
	objReq := planmodifier.ObjectRequest{ConfigValue: types.ObjectNull(cronSchedulePolicyAttrTypes), StateValue: policy, PlanValue: policy, State: present}
	objResp := &planmodifier.ObjectResponse{PlanValue: policy}
	clearWhenUnconfigured{}.PlanModifyObject(ctx, objReq, objResp)
	if !objResp.PlanValue.IsNull() {
		t.Fatalf("removed schedule policy planned %v, want null", objResp.PlanValue)
	}
}
