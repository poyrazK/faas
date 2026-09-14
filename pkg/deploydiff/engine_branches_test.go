// Whitebox tests pinning the unfilled branches in
// pkg/deploydiff/engine.go + quota.go. Pre-existing tests in
// engine_test.go cover the happy paths; this file fills the gaps
// that remain after the render_text_test + render_json_test +
// diff_helpers_test sweep:
//   - diffAppConfig: require_signed / eviction_priority /
//     autoscale_target_rps / autoscale_target_cpu_pct /
//     egress_allowlist modify branches (only RAMMB +
//     MaxConcurrency + MinInstances + IdleTimeoutS + booleans
//     + StreamingEnabled were pre-existing).
//   - diffCrons: enabled-flip "modify" branch (only add/remove
//     were pre-existing).
//   - diffEdgeRules: methodsChanged branch (actionChanged already
//     pinned via existing TestCompute_EdgeRules).
//   - detectSchemaBreak (text-only): entrypoint-change path +
//     EnvSecrets change path (only env-key change was pre-existing).
//   - schemaBreakReason: all 4 switch arms + default.
//   - stringMapsEqual: b[k] != v not-equal branch (equal branch
//     already exercised indirectly).
//   - Quota: WebSocket/WarmSnapshot/RequireAuthn/EgressAllowlist
//     not-allowed + AutoscaleTargetRPS/CP + CronsPerAccount
//     quota-gate branches.

package deploydiff

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

// --- diffAppConfig untested app-config fields ------------------------

func TestCompute_AppConfig_RequireSignedModify(t *testing.T) {
	base := &api.AppResponse{
		Slug: "api", RAMMB: 256, RequireSigned: false,
	}
	baseline := Baseline{App: base}
	v := true
	got := Compute("api", "", baseline, Pending{
		AppConfig: AppConfigPatch{RequireSigned: &v},
	})
	if !hasChangeField(got, "require_signed", ChangeModify) {
		t.Fatalf("require_signed flip should emit a Modify Change; got %+v", got.Changes)
	}
}

func TestCompute_AppConfig_EvictionPriorityModify(t *testing.T) {
	base := &api.AppResponse{
		Slug: "api", RAMMB: 256, EvictionPriority: "low",
	}
	baseline := Baseline{App: base}
	s := "high"
	got := Compute("api", "", baseline, Pending{
		AppConfig: AppConfigPatch{EvictionPriority: &s},
	})
	if !hasChangeField(got, "eviction_priority", ChangeModify) {
		t.Fatalf("eviction_priority change should emit a Modify Change; got %+v", got.Changes)
	}
}

func TestCompute_AppConfig_AutoscaleTargetRPSModify(t *testing.T) {
	base := &api.AppResponse{
		Slug: "api", RAMMB: 256, AutoscaleTargetRPS: 10,
	}
	baseline := Baseline{App: base}
	v := 25
	got := Compute("api", "", baseline, Pending{
		AppConfig: AppConfigPatch{AutoscaleTargetRPS: &v},
	})
	if !hasChangeField(got, "autoscale_target_rps", ChangeModify) {
		t.Fatalf("autoscale_target_rps change should emit a Modify Change; got %+v", got.Changes)
	}
}

func TestCompute_AppConfig_AutoscaleTargetCPModify(t *testing.T) {
	// Per the engine: pending field is AutoscaleTargetCP; baseline
	// field is AutoscaleTargetCPUPct (renamed on the wire form to
	// the customer-facing field "autoscale_target_cpu_pct").
	base := &api.AppResponse{
		Slug: "api", RAMMB: 256, AutoscaleTargetCPUPct: 50,
	}
	baseline := Baseline{App: base}
	v := 75
	got := Compute("api", "", baseline, Pending{
		AppConfig: AppConfigPatch{AutoscaleTargetCP: &v},
	})
	if !hasChangeField(got, "autoscale_target_cpu_pct", ChangeModify) {
		t.Fatalf("autoscale_target_cpu_pct change should emit a Modify Change; got %+v", got.Changes)
	}
}

func TestCompute_AppConfig_EgressAllowlistModify(t *testing.T) {
	// EgressAllowlist diff uses stringSliceEqual (set equality), not
	// a single-value compare — drive the modify branch by flipping
	// the contents of the slice.
	base := &api.AppResponse{
		Slug: "api", RAMMB: 256,
		EgressAllowlist: []string{"api.example.com", "other.example.com"},
	}
	baseline := Baseline{App: base}
	patch := []string{"api.example.com", "new.example.com"}
	got := Compute("api", "", baseline, Pending{
		AppConfig: AppConfigPatch{EgressAllowlist: &patch},
	})
	if !hasChangeField(got, "egress_allowlist", ChangeModify) {
		t.Fatalf("egress_allowlist set change should emit a Modify Change; got %+v", got.Changes)
	}
}

// --- diffCrons: enabled-flip modify ----------------------------------

func TestCompute_Crons_EnabledFlipModify(t *testing.T) {
	// Existing cron with Enabled=true; pending flips to false.
	// Only modifies the .enabled sub-field of the cron row.
	baseline := Baseline{
		Crons: []api.CronResponse{
			{ID: "c1", Schedule: "* * * * *", Path: "/cron", Enabled: true},
		},
	}
	en := false
	pending := Pending{
		Crons: []api.CreateCronRequest{
			{Schedule: "* * * * *", Path: "/cron", Enabled: &en},
		},
	}
	got := Compute("api", "", baseline, pending)
	wantField := "cron[* * * * * /cron].enabled"
	if !hasChangeField(got, wantField, ChangeModify) {
		t.Fatalf("enabled flip should emit %s Change; got %+v", wantField, got.Changes)
	}
}

func TestCompute_Crons_SchedulingOptionsModify(t *testing.T) {
	baseline := Baseline{Crons: []api.CronResponse{{
		ID: "c1", Schedule: "* * * * *", Path: "/cron", Enabled: true,
		Timezone: "UTC", SkipIfRunning: false,
	}}}
	skip := true
	pending := Pending{Crons: []api.CreateCronRequest{{
		Schedule: "* * * * *", Path: "/cron", Timezone: "Europe/Istanbul", SkipIfRunning: &skip,
	}}}
	got := Compute("api", "", baseline, pending)
	for _, field := range []string{
		"cron[* * * * * /cron].timezone",
		"cron[* * * * * /cron].skip_if_running",
	} {
		if !hasChangeField(got, field, ChangeModify) {
			t.Fatalf("missing %s change: %+v", field, got.Changes)
		}
	}
}

// --- diffEdgeRules: methodsChanged branch ----------------------------

func TestCompute_EdgeRules_MethodsChanged(t *testing.T) {
	// Same action + priority + enabled; MatchMethods flip triggers
	// the methodsChanged branch. existing TestCompute_EdgeRules
	// exercises actionChanged; this pins methodsChanged independently.
	baseline := Baseline{
		EdgeRules: []api.EdgeRuleResponse{
			{
				ID: "e1", MatchHost: "api.example.com", MatchPath: "/v1",
				MatchMethods: []string{"GET"}, Priority: 10, Enabled: true,
				Kind: "route", Action: mkAction("/v1"),
			},
		},
	}
	pending := Pending{
		EdgeRules: []api.CreateEdgeRuleRequest{
			{
				MatchHost: "api.example.com", MatchPath: "/v1",
				MatchMethods: []string{"GET", "POST"}, Priority: intPtr(10),
				Kind: "route", Action: mkAction("/v1"),
			},
		},
	}
	got := Compute("api", "", baseline, pending)
	if !hasChangeField(got, "edge_rule[route api.example.com/v1]", ChangeModify) {
		t.Fatalf("methodsChanged should emit a Modify Change; got %+v", got.Changes)
	}
}

// --- detectSchemaBreak (text-only) — entrypoint + EnvSecrets paths --

func TestCompute_SchemaBreak_TextOnlyEntrypointChange(t *testing.T) {
	// Manifest entrypoint differs from base.OverrideEntrypoint →
	// schema_response_changed with SeverityWarn, Field=entrypoint.
	base := &api.DeploymentResponse{
		OverrideEntrypoint: []string{"/app/server"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{Entrypoint: []string{"/app/new-server"}},
	})
	found := false
	for _, b := range got.Breaks {
		if b.Code == "schema_response_changed" &&
			b.Severity == SeverityWarn &&
			b.Field == "entrypoint" {
			found = true
		}
	}
	if !found {
		t.Fatalf("entrypoint change should emit a warn-severity schema_response_changed break; got %+v", got.Breaks)
	}
}

func TestCompute_SchemaBreak_TextOnlyEnvSecretsChange(t *testing.T) {
	// Manifest.EnvSecrets set differs from base.OverrideEnvSecretRefs →
	// schema_env_changed on manifest.env_secrets.
	base := &api.DeploymentResponse{
		OverrideEnvSecretRefs: map[string]string{"STRIPE_KEY": "ref_a"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{EnvSecrets: map[string]string{"STRIPE_KEY": "ref_b"}},
	})
	found := false
	for _, b := range got.Breaks {
		if b.Code == "schema_env_changed" && b.Field == "manifest.env_secrets" {
			found = true
		}
	}
	if !found {
		t.Fatalf("EnvSecrets change should emit a schema_env_changed break on manifest.env_secrets; got %+v", got.Breaks)
	}
}

func TestCompute_SchemaBreak_TextOnlyEnvSecretsCleared(t *testing.T) {
	// EnvSecrets cleared (empty map) but baseline had refs.
	base := &api.DeploymentResponse{
		OverrideEnvSecretRefs: map[string]string{"X": "ref_x"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{EnvSecrets: map[string]string{}},
	})
	// Empty map has len 0; baseline is non-empty → not equal →
	// schema_env_changed break must fire (the previous `len(...) > 0`
	// guard silently dropped this case).
	found := false
	for _, b := range got.Breaks {
		if b.Code == "schema_env_changed" && b.Field == "manifest.env_secrets" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cleared EnvSecrets must fire schema_env_changed; got %+v", got.Breaks)
	}
}

// --- schemaBreakReason — direct, all arms ----------------------------

func TestSchemaBreakReason_AllKinds(t *testing.T) {
	cases := []struct {
		kind openapidiff.SchemaKind
		want string
	}{
		{openapidiff.SchemaKindTypeChange, "schema type changed"},
		{openapidiff.SchemaKindFieldRemoved, "schema field removed"},
		{openapidiff.SchemaKindRequiredAdded, "schema field required"},
		{openapidiff.SchemaKindNullabilityChange, "schema nullability changed"},
		// Default branch (any other kind, including zero-value).
		{openapidiff.SchemaKind("unknown_kind"), "schema changed"},
		{openapidiff.SchemaKind(""), "schema changed"},
	}
	for _, c := range cases {
		got := schemaBreakReason(openapidiff.SchemaBreak{Kind: c.kind})
		if got != c.want {
			t.Errorf("schemaBreakReason(%s) = %q, want %q", c.kind, got, c.want)
		}
	}
}

// --- stringMapsEqual — b[k] != v not-equal branch -------------------

func TestStringMapsEqual_NotEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"equal", map[string]string{"x": "1"}, map[string]string{"x": "1"}, true},
		{"missing-key", map[string]string{"x": "1"}, map[string]string{"y": "1"}, false},
		{"value-differs", map[string]string{"x": "1"}, map[string]string{"x": "2"}, false},
		{"a-empty-b-populated", map[string]string{}, map[string]string{"x": "1"}, false},
		{"both-empty", map[string]string{}, map[string]string{}, true},
		{"nil-vs-nil", nil, nil, true},
		{"extra-key-in-b", map[string]string{"x": "1"}, map[string]string{"x": "1", "y": "2"}, false},
	}
	for _, c := range cases {
		if got := stringMapsEqual(c.a, c.b); got != c.want {
			t.Errorf("stringMapsEqual(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- Quota: untested plan-gate branches ------------------------------

func TestQuota_WebSocketGate_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := true
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{WebSocketEnabled: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanWebSocketNotAllowed) {
		t.Fatalf("Free websocket=true must fire plan_websocket_not_allowed; got %+v", got)
	}
}

func TestQuota_WarmSnapshotGate_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := true
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{WarmSnapshotEnabled: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanWarmSnapshotNotAllowed) {
		t.Fatalf("Free warm_snapshot=true must fire plan_warm_snapshot_not_allowed; got %+v", got)
	}
}

func TestQuota_RequireAuthnGate_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := true
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{RequireAuthn: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanRequireAuthnNotAllowed) {
		t.Fatalf("Free require_authn=true must fire plan_require_authn_not_allowed; got %+v", got)
	}
}

func TestQuota_EgressAllowlistNotAllowed_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	rules := []string{"api.example.com"}
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{EgressAllowlist: &rules},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanEgressAllowlistNotAllowed) {
		t.Fatalf("Free egress_allowlist must fire plan_egress_allowlist_not_allowed; got %+v", got)
	}
}

func TestQuota_AutoscaleTargetRPSGate_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := 5
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{AutoscaleTargetRPS: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanScaleUpNotAllowed) {
		t.Fatalf("Free autoscale_target_rps=5 must fire plan_scale_up_not_allowed; got %+v", got)
	}
}

func TestQuota_AutoscaleTargetRPS_Zero_NotGated(t *testing.T) {
	// Per quota.go:162: only positive RPS trips the gate. RPS=0
	// means "don't tune autoscale" → no break.
	limits := api.MustLimitsFor(api.PlanFree)
	v := 0
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{AutoscaleTargetRPS: &v},
	}, QuotaConfig{Limits: limits})
	if hasCode(got, api.CodePlanScaleUpNotAllowed) {
		t.Fatalf("Free autoscale_target_rps=0 must NOT fire; got %+v", got)
	}
}

func TestQuota_AutoscaleTargetCPGate_Free(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	v := 25
	got := Quota(api.PlanFree, Baseline{}, Pending{
		AppConfig: AppConfigPatch{AutoscaleTargetCP: &v},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanScaleUpNotAllowed) {
		t.Fatalf("Free autoscale_target_cpu_pct=25 must fire plan_scale_up_not_allowed; got %+v", got)
	}
}

// --- Quota: per-account cron cap ------------------------------------

func TestQuota_CronsPerAccount_Breach(t *testing.T) {
	// Hobby CronLimitPerAccount = 10. Account has 8 crons already
	// (cfg.AccountCronCount) on other apps. This app currently has
	// 0 crons (baseline.Crons is empty → existingThisApp=0) and
	// the customer is proposing 5 new crons → post-deploy account
	// count = 8 + (5 - 0) = 13 > 10 → must fire plan_cron_quota.
	limits := api.MustLimitsFor(api.PlanHobby)
	pendingCrons := make([]api.CreateCronRequest, 5)
	for i := range pendingCrons {
		pendingCrons[i] = api.CreateCronRequest{
			Schedule: "* * * * *",
			Path:     "/new_" + string(rune('a'+i)),
		}
	}
	got := Quota(api.PlanHobby, Baseline{}, Pending{Crons: pendingCrons},
		QuotaConfig{Limits: limits, AccountCronCount: 8})
	if !hasCode(got, api.CodePlanCronQuota) {
		t.Fatalf("Hobby per-account cron = 8 + 5 new = 13 over cap 10 must fire plan_cron_quota; got %+v", got)
	}
}

// --- Quota: edge-rule kind gate (ip) ---------------------------------

func TestQuota_EdgeRuleKind_IP_Free(t *testing.T) {
	// Free plan cannot use kind=ip either (parallel to jwt test).
	limits := api.MustLimitsFor(api.PlanFree)
	got := Quota(api.PlanFree, Baseline{}, Pending{
		EdgeRules: []api.CreateEdgeRuleRequest{{Kind: "ip", MatchPath: "/p", Action: jsonRaw("{}")}},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanEdgeRuleKindNotAllowed) {
		t.Fatalf("Free ip-edge-rule must fire plan_edge_rule_kind_not_allowed; got %+v", got)
	}
}

// --- Quota: env-total cap (EnvVarsMax) -------------------------------

func TestQuota_EnvVarsMax_Breach(t *testing.T) {
	// Hobby EnvVarsMax = 32. Push 33 keys (across two scopes) to
	// drive the breach.
	limits := api.MustLimitsFor(api.PlanHobby)
	keys := make([]PendingEnv, 0, 18)
	for i := 0; i < 18; i++ {
		keys = append(keys, PendingEnv{Key: "k1_" + string(rune('a'+i))})
	}
	keys2 := make([]PendingEnv, 0, 15)
	for i := 0; i < 15; i++ {
		keys2 = append(keys2, PendingEnv{Key: "k2_" + string(rune('a'+i))})
	}
	got := Quota(api.PlanHobby, Baseline{}, Pending{
		EnvByScope: map[string][]PendingEnv{
			"default": keys,
			"staging": keys2,
		},
	}, QuotaConfig{Limits: limits})
	if !hasCode(got, api.CodePlanLimitEnvVars) {
		t.Fatalf("Hobby 33 env vars over cap 32 must fire plan_limit_env_vars; got %+v", got)
	}
}

// --- helpers ---------------------------------------------------------

// hasChangeField returns true iff a Change with the given field name
// and kind exists in d.Changes. Used by the per-field per-kind
// tests above.
func hasChangeField(d Diff, field string, kind ChangeKind) bool {
	for _, c := range d.Changes {
		if c.Field == field && c.Kind == kind {
			return true
		}
	}
	return false
}
