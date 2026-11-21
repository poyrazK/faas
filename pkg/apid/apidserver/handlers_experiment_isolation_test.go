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
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (f flagExperimentTestEnv) rule(t *testing.T) flags.Rule {
	t.Helper()
	v, err := f.mem.GetFeatureFlags(context.Background(), f.scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v.Config.Flags[0].Rules[0]
}

// ADR 20261119173702 §2: a layered experiment holds a slice of its layer
// while it runs and its rule carries it; a layer in use cannot go; the
// holdout cannot change while an experiment runs, and once set every
// started experiment's rule carries it.
func TestFlagExperimentLayersAndHoldout(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	layers := "/v1/projects/flags-guard/environments/production/experiment-layers"
	holdout := "/v1/projects/flags-guard/environments/production/experiment-holdout"
	if code, body := f.do(t, http.MethodPost, layers, api.FlagExperimentLayerRequest{Key: "Not A Key"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad layer key = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodPost, layers, api.FlagExperimentLayerRequest{Key: "checkout-layer"}); code != http.StatusCreated {
		t.Fatalf("create layer = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodPost, layers, api.FlagExperimentLayerRequest{Key: "checkout-layer"}); code != http.StatusConflict {
		t.Fatalf("duplicate layer = %d %s", code, body)
	}
	missing := api.FlagExperimentCreateRequest{Key: "nowhere", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		Layer: "missing", Traffic: 5000, Start: true}
	if code, body := f.do(t, http.MethodPost, f.base, missing); code != http.StatusUnprocessableEntity || !strings.Contains(body, "layer") {
		t.Fatalf("unknown layer = %d %s", code, body)
	}

	layered := f.create(t, api.FlagExperimentCreateRequest{Key: "layered", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricEvent: "order_completed", Layer: "checkout-layer", Traffic: 6000, Start: true})
	if layered.Status != flags.ExperimentRunning || layered.LayerFrom != 0 || layered.LayerTo != 6000 {
		t.Fatalf("layered = %+v; want running on [0, 6000)", layered)
	}
	if x := f.rule(t).Experiment; x == nil || x.Key != "layered" || x.Layer != "checkout-layer" || x.From != 0 || x.To != 6000 || x.Unit != flags.ExperimentUnitCustomer {
		t.Fatalf("rule experiment = %+v", x)
	}
	code, body := f.do(t, http.MethodGet, layers, nil)
	var list api.FlagExperimentLayerList
	if err := json.Unmarshal([]byte(body), &list); code != http.StatusOK || err != nil || len(list.Layers) != 1 || list.Layers[0].Free != 4000 || len(list.Layers[0].Slices) != 1 {
		t.Fatalf("layers = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodDelete, layers+"/checkout-layer", nil); code != http.StatusConflict {
		t.Fatalf("delete layer in use = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodPut, holdout, api.FlagExperimentHoldout{BasisPoints: 500}); code != http.StatusConflict {
		t.Fatalf("holdout while running = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodPost, f.base+"/layered/stop", nil); code != http.StatusOK {
		t.Fatalf("stop = %d %s", code, body)
	}
	if x := f.rule(t).Experiment; x != nil {
		t.Fatalf("rule experiment after stop = %+v", x)
	}

	if code, body := f.do(t, http.MethodPut, holdout, api.FlagExperimentHoldout{BasisPoints: 3000}); code != http.StatusUnprocessableEntity {
		t.Fatalf("holdout above the limit = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodPut, holdout, api.FlagExperimentHoldout{BasisPoints: 500}); code != http.StatusOK {
		t.Fatalf("holdout = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodGet, holdout, nil); code != http.StatusOK || !strings.Contains(body, `"basis_points":500`) {
		t.Fatalf("get holdout = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodDelete, layers+"/checkout-layer", nil); code != http.StatusNoContent {
		t.Fatalf("delete layer = %d %s", code, body)
	}
	if code, body := f.do(t, http.MethodDelete, layers+"/checkout-layer", nil); code != http.StatusNotFound {
		t.Fatalf("delete missing layer = %d %s", code, body)
	}
	f.create(t, api.FlagExperimentCreateRequest{Key: "held", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricEvent: "order_completed", Start: true})
	if x := f.rule(t).Experiment; x == nil || x.Key != "held" || x.Layer != "" {
		t.Fatalf("rule experiment under a holdout = %+v; want one without a layer", x)
	}
}

// ADR 20261119173702 §1, §3, §4: the unit, allocation, layer and metric
// settings are validated against the metric kind.
func TestFlagExperimentDesignValidation(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	base := api.FlagExperimentCreateRequest{Key: "k", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed"}
	with := func(mut func(*api.FlagExperimentCreateRequest)) api.FlagExperimentCreateRequest {
		r := base
		mut(&r)
		return r
	}
	for _, tc := range []struct {
		name string
		req  api.FlagExperimentCreateRequest
		want string
	}{
		{"unit", with(func(r *api.FlagExperimentCreateRequest) { r.Unit = "device" }), "unit"},
		{"subject mean", with(func(r *api.FlagExperimentCreateRequest) { r.Unit, r.MetricKind = "subject", flags.ExperimentMetricMean }), "subject"},
		{"allocation", with(func(r *api.FlagExperimentCreateRequest) { r.Allocation = "greedy" }), "allocation"},
		{"bandit mean", with(func(r *api.FlagExperimentCreateRequest) {
			r.Allocation, r.MetricKind = "bandit", flags.ExperimentMetricMean
		}), "bandit"},
		{"traffic without layer", with(func(r *api.FlagExperimentCreateRequest) { r.Traffic = 100 }), "traffic"},
		{"layer traffic", with(func(r *api.FlagExperimentCreateRequest) { r.Layer = "l" }), "traffic"},
		{"ratio denominator", with(func(r *api.FlagExperimentCreateRequest) { r.MetricKind = flags.ExperimentMetricRatio }), "denominator_event"},
		{"denominator on conversion", with(func(r *api.FlagExperimentCreateRequest) { r.DenominatorEvent = "visit" }), "denominator_event"},
		{"percentile", with(func(r *api.FlagExperimentCreateRequest) {
			r.MetricKind, r.Percentile = flags.ExperimentMetricPercentile, 30
		}), "percentile"},
		{"subject rule", with(func(r *api.FlagExperimentCreateRequest) { r.Unit = "subject" }), ""},
	} {
		code, body := f.do(t, http.MethodPost, f.base, tc.req)
		if tc.want == "" {
			// Without product analytics a subject experiment is refused first.
			if code != http.StatusForbidden {
				t.Errorf("%s: %d %s; want 403", tc.name, code, body)
			}
			continue
		}
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s; want 422 naming %q", tc.name, code, body, tc.want)
		}
	}
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)
	if code, body := f.do(t, http.MethodPost, f.base, with(func(r *api.FlagExperimentCreateRequest) { r.Unit = "subject" })); code != http.StatusUnprocessableEntity || !strings.Contains(body, "rollout_unit subject") {
		t.Fatalf("subject experiment on a customer rule = %d %s", code, body)
	}
	ratio := f.create(t, with(func(r *api.FlagExperimentCreateRequest) {
		r.Key, r.MetricKind, r.DenominatorEvent = "per-visit", flags.ExperimentMetricRatio, "visit"
	}))
	if ratio.DenominatorEvent != "visit" || ratio.MixingLift != api.FlagExperimentMixingEffect {
		t.Fatalf("ratio experiment = %+v", ratio)
	}
	code, body := f.do(t, http.MethodGet, f.base+"/per-visit/results", nil)
	if code != http.StatusOK || !strings.Contains(body, `"reason":"not_supported"`) {
		t.Fatalf("ratio results = %d %s; want CUPED not supported", code, body)
	}
}

// ADR 20261119173702 §4: a bandit shifts its flag's weights to the leading
// arm, keeps the floor on the others, and reports best-arm probabilities and
// expected loss instead of p-values.
func TestFlagExperimentBanditReweights(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	x := f.create(t, api.FlagExperimentCreateRequest{Key: "bandit", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricEvent: "order_completed", Allocation: flags.ExperimentAllocationBandit, Start: true})
	now := time.Now()
	conversions := map[string]time.Time{}
	for i := 0; i < 300; i++ {
		legacy, fresh := uuid.NewString(), uuid.NewString()
		f.mem.SeedFlagExperimentUnitForTest(x.ID, legacy, "legacy", now.Add(-time.Hour), 1, 0, 0, 0, 10)
		f.mem.SeedFlagExperimentUnitForTest(x.ID, fresh, "new", now.Add(-time.Hour), 1, 0, 0, 0, 10)
		if i%10 == 0 {
			conversions[legacy] = now
		}
		if i%10 < 4 {
			conversions[fresh] = now
		}
	}
	if err := f.mem.MarkFlagExperimentUnitConversions(context.Background(), x.ID, conversions); err != nil {
		t.Fatal(err)
	}
	apidserver.RunFlagExperimentPass(*f.e.S(), now)
	v, err := f.mem.GetFeatureFlags(context.Background(), f.scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	weights := map[string]int{}
	for _, variant := range v.Config.Flags[0].Variants {
		weights[variant.Key] = variant.Weight
	}
	if weights["legacy"] != api.ExperimentBanditFloorBasisPoints || weights["new"] != 10000-api.ExperimentBanditFloorBasisPoints || v.Actor != "system:flags-experiment-bandit" {
		t.Fatalf("weights after a pass = %v by %q; want the floor on legacy", weights, v.Actor)
	}
	code, body := f.do(t, http.MethodGet, f.base+"/bandit/results", nil)
	var res api.FlagExperimentResults
	if err := json.Unmarshal([]byte(body), &res); code != http.StatusOK || err != nil || res.Bandit == nil || res.Bandit.WeightedAt == nil {
		t.Fatalf("results = %d %s", code, body)
	}
	if len(res.Variants) != 1 || res.Variants[0].PValue != nil || res.Variants[0].BestProbability == nil || *res.Variants[0].BestProbability < 0.99 {
		t.Fatalf("bandit variant = %+v; want a best-arm probability and no p-value", res.Variants)
	}
	if res.Recommendation.Action != flags.RecommendShipVariant || res.Recommendation.Variant != "new" {
		t.Fatalf("recommendation = %+v; want ship new", res.Recommendation)
	}
}

// ADR 20261119173702 §1: SDK exposures add subject units to a running
// subject experiment, for active customers only, when product analytics is
// on.
func TestFlagExperimentSubjectExposures(t *testing.T) {
	e, token, customer := flagEventsRuntime(t, api.PlanPro)
	ctx := context.Background()
	acct := (*e.Acct()).ID
	project, err := (*e.Store()).ProjectBySlug(ctx, acct, "flags-events")
	if err != nil {
		t.Fatal(err)
	}
	env, err := (*e.Store()).ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID}
	everyone := 10000
	if _, err := (*e.Store()).UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, Actor: acct, Config: flags.Config{Flags: []flags.Flag{{
		Key: "checkout", Type: "variant", Enabled: true, Default: "legacy", Variants: []flags.FlagVariant{{Key: "legacy", Weight: 5000}, {Key: "new", Weight: 5000}},
		Rules: []flags.Rule{{ID: "by-user", Customers: []string{customer}, RolloutUnit: "subject", Rollout: &everyone}}}}}}); err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Authorization": "Bearer " + token}
	exposures := []api.FlagExposure{
		{Flag: "checkout", RuleID: "by-user", Variant: "new", CustomerID: customer, Subject: "user-1"},
		{Flag: "checkout", RuleID: "by-user", Variant: "legacy", CustomerID: customer, Subject: "user-2"},
		{Flag: "checkout", RuleID: "by-user", Variant: "new", CustomerID: uuid.NewString(), Subject: "user-3"},
		{Flag: "checkout", RuleID: "by-user", Variant: "new", CustomerID: customer, Subject: ""},
	}
	post := func() api.FlagEventsResponse {
		t.Helper()
		res := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Exposures: exposures}, auth)
		var out api.FlagEventsResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); res.Code != http.StatusOK || err != nil {
			t.Fatalf("exposures = %d %s", res.Code, res.Body.String())
		}
		return out
	}
	if out := post(); out.Exposures != 0 {
		t.Fatalf("exposures without analytics = %+v", out)
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)
	res := e.Do(t, http.MethodPost, "/v1/projects/flags-events/environments/production/experiments", api.FlagExperimentCreateRequest{
		Key: "by-user", Flag: "checkout", RuleID: "by-user", Control: "legacy", MetricEvent: "order_completed", Unit: "subject", Start: true}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Code, res.Body.String())
	}
	var created api.FlagExperiment
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil || created.Unit != "subject" {
		t.Fatalf("created = %s", res.Body.String())
	}
	v, err := (*e.Store()).GetFeatureFlags(ctx, scope, 0)
	if err != nil || v.Config.Flags[0].Rules[0].Experiment == nil || v.Config.Flags[0].Rules[0].Experiment.Unit != flags.ExperimentUnitSubject {
		t.Fatalf("rule experiment = %+v, %v", v.Config.Flags[0].Rules[0].Experiment, err)
	}
	if out := post(); out.Exposures != 2 {
		t.Fatalf("exposures = %+v; want the two valid ones of the active customer", out)
	}
	tooMany := make([]api.FlagExposure, api.FlagExposuresPerRequest+1)
	if res := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Exposures: tooMany}, auth); res.Code != http.StatusBadRequest {
		t.Fatalf("too many exposures = %d %s", res.Code, res.Body.String())
	}
	x, err := (*e.Store()).GetFlagExperiment(ctx, env.ID, "by-user")
	if err != nil {
		t.Fatal(err)
	}
	arms, _, err := (*e.Store()).FlagExperimentArms(ctx, x, 1)
	if err != nil || len(arms) != 2 {
		t.Fatalf("arms = %+v, %v; want one subject per variant", arms, err)
	}
}

// ADR 20261119173702 §5: a metric-gated stage rolls back to 0 when its
// customers convert clearly worse, and holds without enough units.
func TestFeatureFlagRolloutMetricRollsBack(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name                  string
		comparison, treatment flags.Arm
		wantRollout           int
		wantProgression       bool
	}{
		{"worse", flags.Arm{Exposed: 2000, Converted: 600}, flags.Arm{Exposed: 2000, Converted: 200}, 0, false},
		{"too few", flags.Arm{Exposed: 50, Converted: 15}, flags.Arm{Exposed: 50, Converted: 15}, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, store := setupAutomaticProgressiveRollout(t, []sqlc.FeatureFlagRequestOutcomesRow{
				{DecisionType: "boolean", DecisionValue: "true", RequestCount: 50, UsedCount: 50, P95LatencyMs: 100},
			}, now.Add(-10*time.Minute))
			current, err := (*e.Store()).GetFeatureFlags(context.Background(), scope, 0)
			if err != nil {
				t.Fatal(err)
			}
			current.Flags[0].Rules[0].Progression.Metric = &flags.RolloutMetric{Event: "order_completed"}
			if _, err := store.UpdateFeatureFlags(context.Background(), state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version,
				Config: current.Config, Actor: (*e.Acct()).ID}); err != nil {
				t.Fatal(err)
			}
			store.MemStore.SetRolloutMetricArmsForTest("new-export", "selected", tc.comparison, tc.treatment)
			candidate := state.FeatureFlagAutoRolloutCandidate{Scope: scope, ProjectSlug: "rollouts", EnvironmentSlug: "production"}
			if err := (*e.S()).AutoAdvanceFeatureFlagRollout(context.Background(), candidate, now); err != nil {
				t.Fatal(err)
			}
			after, err := store.GetFeatureFlags(context.Background(), scope, 0)
			if err != nil {
				t.Fatal(err)
			}
			rule := after.Flags[0].Rules[0]
			if *rule.Rollout != tc.wantRollout || (rule.Progression != nil) != tc.wantProgression {
				t.Fatalf("rule = rollout %d progression %+v; want %d", *rule.Rollout, rule.Progression, tc.wantRollout)
			}
			if tc.name == "worse" && after.Actor != apidserver.FeatureFlagAutoAdvanceActor {
				t.Fatalf("rollback actor = %q", after.Actor)
			}
		})
	}
}
