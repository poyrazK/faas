package mcpmanagement

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A deployment staged at 0% by the CLI or an earlier session (no plan in this
// server) is promoted through a promotion plan; the plan refuses anything
// that is not the app's latest staged revision.
func TestPlanPromotionPromotesAStagedDeployment(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	f.mu.Lock()
	f.deployments["release-new"] = api.DeploymentResponse{ID: "release-new", AppID: "app-one", Kind: "image", Status: "live", Scope: "default", TrafficPercent: 0}
	f.latest = "release-new"
	f.mu.Unlock()

	for name, args := range map[string]map[string]any{
		"serving":    {"app": "alpha", "deployment_id": "release-base", "reason": "Promote"},
		"superseded": {"app": "alpha", "deployment_id": "release-old", "reason": "Promote"},
		"missing":    {"app": "alpha", "deployment_id": "release-gone", "reason": "Promote"},
	} {
		if out := callTool(t, ctx, cs, "gregale_plan_promotion", args, true); out == nil {
			t.Fatalf("%s: no refusal", name)
		}
	}

	out := callTool(t, ctx, cs, "gregale_plan_promotion", map[string]any{"app": "alpha", "deployment_id": "release-new", "reason": "Promote staged image"}, false)
	if string(out["expected_current_deployment_id"]) != `"release-base"` || !strings.Contains(string(out["traffic_effect"]), "release-new") {
		t.Fatalf("plan=%s", out)
	}
	args := map[string]any{"plan_id": planID(t, out)}
	out = callTool(t, ctx, cs, "gregale_promote_deployment", args, false)
	if !strings.Contains(string(out["deployment"]), `"traffic_percent":100`) {
		t.Fatalf("promoted=%s", out)
	}
	callTool(t, ctx, cs, "gregale_promote_deployment", args, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current != "release-new" {
		t.Fatalf("serving=%s", f.current)
	}
}

func TestPlanPromotionRefusesAnOlderStagedRevision(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	f.mu.Lock()
	f.deployments["release-new"] = api.DeploymentResponse{ID: "release-new", AppID: "app-one", Status: "live", Scope: "default", TrafficPercent: 0}
	f.latest = "release-newer"
	f.mu.Unlock()
	out := callTool(t, ctx, cs, "gregale_plan_promotion", map[string]any{"app": "alpha", "deployment_id": "release-new", "reason": "Promote"}, true)
	if string(out["code"]) != `"not_latest"` {
		t.Fatalf("refusal=%s", out)
	}
}
