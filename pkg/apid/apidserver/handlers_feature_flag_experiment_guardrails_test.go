package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

type flagExperimentTestEnv struct {
	e     apidserver.TestEnv
	mem   *state.MemStore
	scope state.FeatureFlagScope
	base  string
}

// newFlagExperimentTestEnv is a Pro account with project "flags-guard" whose
// production environment has a two-variant "checkout" flag with one weighted
// rule for everyone.
func newFlagExperimentTestEnv(t *testing.T) flagExperimentTestEnv {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	ctx := context.Background()
	acct := (*e.Acct()).ID
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-guard", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	env, err := (*e.Store()).ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID}
	everyone := 10000
	if _, err := (*e.Store()).UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, Actor: acct,
		Config: flags.Config{Groups: map[string][]string{}, Flags: []flags.Flag{
			{Key: "checkout", Type: "variant", Enabled: true, Default: "legacy", Variants: []flags.FlagVariant{{Key: "legacy", Weight: 5000}, {Key: "new", Weight: 5000}},
				Rules: []flags.Rule{{ID: "weighted", Rollout: &everyone}}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return flagExperimentTestEnv{e: e, mem: *e.Store(), scope: scope, base: "/v1/projects/flags-guard/environments/production/experiments"}
}

func (f flagExperimentTestEnv) do(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	res := f.e.Do(t, method, path, body, nil)
	return res.Code, res.Body.String()
}

func (f flagExperimentTestEnv) create(t *testing.T, req api.FlagExperimentCreateRequest) state.FlagExperiment {
	t.Helper()
	if code, body := f.do(t, http.MethodPost, f.base, req); code != http.StatusCreated {
		t.Fatalf("create %s = %d %s", req.Key, code, body)
	}
	x, err := f.mem.GetFlagExperiment(context.Background(), f.scope.EnvironmentID, req.Key)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func (f flagExperimentTestEnv) ruleValue(t *testing.T) any {
	t.Helper()
	v, err := f.mem.GetFeatureFlags(context.Background(), f.scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v.Config.Flags[0].Rules[0].Value
}

// ADR 20261116190208 §1: a variant whose server error rate is clearly above
// control's sends every customer of the rule to control and stops the
// experiment, even when the pass cannot read new telemetry; control's p95
// latency becomes the latency threshold.
func TestFlagExperimentGuardrailBreachRollsBackToControl(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	x := f.create(t, api.FlagExperimentCreateRequest{Key: "guarded", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Start: true})
	if !(x.Guardrails.ServerErrorRate.MaxIncrease == api.FlagExperimentGuardrailErrorTolerance && x.Guardrails.P95Latency.MaxIncrease == api.FlagExperimentGuardrailLatencyTolerance) {
		t.Fatalf("default guardrails = %+v", x.Guardrails)
	}
	now := time.Now()
	for i := 0; i < 150; i++ {
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "legacy", now, 20, int64(i%2), 0, 0, 100)
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "new", now, 20, int64(8+i%5), 0, 0, 100)
	}

	apidserver.RunFlagExperimentPass(*f.e.S(), now)

	if got := f.ruleValue(t); got != "legacy" {
		t.Fatalf("rule value after breach = %v; want legacy for everyone", got)
	}
	code, body := f.do(t, http.MethodGet, f.base+"/guarded/results", nil)
	var res api.FlagExperimentResults
	if err := json.Unmarshal([]byte(body), &res); code != http.StatusOK || err != nil {
		t.Fatalf("results = %d %s", code, body)
	}
	if res.Experiment.Status != flags.ExperimentStopped || res.Experiment.StopReason != flags.StopGuardrailBreach ||
		res.Experiment.Breach["guardrail"] != flags.GuardrailServerErrorRate || res.Experiment.Breach["rolled_back"] != true {
		t.Fatalf("experiment = %+v; want stopped by the server error guardrail", res.Experiment)
	}
	if res.Recommendation.Action != flags.RecommendRolledBack {
		t.Fatalf("recommendation = %+v; want rolled_back", res.Recommendation)
	}
	if len(res.Guardrails) != 2 || res.Guardrails[0].Status != flags.GuardrailBreached {
		t.Fatalf("guardrails = %+v; want the server error rate breached", res.Guardrails)
	}
	if res.Experiment.LatencyThresholdMS == nil || len(res.Latency) != 2 || res.Latency[0].Requests != 3000 {
		t.Fatalf("latency = threshold %v, %+v; want control's p95 fixed and per-variant p95", res.Experiment.LatencyThresholdMS, res.Latency)
	}
}

// ADR 20261116190208 §1: a variant within tolerance keeps running.
func TestFlagExperimentGuardrailWithinToleranceKeepsRunning(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	x := f.create(t, api.FlagExperimentCreateRequest{Key: "calm", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Start: true})
	now := time.Now()
	for i := 0; i < 150; i++ {
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "legacy", now, 20, int64(i%20/19), 0, 0, 100)
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "new", now, 20, int64(i%20/19), 0, 0, 100)
	}
	apidserver.RunFlagExperimentPass(*f.e.S(), now)
	if got := f.ruleValue(t); got != nil {
		t.Fatalf("rule value = %v; want the weighted allocation kept", got)
	}
	got, err := f.mem.GetFlagExperiment(context.Background(), f.scope.EnvironmentID, "calm")
	if err != nil || got.Status != flags.ExperimentRunning {
		t.Fatalf("experiment = %+v, %v; want running", got, err)
	}
}

// ADR 20261116190208 §2, §3: create validates metric kind and guardrails;
// ship publishes the rule serving one variant and stops the experiment.
func TestFlagExperimentShipAndCreateOptions(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	tooBig := 2.0
	for _, tc := range []struct {
		name string
		req  api.FlagExperimentCreateRequest
		want string
	}{
		{"bad kind", api.FlagExperimentCreateRequest{Key: "k1", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order", MetricKind: "median"}, "metric_kind"},
		{"bad tolerance", api.FlagExperimentCreateRequest{Key: "k2", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order",
			Guardrails: &api.FlagExperimentGuardrailsRequest{ServerErrorRate: &api.FlagExperimentGuardrailRequest{MaxIncrease: &tooBig}}}, "max_increase"},
	} {
		if code, body := f.do(t, http.MethodPost, f.base, tc.req); code != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s; want 422 naming %q", tc.name, code, body, tc.want)
		}
	}

	x := f.create(t, api.FlagExperimentCreateRequest{Key: "revenue", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MetricKind: flags.ExperimentMetricMean, Start: true,
		Guardrails: &api.FlagExperimentGuardrailsRequest{P95Latency: &api.FlagExperimentGuardrailRequest{Disabled: true}}})
	if x.MetricKind != flags.ExperimentMetricMean || x.MixingLift != api.FlagExperimentMixingEffect || !x.Guardrails.P95Latency.Disabled || x.Guardrails.ServerErrorRate.Disabled {
		t.Fatalf("created = %+v; want a mean metric with the default effect size and only the error guardrail", x)
	}

	code, body := f.do(t, http.MethodGet, f.base+"/revenue/results", nil)
	if code != http.StatusOK || !strings.Contains(body, `"action":"keep_running"`) || !strings.Contains(body, `"name":"server_error_rate"`) || strings.Contains(body, `"name":"p95_latency"`) {
		t.Fatalf("results = %d %s; want keep_running with only the server error guardrail", code, body)
	}

	if code, body := f.do(t, http.MethodPost, f.base+"/revenue/ship", api.FlagExperimentShipRequest{Variant: "nope"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("ship unknown variant = %d %s; want 422", code, body)
	}
	code, body = f.do(t, http.MethodPost, f.base+"/revenue/ship", api.FlagExperimentShipRequest{Variant: "new"})
	var shipped api.FlagExperimentShipResponse
	if err := json.Unmarshal([]byte(body), &shipped); code != http.StatusOK || err != nil {
		t.Fatalf("ship = %d %s", code, body)
	}
	if shipped.Experiment.Status != flags.ExperimentStopped || shipped.Experiment.StopReason != flags.StopShipped || shipped.ConfigVersion < 2 {
		t.Fatalf("shipped = %+v", shipped)
	}
	if got := f.ruleValue(t); got != "new" {
		t.Fatalf("rule value after ship = %v; want new", got)
	}
	// Shipping again is idempotent: the rule already serves the variant.
	code, body = f.do(t, http.MethodPost, f.base+"/revenue/ship", api.FlagExperimentShipRequest{Variant: "new"})
	if err := json.Unmarshal([]byte(body), &shipped); code != http.StatusOK || err != nil || shipped.ConfigVersion < 2 {
		t.Fatalf("ship again = %d %s", code, body)
	}
	// Shipping a different variant once the rule serves one is refused: the
	// rule no longer allocates by weight.
	if code, body := f.do(t, http.MethodPost, f.base+"/revenue/ship", api.FlagExperimentShipRequest{Variant: "legacy"}); code != http.StatusConflict {
		t.Fatalf("ship another variant = %d %s; want 409", code, body)
	}
}
