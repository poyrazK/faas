// routes_canary_e2e_test.go — KVM-free acceptance for critical route health
// during canaries (ADR-454): the canary guard compares a candidate's routes
// with the stable deployment's from the request telemetry the real gateway
// records, holds a regressed canary and lets a healthy one advance.
//
// Two apps run a 50% canary at the same time, so both outcomes are proven in
// one pair of closed telemetry windows. Deployments are seeded at the state
// boundary, as in the other routes tests.

//go:build !no_pg

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeHealthCanary is one app mid-canary: the stable and candidate
// deployments are both live and running, 50% each.
type routeHealthCanary struct {
	slug, host, appID string
	stable, candidate state.Deployment
}

// startRouteHealthCanary puts a balanced canary at its 50% stage (step 2 of 4)
// beside stableID. MarkDeploymentLive gives the stable deployment the rest.
func startRouteHealthCanary(t *testing.T, f *routesFixture, slug, appID, stableID, stableInstanceID string, checkoutStatus int) routeHealthCanary {
	t.Helper()
	candidate, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64),
		CanaryPreset: "balanced", CanaryStep: 2, CanaryTotalSteps: 4, TrafficPercent: 50,
	})
	if err != nil {
		t.Fatalf("create %s canary: %v", slug, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, candidate.ID); err != nil {
		t.Fatalf("mark %s canary live: %v", slug, err)
	}
	publishNormalPathLayer(t, f.normalPathFixture, candidate.ID)
	instance, err := f.store.CreateInstance(f.ctx, appID, candidate.ID, string(state.StateRunning), f.app.RAMMB, f.nodeID, "")
	if err != nil {
		t.Fatalf("create %s canary instance: %v", slug, err)
	}
	f.vmmd.SetVersion(instance.ID, "candidate")
	f.vmmd.SetResponseForPath(stableInstanceID, "/checkout", e2etest.FakeResponse{Status: http.StatusOK, Body: []byte("checkout:stable")})
	f.vmmd.SetResponseForPath(stableInstanceID, "/health", e2etest.FakeResponse{Status: http.StatusOK, Body: []byte("health:stable")})
	f.vmmd.SetResponseForPath(instance.ID, "/checkout", e2etest.FakeResponse{Status: checkoutStatus, Body: []byte("checkout:candidate")})
	f.vmmd.SetResponseForPath(instance.ID, "/health", e2etest.FakeResponse{Status: http.StatusOK, Body: []byte("health:candidate")})
	notifyRoutesApp(t, f, appID, candidate.ID, instance.ID)

	c := routeHealthCanary{slug: slug, host: slug + ".apps.test.example", appID: appID, candidate: candidate}
	if c.stable, err = f.store.DeploymentByID(f.ctx, stableID); err != nil {
		t.Fatalf("read %s stable deployment: %v", slug, err)
	}
	if c.candidate, err = f.store.DeploymentByID(f.ctx, candidate.ID); err != nil {
		t.Fatalf("read %s canary: %v", slug, err)
	}
	if c.stable.TrafficPercent != 50 || c.candidate.TrafficPercent != 50 || c.candidate.RolloutState != "rolling_out" {
		t.Fatalf("%s split = stable %d%% / candidate %d%% (%s), want 50/50 rolling_out", slug,
			c.stable.TrafficPercent, c.candidate.TrafficPercent, c.candidate.RolloutState)
	}
	// Both deployments must be answering before the guard's windows open.
	seen := map[string]bool{}
	deadline := time.Now().Add(30 * time.Second)
	for len(seen) < 2 && time.Now().Before(deadline) {
		if status, body, err := gatewayGetNoFatal(f, c.host, "/health"); err == nil && status == http.StatusOK {
			seen[string(body)] = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !seen["health:stable"] || !seen["health:candidate"] {
		t.Fatalf("%s: GET /health reached %v within 30s, want both deployments", slug, seen)
	}
	return c
}

// createRoutesApp creates another app on the fixture's account with its
// public API open, serving a live stable deployment.
func createRoutesApp(t *testing.T, f *routesFixture, slug string) (api.AppResponse, state.Deployment, state.Instance) {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)})
	if status != http.StatusCreated {
		t.Fatalf("create app %s: status=%d body=%s", slug, status, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode app %s: %v body=%s", slug, err, body)
	}
	if status := statusOnly(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen},
	}); status != http.StatusOK {
		t.Fatalf("open public auth for %s: status=%d", slug, status)
	}
	dep, instance := createNormalPathLiveDeployment(t, f.normalPathFixture, app.ID, "stable")
	f.vmmd.SetVersion(instance.ID, "stable")
	notifyRoutesApp(t, f, app.ID, dep.ID, instance.ID)
	waitForNormalPathResponse(t, f.h, slug+".apps.test.example", "normal-path:stable\n", 30*time.Second)
	return app, dep, instance
}

// notifyRoutesApp tells the gateway a deployment and its running instance
// changed, as the deploy pipeline and schedd do.
func notifyRoutesApp(t *testing.T, f *routesFixture, appID, deploymentID, instanceID string) {
	t.Helper()
	deployment, _ := json.Marshal(map[string]string{"app_id": appID, "deployment_id": deploymentID, "status": string(state.DeployLive)})
	instance, _ := json.Marshal(map[string]string{"app_id": appID, "instance_id": instanceID, "state": string(state.StateRunning)})
	if err := db.Notify(f.ctx, f.h.Pool, db.NotifyDeploymentChanged, string(deployment)); err != nil {
		t.Fatalf("notify deployment_changed: %v", err)
	}
	if err := db.Notify(f.ctx, f.h.Pool, db.NotifyInstanceChanged, string(instance)); err != nil {
		t.Fatalf("notify instance_changed: %v", err)
	}
}

func (f *routesFixture) routeHealthReport(t *testing.T, c routeHealthCanary) api.RouteHealthReport {
	t.Helper()
	var report api.RouteHealthReport
	if status := f.api(t, http.MethodGet, "/v1/apps/"+c.slug+"/route-health/deployments/"+c.candidate.ID, nil, &report); status != http.StatusOK {
		t.Fatalf("GET %s route health report: status=%d", c.slug, status)
	}
	return report
}

func routeHealthFinding(report api.RouteHealthReport, path string) api.RouteHealthFinding {
	i := slices.IndexFunc(report.Routes, func(r api.RouteHealthFinding) bool { return r.Path == path })
	if i < 0 {
		return api.RouteHealthFinding{Path: path, Status: "missing"}
	}
	return report.Routes[i]
}

// routeHealthSettled reports whether every selected route has a verdict from
// two closed windows.
func routeHealthSettled(report api.RouteHealthReport) bool {
	return len(report.Routes) > 0 && !slices.ContainsFunc(report.Routes, func(r api.RouteHealthFinding) bool { return r.Status == "unknown" })
}

func routeHealthSummary(report api.RouteHealthReport) string {
	out := []string{report.Status + "/" + report.Reason}
	for _, r := range report.Routes {
		windows := make([]string, 0, len(r.Windows))
		for _, w := range r.Windows {
			windows = append(windows, fmt.Sprintf("%s candidate=%d/%d stable=%d/%d %s/%s", w.Start.Format("15:04"),
				w.Candidate.ServerErrors, w.Candidate.Requests, w.Stable.ServerErrors, w.Stable.Requests, w.Status, w.Reason))
		}
		out = append(out, fmt.Sprintf("%s %s: %s/%s %v", r.Method, r.Path, r.Status, r.Reason, windows))
	}
	return strings.Join(out, "; ")
}

// advanceCanary asks for the 50% → 100% step as a customer would.
func (f *routesFixture) advanceCanary(t *testing.T, c routeHealthCanary) ([]byte, int) {
	t.Helper()
	return doReq(t, f.h, f.key, http.MethodPost, "/v1/deployments/"+c.candidate.ID+"/canary/advance",
		api.AdvanceCanaryRequest{ExpectedStep: 2})
}

// TestE2E_Routes_CanaryHealthGateHoldsRegressionAndAdvancesHealthy drives the
// canary guard a customer configures: an enforce-mode gate on GET /checkout
// and GET /health. One canary answers /checkout with 500s while its stable
// deployment answers 200; the guard reports the route regressed, keeps
// /health healthy, and refuses to raise its traffic. The other canary is
// healthy on both routes; it advances to 100% and the gateway moves traffic.
func TestE2E_Routes_CanaryHealthGateHoldsRegressionAndAdvancesHealthy(t *testing.T) {
	f := newRoutesFixture(t, "canary-held")
	if f == nil {
		return
	}
	held := startRouteHealthCanary(t, f, f.app.Slug, f.app.ID, f.instance.DeploymentID, f.instance.ID, http.StatusInternalServerError)
	app, stable, stableInstance := createRoutesApp(t, f, "canary-advanced")
	advanced := startRouteHealthCanary(t, f, app.Slug, app.ID, stable.ID, stableInstance.ID, http.StatusOK)

	// The gate's update time anchors the evidence: only windows that start
	// after it count.
	zero := int64(0)
	for _, c := range []routeHealthCanary{held, advanced} {
		var gate api.RouteHealthGate
		if status := f.api(t, http.MethodPut, "/v1/apps/"+c.slug+"/route-health/gate", api.SetRouteHealthGateRequest{
			Mode: "enforce", OnRegression: "hold", ExpectedRevision: &zero,
			Routes: []api.RouteHealthRoute{{Method: http.MethodGet, Path: "/checkout"}, {Method: http.MethodGet, Path: "/health"}},
		}, &gate); status != http.StatusOK {
			t.Fatalf("PUT %s route health gate: status=%d", c.slug, status)
		}
		if gate.Mode != "enforce" || gate.Revision != 1 || len(gate.Routes) != 2 {
			t.Fatalf("%s route health gate = %+v", c.slug, gate)
		}
	}

	// Each closed minute needs RouteHealthMinRequests per route per
	// deployment. The 50% split alternates deployments request by request,
	// so each route is asked twice in a row to reach both.
	var mu sync.Mutex
	served := map[string]int{}
	stop := make(chan struct{})
	var traffic sync.WaitGroup
	traffic.Add(1)
	go func() {
		defer traffic.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			for _, c := range []routeHealthCanary{held, advanced} {
				for _, path := range []string{"/checkout", "/checkout", "/health", "/health"} {
					status, body, err := gatewayGetNoFatal(f, c.host, path)
					key := fmt.Sprintf("%s %s %d %s", c.slug, path, status, body)
					if err != nil {
						key = fmt.Sprintf("%s %s error", c.slug, path)
					}
					mu.Lock()
					served[key]++
					mu.Unlock()
				}
			}
		}
	}()
	stopTraffic := sync.OnceFunc(func() { close(stop); traffic.Wait() })
	defer stopTraffic()
	servedSummary := func() string {
		mu.Lock()
		defer mu.Unlock()
		return fmt.Sprint(served)
	}
	f.waitTelemetry(t, http.MethodGet, "/checkout", 5)

	// Two closed one-minute windows after the anchor, plus the ingestion
	// allowance: at most three and a half minutes.
	var heldReport, advancedReport api.RouteHealthReport
	deadline := time.Now().Add(6 * time.Minute)
	for {
		heldReport, advancedReport = f.routeHealthReport(t, held), f.routeHealthReport(t, advanced)
		if routeHealthSettled(heldReport) && routeHealthSettled(advancedReport) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("route health did not settle within 6m:\n%s: %s\n%s: %s\ntraffic: %s", held.slug, routeHealthSummary(heldReport),
				advanced.slug, routeHealthSummary(advancedReport), servedSummary())
		}
		time.Sleep(10 * time.Second)
	}
	checkout, health := routeHealthFinding(heldReport, "/checkout"), routeHealthFinding(heldReport, "/health")
	if heldReport.StableDeploymentID != held.stable.ID || heldReport.Status != "regressed" ||
		checkout.Status != "regressed" || checkout.Reason != "consecutive_server_error_regression" || health.Status != "healthy" {
		t.Fatalf("%s report = %s, want /checkout regressed against %s and /health healthy", held.slug, routeHealthSummary(heldReport), held.stable.ID)
	}
	if advancedReport.StableDeploymentID != advanced.stable.ID || advancedReport.Status != "healthy" {
		t.Fatalf("%s report = %s, want healthy against %s", advanced.slug, routeHealthSummary(advancedReport), advanced.stable.ID)
	}

	// The regressed canary is held: traffic is not raised, and the decision
	// is kept in its route health history.
	raw, status := f.advanceCanary(t, held)
	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &problem)
	if status != http.StatusConflict || problem.Code != api.CodeRouteHealthBlocked || !strings.Contains(problem.Detail, "consecutive_server_error_regression") {
		t.Fatalf("advance %s: status=%d body=%s, want 409 %s for the /checkout regression", held.slug, status, raw, api.CodeRouteHealthBlocked)
	}
	if got, err := f.store.DeploymentByID(f.ctx, held.candidate.ID); err != nil || got.TrafficPercent != 50 || got.CanaryStep != 2 {
		t.Fatalf("%s canary after the hold: traffic=%d step=%d err=%v, want 50%% at step 2", held.slug, got.TrafficPercent, got.CanaryStep, err)
	}
	var history api.RouteHealthHistoryPage
	if status := f.api(t, http.MethodGet, "/v1/apps/"+held.slug+"/route-health/deployments/"+held.candidate.ID+"/history", nil, &history); status != http.StatusOK {
		t.Fatalf("GET %s route health history: status=%d", held.slug, status)
	}
	if len(history.Entries) == 0 || history.Entries[0].Decision.Status != "blocked" ||
		routeHealthFinding(history.Entries[0].Report, "/checkout").Status != "regressed" {
		t.Fatalf("%s route health history = %+v, want the blocked decision with /checkout regressed", held.slug, history.Entries)
	}

	// The healthy canary advances. A window that closes between the report
	// and the request can still be ingesting, which also holds; retry that.
	var advance api.CanaryAdvanceResponse
	for attempt := 1; ; attempt++ {
		raw, status = f.advanceCanary(t, advanced)
		if status == http.StatusOK {
			if err := json.Unmarshal(raw, &advance); err != nil {
				t.Fatalf("decode %s advance: %v body=%s", advanced.slug, err, raw)
			}
			break
		}
		if status != http.StatusConflict || !strings.Contains(string(raw), api.CodeRouteHealthBlocked) || attempt == 3 {
			t.Fatalf("advance %s: status=%d body=%s; report: %s", advanced.slug, status, raw, routeHealthSummary(f.routeHealthReport(t, advanced)))
		}
		time.Sleep(15 * time.Second)
	}
	if advance.Deployment.TrafficPercent != 100 || advance.RouteHealth == nil || advance.RouteHealth.Status != "allowed" {
		t.Fatalf("%s advance = traffic %d%%, route health %+v; want 100%% allowed", advanced.slug, advance.Deployment.TrafficPercent, advance.RouteHealth)
	}
	if got, err := f.store.DeploymentByID(f.ctx, advanced.stable.ID); err != nil || got.TrafficPercent != 0 {
		t.Fatalf("%s stable after the advance: traffic=%d err=%v, want 0%%", advanced.slug, got.TrafficPercent, err)
	}
	// The gateway follows: every request now reaches the candidate.
	consecutive := 0
	deadline = time.Now().Add(30 * time.Second)
	for consecutive < 10 {
		if time.Now().After(deadline) {
			t.Fatalf("%s: GET /health did not settle on the candidate within 30s; traffic: %s", advanced.slug, servedSummary())
		}
		if status, body, err := gatewayGetNoFatal(f, advanced.host, "/health"); err == nil && status == http.StatusOK && string(body) == "health:candidate" {
			consecutive++
		} else {
			consecutive = 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	stopTraffic()
	t.Logf("traffic: %s", servedSummary())
}
