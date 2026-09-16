package deploydiff

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestQuota_RAMCap_Hobby — pushing Hobby above 256 MB must fire.
func TestQuota_RAMCap_Hobby(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	v := 1024
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		AppConfig: AppConfigPatch{RAMMB: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanLimitRAM) {
		t.Fatalf("Hobby 1024MB should fire plan_limit_ram; got %+v", got)
	}
}

func TestQuota_FreshAppAtCapBlocksButExistingAppDoesNot(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	cfg := QuotaConfig{Limits: limits, AccountAppCount: limits.DeployedApps, AccountAppCountKnown: true}
	fresh := Quota(api.PlanFree, Baseline{}, Pending{}, cfg)
	b := findBreak(fresh, api.CodePlanLimitApps)
	if b == nil {
		t.Fatalf("fresh app at cap should fire plan_limit_apps; got %+v", fresh)
	}
	if b.Observed.Value != limits.DeployedApps || b.Limit.Value != limits.DeployedApps {
		t.Fatalf("fresh app quota observed/limit = %v/%v, want %d/%d", b.Observed.Value, b.Limit.Value, limits.DeployedApps, limits.DeployedApps)
	}

	existing := Baseline{App: &api.AppResponse{Slug: "existing"}}
	if got := Quota(api.PlanFree, existing, Pending{}, cfg); hasCode(got, api.CodePlanLimitApps) {
		t.Fatalf("existing app preview consumed another slot: %+v", got)
	}
	unknown := Quota(api.PlanFree, Baseline{}, Pending{}, QuotaConfig{Limits: limits})
	if hasCode(unknown, api.CodePlanLimitApps) {
		t.Fatalf("unknown app count was treated as zero/authoritative: %+v", unknown)
	}
}

// adr: 122
func TestQuotaValidatesVCPUAndRolloutParity(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	badVCPU := limits.VCPU + 1
	traffic := 25
	canary := &api.CanaryPresetSpec{Preset: "balanced"}
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		AppConfig:      AppConfigPatch{VCPU: &badVCPU},
		TrafficPercent: &traffic,
		Canary:         canary,
	}, QuotaConfig{Limits: limits})
	for _, code := range []string{api.CodeValidation, api.CodePlanTrafficSplitNotAllowed} {
		if !hasCode(got, code) {
			t.Errorf("missing %s break: %+v", code, got)
		}
	}
}

// TestQuota_StreamingGate_Free — Free plan cannot enable streaming.
func TestQuota_StreamingGate_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := true
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{StreamingEnabled: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanStreamingNotAllowed) {
		t.Fatalf("Free streaming should fire plan_streaming_not_allowed; got %+v", got)
	}
}

func TestQuota_ScalingPolicyGates(t *testing.T) {
	policy := &api.ScalingPolicy{MinInstances: 1, MaxInstances: 2, ScaleOutCooldownS: 5, ScaleInCooldownS: 60}
	got := Quota(api.PlanFree, Baseline{}, Pending{AppConfig: AppConfigPatch{ScalingPolicy: policy}}, QuotaConfig{Limits: api.MustLimitsFor(api.PlanFree)})
	if !hasCode(got, api.CodePlanMinInstancesNotAllowed) || !hasCode(got, api.CodePlanMaxInstancesNotAllowed) {
		t.Fatalf("Free nested scaling policy should fire both plan gates: %+v", got)
	}
}

func TestQuota_ScalingPolicyBounds(t *testing.T) {
	policy := &api.ScalingPolicy{MinInstances: 2, MaxInstances: 1, ScaleOutCooldownS: 0, ScaleInCooldownS: 1}
	got := Quota(api.PlanHobby, Baseline{}, Pending{AppConfig: AppConfigPatch{ScalingPolicy: policy}}, QuotaConfig{Limits: api.MustLimitsFor(api.PlanHobby)})
	for _, code := range []string{api.CodeInvalidMaxInstances, api.CodeInvalidCooldown} {
		if !hasCode(got, code) {
			t.Errorf("missing %s break: %+v", code, got)
		}
	}
}

// TestQuota_CronsPerApp_Free — Free = crons disabled entirely.
func TestQuota_CronsPerApp_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	got := Quota(api.PlanFree, Baseline{}, Pending{
		Crons: []api.CreateCronRequest{{Schedule: "* * * * *", Path: "/x"}},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanCronsNotAllowed) {
		t.Fatalf("Free crons should fire plan_crons_not_allowed; got %+v", got)
	}
}

// TestQuota_CronsPerApp_Hobby — pushing 6 crons on Hobby (cap 5).
func TestQuota_CronsPerApp_Hobby(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	crons := make([]api.CreateCronRequest, 6)
	for i := range crons {
		crons[i] = api.CreateCronRequest{Schedule: "* * * * *", Path: "/p" + string(rune('0'+i))}
	}
	got := Quota(api.PlanHobby, Baseline{}, Pending{Crons: crons}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanCronQuota) {
		t.Fatalf("Hobby 6 crons should fire plan_cron_quota; got %+v", got)
	}
}

// TestQuota_EdgeRulesPerApp_Hobby — pushing 26 rules on Hobby (cap 25).
func TestQuota_EdgeRulesPerApp_Hobby(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	rules := make([]api.CreateEdgeRuleRequest, 26)
	for i := range rules {
		rules[i] = api.CreateEdgeRuleRequest{
			Kind: "route", MatchPath: "/p" + string(rune('0'+i)),
			Action: jsonRaw("{}"),
		}
	}
	got := Quota(api.PlanHobby, Baseline{}, Pending{EdgeRules: rules}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanLimitEdgeRules) {
		t.Fatalf("Hobby 26 edge rules should fire plan_limit_edge_rules; got %+v", got)
	}
}

// TestQuota_EdgeRuleKind_Free — Free cannot use kind=jwt.
func TestQuota_EdgeRuleKind_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	got := Quota(api.PlanFree, Baseline{}, Pending{
		EdgeRules: []api.CreateEdgeRuleRequest{{Kind: "jwt", MatchPath: "/p", Action: jsonRaw("{}")}},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanEdgeRuleKindNotAllowed) {
		t.Fatalf("Free jwt should fire plan_edge_rule_kind_not_allowed; got %+v", got)
	}
}

// TestQuota_EdgeRuleKind_Hobby — Hobby can use kind=jwt.
func TestQuota_EdgeRuleKind_Hobby(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		EdgeRules: []api.CreateEdgeRuleRequest{{Kind: "jwt", MatchPath: "/p", Action: jsonRaw("{}")}},
	}, QuotaConfig{Limits: limits})
	if hasCode(got, api.CodePlanEdgeRuleKindNotAllowed) {
		t.Fatalf("Hobby jwt should not fire; got %+v", got)
	}
}

// TestQuota_MinInstancesAllowed_Free — Free cannot enable floor.
func TestQuota_MinInstancesAllowed_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := 1
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{MinInstances: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanMinInstancesNotAllowed) {
		t.Fatalf("Free min_instances should fire plan_min_instances_not_allowed; got %+v", got)
	}
}

// TestQuota_MinInstancesCap_Hobby — Hobby MaxMinInstances = 1.
func TestQuota_MinInstancesCap_Hobby(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	v := 5
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		AppConfig: AppConfigPatch{MinInstances: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanMinInstancesNotAllowed) {
		t.Fatalf("Hobby min_instances=5 should fire plan_min_instances_not_allowed; got %+v", got)
	}
}

// TestQuota_EnvVarValueByteCap — pushing 4 KB+1 on Hobby (cap 8 KB).
func TestQuota_EnvVarValueByteCap(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	bigValue := make([]byte, 9*1024) // Hobby EnvValueMaxBytes = 8 KB
	for i := range bigValue {
		bigValue[i] = 'x'
	}
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		EnvByScope: map[string][]PendingEnv{
			"default": {{Key: "BIG", Value: string(bigValue)}},
		},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodeEnvVarValueTooLarge) {
		t.Fatalf("9KB env value should fire env_value_too_large; got %+v", got)
	}
}

// TestQuota_EgressAllowlistSizeCap — Pro cap is 16 entries.
func TestQuota_EgressAllowlistSizeCap(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro)
	entries := make([]string, 17)
	for i := range entries {
		entries[i] = "10.0.0.0/24"
	}
	got := Quota(api.PlanPro, Baseline{}, Pending{
		AppConfig: AppConfigPatch{EgressAllowlist: &entries},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, "egress_allowlist_too_long") {
		t.Fatalf("Pro 17 entries should fire egress_allowlist_too_long; got %+v", got)
	}
}

// TestQuota_AllLimitsReadFromLimitsStruct — sanity: the gate never
// inline-limits a constant. Hobby's MaxConcurrency is 2 — pushing
// 3 must fire plan_limit_concurrency with the observed/limit values
// populated from the struct, not literals.
func TestQuota_AllLimitsReadFromLimitsStruct(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	v := 3
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		AppConfig: AppConfigPatch{MaxConcurrency: &v},
	}, QuotaConfig{Limits: limits})
	b := findBreak(got, api.CodePlanLimitConcur)
	if b == nil {
		t.Fatalf("expected plan_limit_concurrency break")
	}
	if b.Observed.Value != 3 {
		t.Fatalf("observed should be 3, got %v", b.Observed.Value)
	}
	if b.Limit.Value != 2 {
		t.Fatalf("limit should be Hobby MaxConcurrency=2, got %v", b.Limit.Value)
	}
}

// helpers ---------------------------------------------------------------

func hasCode(breaks []Break, code string) bool {
	for _, b := range breaks {
		if b.Code == code {
			return true
		}
	}
	return false
}

func findBreak(breaks []Break, code string) *Break {
	for i := range breaks {
		if breaks[i].Code == code {
			return &breaks[i]
		}
	}
	return nil
}

// jsonRaw is a tiny helper to build a json.RawMessage literal in
// tests without dragging encoding/json + the full mkAction closure.
func jsonRaw(s string) (out []byte) {
	// []byte cast so callers can assign to json.RawMessage-shaped fields.
	out = []byte(s)
	return
}
