package stagealerts_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/stagealerts"
	"github.com/onebox-faas/faas/pkg/state"
)

type envReader map[string]state.ProjectEnvironment

func (e envReader) ProjectEnvironmentBySlug(_ context.Context, _, _, slug string) (state.ProjectEnvironment, error) {
	if slug == "broken" {
		return state.ProjectEnvironment{}, errors.New("db down")
	}
	env, ok := e[slug]
	if !ok {
		return state.ProjectEnvironment{}, state.ErrNotFound
	}
	return env, nil
}

// adr: 20261104120000 — ?environment names production (omitted or a
// production slug) or one of the app's stages.
func TestEnvironment(t *testing.T) {
	store := envReader{"staging": {ID: "env-1", Slug: "staging"}}
	app := state.App{AccountID: "acct", ProjectID: "proj"}
	for _, tc := range []struct {
		app    state.App
		slug   string
		want   string
		status int
	}{
		{app, "", "", 0},
		{app, "production", "", 0},
		{app, " staging ", "env-1", 0},
		{app, "nope", "", http.StatusNotFound},
		{state.App{AccountID: "acct"}, "staging", "", http.StatusNotFound},
		{app, "broken", "", http.StatusServiceUnavailable},
	} {
		got, prob := stagealerts.Environment(context.Background(), store, tc.app, tc.slug)
		status := 0
		if prob != nil {
			status = prob.Status
		}
		if got != tc.want || status != tc.status {
			t.Errorf("slug %q: %q, status %d; want %q, %d", tc.slug, got, status, tc.want, tc.status)
		}
	}
}

// adr: 20261104120000 — a stage rule needs the runtime key and a metric,
// comparison and action the stage can run; a paused stage rule can be
// edited while either is missing.
func TestCheckNewAndUpdated(t *testing.T) {
	runnable := state.AlertRule{AppID: "app", EnvironmentID: "env-1", Enabled: true, Metric: state.AlertMetricErrorRate,
		Comparison: state.AlertGt, WebhookURL: "https://example.com/hook"}
	notRunnable := runnable
	notRunnable.Metric = state.AlertMetricSLOBudgetBurn
	for _, tc := range []struct {
		name    string
		rule    state.AlertRule
		open    bool
		new     int
		updated int
	}{
		{"production", state.AlertRule{Enabled: true, Metric: state.AlertMetricSLOBudgetBurn}, false, 0, 0},
		{"closed", runnable, false, http.StatusNotImplemented, http.StatusNotImplemented},
		{"open", runnable, true, 0, 0},
		{"not runnable", notRunnable, true, http.StatusBadRequest, http.StatusBadRequest},
		{"paused, closed", func() state.AlertRule { r := notRunnable; r.Enabled = false; return r }(), false, http.StatusNotImplemented, 0},
	} {
		gotNew, gotUpdated := 0, 0
		if p := stagealerts.CheckNew(tc.rule, tc.open); p != nil {
			gotNew = p.Status
		}
		if p := stagealerts.CheckUpdated(tc.rule, tc.open); p != nil {
			gotUpdated = p.Status
		}
		if gotNew != tc.new || gotUpdated != tc.updated {
			t.Errorf("%s: new %d, updated %d; want %d, %d", tc.name, gotNew, gotUpdated, tc.new, tc.updated)
		}
	}
}
