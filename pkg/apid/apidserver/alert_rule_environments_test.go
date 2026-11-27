package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — alert routes take an environment: a stage's rules
// are listed and created there, production's list never shows them, a stage
// rule watches only signals the stage has, and stage rules wait for the
// runtime key.
func TestAlertRulesInEnvironment(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	store, account := *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "alert-env"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "alert-env-api",
		Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/apps/alert-env-api/alerts"
	rule := func(name, metric string) api.CreateAlertRuleRequest {
		return api.CreateAlertRuleRequest{Name: name, Metric: metric, Comparison: "gt", Threshold: 5, WindowSpec: "5m",
			WebhookURL: "https://hooks.example.com/a", WebhookSecret: "shh"}
	}

	// A stage's rule waits for the runtime key.
	if res := e.Do(t, http.MethodPost, base+"?environment=staging", rule("early", "error_rate_pct"), nil); res.Code != http.StatusNotImplemented {
		t.Fatalf("stage rule before the key = %d %s, want 501", res.Code, res.Body)
	}
	(*e.S()).EnableStageAlertRulesForTest(t)

	production := tenantJSON[api.AlertRuleResponse](t, e, http.MethodPost, base, rule("prod errors", "error_rate_pct"), http.StatusCreated)
	staged := tenantJSON[api.AlertRuleResponse](t, e, http.MethodPost, base+"?environment=staging", rule("stage errors", "error_rate_pct"), http.StatusCreated)
	if production.EnvironmentID != "" || staged.EnvironmentID != stage.ID {
		t.Fatalf("environments = %q, %q; want production and %s", production.EnvironmentID, staged.EnvironmentID, stage.ID)
	}
	for _, tc := range []struct{ path, want string }{
		{base, production.ID},
		{base + "?environment=production", production.ID},
		{base + "?environment=staging", staged.ID},
	} {
		list := tenantJSON[[]api.AlertRuleResponse](t, e, http.MethodGet, tc.path, nil, http.StatusOK)
		if len(list) != 1 || list[0].ID != tc.want {
			t.Fatalf("GET %s = %+v, want only %s", tc.path, list, tc.want)
		}
	}
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"?environment=missing", nil, http.StatusNotFound)

	// A stage rule on a signal stages do not count is refused.
	if res := e.Do(t, http.MethodPost, base+"?environment=staging", rule("stage queue", "queue_depth"), nil); res.Code < 400 || res.Code >= 500 {
		t.Fatalf("stage queue_depth rule = %d %s, want a validation refusal", res.Code, res.Body)
	}
	// A stage rule cannot move to an anomaly baseline.
	anomaly := "above_baseline"
	if res := e.Do(t, http.MethodPatch, base+"/"+staged.ID, api.UpdateAlertRuleRequest{Comparison: &anomaly}, nil); res.Code < 400 || res.Code >= 500 {
		t.Fatalf("stage rule to a baseline = %d %s, want a refusal", res.Code, res.Body)
	}
	// Pausing it is allowed.
	off := false
	paused := tenantJSON[api.AlertRuleResponse](t, e, http.MethodPatch, base+"/"+staged.ID, api.UpdateAlertRuleRequest{Enabled: &off}, http.StatusOK)
	if paused.Enabled || paused.EnvironmentID != stage.ID {
		t.Fatalf("paused = %+v", paused)
	}
}
