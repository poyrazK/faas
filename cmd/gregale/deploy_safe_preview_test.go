package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
)

func TestNewSafeReleasePreview_UsesBalancedLadderAndRollbackTarget(t *testing.T) {
	latest := &api.DeploymentResponse{ID: "dep-previous"}
	got, err := newSafeReleasePreview(&api.CanaryPresetSpec{Preset: "balanced"}, latest)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("preview = nil, want safe-release preview")
	}
	if got.Rollout.Step != 1 || got.Rollout.TotalSteps != 4 || got.Rollout.TrafficPercent != 1 {
		t.Fatalf("rollout = %+v, want step 1/4 at 1%%", got.Rollout)
	}
	if got.RollbackTarget != latest.ID {
		t.Errorf("rollback target = %q, want %q", got.RollbackTarget, latest.ID)
	}
	wantPercents := []int{1, 10, 50, 100}
	for i, want := range wantPercents {
		if got.Rollout.Stages[i].TrafficPercent != want {
			t.Errorf("stage %d traffic = %d, want %d", i, got.Rollout.Stages[i].TrafficPercent, want)
		}
	}
}

func TestSafeReleaseHealthGate_FiltersToActionableRules(t *testing.T) {
	got := safeReleaseHealthGate([]api.AlertRuleResponse{
		{Name: "zeta", Metric: "latency_p95_ms", Action: "demote", Enabled: true, State: "ok"},
		{Name: "alpha", Metric: "error_rate_pct", Action: "rollback", Enabled: true, State: "firing"},
		{Name: "ignored-webhook", Action: "webhook", Enabled: true, State: "firing"},
		{Name: "ignored-disabled", Action: "rollback", Enabled: false, State: "firing"},
	})
	if got.Status != deploydiff.SafeReleaseHealthBlocked || got.Configured != 2 || got.Firing != 1 {
		t.Fatalf("health gate = %+v, want blocked/2/1", got)
	}
	if len(got.Rules) != 2 || got.Rules[0].Name != "alpha" || got.Rules[1].Name != "zeta" {
		t.Fatalf("rules = %+v, want sorted actionable rules", got.Rules)
	}
}

func TestAddSafeReleasePreview_ReportsMissingHealthGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/alerts" {
			t.Fatalf("path = %q, want /v1/apps/demo/alerts", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	d := deploydiff.Diff{Slug: "demo"}
	opts := diffCLIOptions{
		Slug:   "demo",
		Safe:   true,
		Canary: &api.CanaryPresetSpec{Preset: "balanced"},
	}
	addSafeReleasePreview(context.Background(), NewClient(server.URL, "test"), opts, deploydiff.EmptyBaseline(), &d)
	if d.SafeRelease == nil || d.SafeRelease.HealthGate.Status != deploydiff.SafeReleaseHealthNotConfigured {
		t.Fatalf("safe release = %+v, want not_configured health gate", d.SafeRelease)
	}
	for _, b := range d.Breaks {
		if b.Code == "safe_release_health_gate_missing" {
			return
		}
	}
	t.Fatalf("breaks = %+v, want safe_release_health_gate_missing", d.Breaks)
}

func TestAddSafeReleasePreview_ReportsHealthGateInHumanOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"checkout errors","metric":"error_rate_pct","action":"rollback","enabled":true,"state":"firing"}]`))
	}))
	defer server.Close()

	d := deploydiff.Diff{Slug: "demo"}
	opts := diffCLIOptions{Slug: "demo", Safe: true, Canary: &api.CanaryPresetSpec{Preset: "balanced"}}
	addSafeReleasePreview(context.Background(), NewClient(server.URL, "test"), opts, deploydiff.EmptyBaseline(), &d)
	if d.SafeRelease.HealthGate.Status != deploydiff.SafeReleaseHealthBlocked {
		t.Fatalf("status = %q, want blocked", d.SafeRelease.HealthGate.Status)
	}
	var out strings.Builder
	deploydiff.RenderText(&out, d)
	for _, want := range []string{"Safe rollout:", "1% (2m0s) → 10% (2m0s) → 50% (2m0s) → 100% (0s)", "step 1/4", "5xx rollback: enabled (first wake)", "BLOCKED", "Health gate blocks rollout."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}
