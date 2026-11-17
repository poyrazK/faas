package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261115163113 §1, §5, §6: experiments run on a variant flag's weighted
// rule, one at a time per rule, and report insufficient data until both arms
// have enough customers.
func TestFlagExperimentsLifecycle(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	ctx := context.Background()
	acct := (*e.Acct()).ID
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-exp", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	env, err := (*e.Store()).ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	everyone := 10000
	if _, err := (*e.Store()).UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{
		Scope: state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID},
		Actor: acct,
		Config: flags.Config{Groups: map[string][]string{}, Flags: []flags.Flag{
			{Key: "checkout", Type: "variant", Enabled: true, Default: "legacy", Variants: []flags.FlagVariant{{Key: "legacy", Weight: 5000}, {Key: "new", Weight: 5000}},
				Rules: []flags.Rule{{ID: "weighted", Rollout: &everyone}}},
			{Key: "export", Enabled: true, Default: false, Rules: []flags.Rule{{ID: "pilot", Rollout: &everyone, Value: true}}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/projects/flags-exp/environments/production/experiments"
	do := func(method, path string, body any) (int, string) {
		res := e.Do(t, method, path, body, nil)
		return res.Code, res.Body.String()
	}

	for _, tc := range []struct {
		name string
		req  api.FlagExperimentCreateRequest
		want string
	}{
		{"boolean flag", api.FlagExperimentCreateRequest{Key: "x1", Flag: "export", RuleID: "pilot", Control: "false", MetricEvent: "order"}, "not a variant flag"},
		{"bad key", api.FlagExperimentCreateRequest{Key: "Bad Key", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order"}, "key is"},
		{"bad event", api.FlagExperimentCreateRequest{Key: "x2", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "Order Done"}, "metric_event"},
		{"unknown control", api.FlagExperimentCreateRequest{Key: "x3", Flag: "checkout", RuleID: "weighted", Control: "nope", MetricEvent: "order"}, "is not a variant"},
	} {
		if code, body := do(http.MethodPost, base, tc.req); code != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s; want 422 naming %q", tc.name, code, body, tc.want)
		}
	}

	code, body := do(http.MethodPost, base, api.FlagExperimentCreateRequest{Key: "checkout-v2", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Start: true})
	var x api.FlagExperiment
	if err := json.Unmarshal([]byte(body), &x); code != http.StatusCreated || err != nil {
		t.Fatalf("create = %d %s", code, body)
	}
	if x.Status != flags.ExperimentRunning || x.Weights["new"] != 5000 || x.MixingLift != api.FlagExperimentMixingLift || x.StartedAt == nil {
		t.Fatalf("created = %+v; want running with the flag's weights and the default mixing lift", x)
	}
	if code, body := do(http.MethodPost, base, api.FlagExperimentCreateRequest{Key: "checkout-v3", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Start: true}); code != http.StatusConflict {
		t.Fatalf("second running experiment on the rule = %d %s; want 409", code, body)
	}

	apidserver.RunFlagExperimentPass(*e.S(), time.Now())
	code, body = do(http.MethodGet, base+"/checkout-v2/results", nil)
	var results api.FlagExperimentResults
	if err := json.Unmarshal([]byte(body), &results); code != http.StatusOK || err != nil {
		t.Fatalf("results = %d %s", code, body)
	}
	if results.Control.Variant != "legacy" || len(results.Variants) != 1 || results.Variants[0].Variant != "new" || results.Variants[0].Status != flags.ExperimentInsufficientData {
		t.Fatalf("results = %+v; want new against legacy with insufficient data", results)
	}

	if code, body := do(http.MethodDelete, base+"/checkout-v2", nil); code != http.StatusConflict {
		t.Fatalf("delete running = %d %s; want 409", code, body)
	}
	if code, body := do(http.MethodPost, base+"/checkout-v2/stop", nil); code != http.StatusOK || !strings.Contains(body, `"status":"stopped"`) {
		t.Fatalf("stop = %d %s", code, body)
	}
	code, body = do(http.MethodGet, base, nil)
	if code != http.StatusOK || !strings.Contains(body, `"key":"checkout-v2"`) || !strings.Contains(body, `"key":"checkout-v3"`) {
		t.Fatalf("list = %d %s; want both experiments", code, body)
	}
	if code, body := do(http.MethodPost, base+"/checkout-v3/start", nil); code != http.StatusOK {
		t.Fatalf("start the draft once the rule is free = %d %s", code, body)
	}
	if code, body := do(http.MethodDelete, base+"/checkout-v2", nil); code != http.StatusNoContent {
		t.Fatalf("delete stopped = %d %s", code, body)
	}
	if code, body := do(http.MethodGet, base+"/checkout-v2", nil); code != http.StatusNotFound {
		t.Fatalf("get deleted = %d %s; want 404", code, body)
	}
}
