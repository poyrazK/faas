package apidserver_test

// adr: 20261028110044

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Per-customer route evaluation groups by platform tenant or API consumer
// identities, so it follows those capabilities rather than the route
// feature's own.
func TestRouteCustomerGroupingFollowsIdentityCapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "production-route-monitoring"), platformflags.Definition{Enabled: true, Default: true})
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "grouped-api", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	budget, zero := int64(100), int64(0)
	req := api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: &zero, CustomerGroupBy: "tenant",
		Routes: []api.RouteMonitorRoute{{Method: http.MethodGet, Path: "/orders", Max5xxRateBPS: &budget}}}
	if r := e.Do(t, http.MethodPut, "/v1/apps/grouped-api/route-monitor", req, nil); !capabilityRefused(r) {
		t.Fatalf("tenant grouping with platform tenants off = %d %s, want capability_not_released", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/apps/grouped-api/route-monitor/preview", api.PreviewRouteMonitorRequest{CustomerGroupBy: "consumer", Routes: req.Routes}, nil); !capabilityRefused(r) {
		t.Fatalf("consumer grouping preview with API consumers off = %d %s, want capability_not_released", r.Code, r.Body)
	}
	req.CustomerGroupBy = ""
	if r := e.Do(t, http.MethodPut, "/v1/apps/grouped-api/route-monitor", req, nil); r.Code != http.StatusOK {
		t.Fatalf("ungrouped monitor: %d %s", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "platform-tenants"), platformflags.Definition{Enabled: true, Default: true})
	one := int64(1)
	req.CustomerGroupBy, req.ExpectedRevision = "tenant", &one
	if r := e.Do(t, http.MethodPut, "/v1/apps/grouped-api/route-monitor", req, nil); r.Code != http.StatusOK {
		t.Fatalf("tenant grouping with platform tenants on: %d %s", r.Code, r.Body)
	}
}

// A held canary's report says when the hold began and when the hold
// deadline aborts it.
func TestRouteHealthReportShowsHoldDeadline(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "held-canary")
	app, _ := (*e.Store()).AppBySlug(t.Context(), slug)
	stable, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	d, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + slug + "/route-health"
	report := func() api.RouteHealthReport {
		t.Helper()
		rec := e.Do(t, "GET", base+"/deployments/"+d.ID, nil, nil)
		var out api.RouteHealthReport
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("report: %d %s", rec.Code, rec.Body)
		}
		return out
	}
	gate := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}
	if rec := e.Do(t, "PUT", base+"/gate", gate, nil); rec.Code != http.StatusOK {
		t.Fatalf("gate %d %s", rec.Code, rec.Body)
	}
	if hold := report().Hold; hold != nil {
		t.Fatalf("hold before any blocked advance: %+v", hold)
	}
	before := time.Now().Add(-time.Second)
	if rec := e.Do(t, "POST", "/v1/deployments/"+d.ID+"/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("advance on unknown evidence = %d %s, want a hold", rec.Code, rec.Body)
	}
	hold := report().Hold
	if hold == nil || hold.Since.Before(before) || !hold.AbortsAt.Equal(hold.Since.Add(api.RouteHealthHoldDeadline)) {
		t.Fatalf("hold = %+v, want since the advance and aborting %s later", hold, api.RouteHealthHoldDeadline)
	}
}
