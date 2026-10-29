// routes_monitor_rollback_e2e_test.go — KVM-free acceptance for the
// production route monitor's automatic rollback (ADR-845,
// ADR-20261028110044): a monitor with on_violation rollback records the
// healthy serving deployment, and when a new release breaks a route's error
// budget within the rollback window the incident requests one checked
// rollback to that healthy deployment.
//
// Deployments are seeded at the state boundary, as in the other routes tests.

//go:build !no_pg

package e2e_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

func TestE2E_Routes_ProductionMonitorRollsBack(t *testing.T) {
	f := newRoutesFixture(t, "routes-monitor-rollback")
	if f == nil {
		return
	}
	stable := f.instance
	f.respond("/checkout", http.StatusOK, "checkout ok")
	f.respond("/health", http.StatusOK, "ok")

	slug := f.app.Slug
	budget := int64(100) // 1% 5xx
	zero := int64(0)
	var config api.RouteMonitorConfig
	if status := f.api(t, http.MethodPut, "/v1/apps/"+slug+"/route-monitor", api.SetRouteMonitorRequest{
		Enabled: true, OnViolation: "rollback", ExpectedRevision: &zero,
		Routes: []api.RouteMonitorRoute{
			{Method: http.MethodGet, Path: "/checkout", Max5xxRateBPS: &budget},
			{Method: http.MethodGet, Path: "/health", Max5xxRateBPS: &budget},
		},
	}, &config); status != http.StatusOK {
		t.Fatalf("PUT route monitor: status=%d", status)
	}
	if config.OnViolation != "rollback" {
		t.Fatalf("route monitor config = %+v, want on_violation rollback", config)
	}

	ctx, stop := context.WithCancel(context.Background())
	var traffic sync.WaitGroup
	traffic.Add(1)
	go func() {
		defer traffic.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, path := range []string{"/checkout", "/health"} {
					_, _, _ = gatewayReqNoFatal(f, path)
				}
			}
		}
	}()
	defer func() { stop(); traffic.Wait() }()
	f.waitTelemetry(t, http.MethodGet, "/checkout", 5)

	// The monitor worker records the serving deployment as the healthy
	// baseline when it evaluates a healthy report; wait for one healthy
	// report and a worker evaluation after it.
	waitRouteMonitorReport(t, f, "healthy", f.instance.DeploymentID, 5*time.Minute)
	before, _ := f.apidMetric(t, `apid_route_monitor_evaluations_total{outcome="evaluated"}`)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if after, _ := f.apidMetric(t, `apid_route_monitor_evaluations_total{outcome="evaluated"}`); after > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the monitor worker evaluated nothing after the healthy report within 3m")
		}
		time.Sleep(5 * time.Second)
	}

	// Release a version whose checkout fails.
	release, instance := createNormalPathLiveDeployment(t, f.normalPathFixture, f.app.ID, "v2")
	f.vmmd.SetVersion(instance.ID, "v2")
	f.vmmd.SetResponseForPath(instance.ID, "/checkout", e2etest.FakeResponse{Status: http.StatusInternalServerError, Body: []byte("checkout failed")})
	f.vmmd.SetResponseForPath(instance.ID, "/health", e2etest.FakeResponse{Status: http.StatusOK, Body: []byte("ok")})
	notifyRoutesApp(t, f, f.app.ID, release.ID, instance.ID)
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v2\n", 30*time.Second)

	var incident api.RouteMonitorIncident
	deadline = time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		var page api.RouteMonitorIncidentPage
		if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/route-monitor/incidents", nil, &page); status != http.StatusOK {
			t.Fatalf("GET incidents: status=%d", status)
		}
		if len(page.Incidents) > 0 {
			if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/route-monitor/incidents/"+page.Incidents[0].ID, nil, &incident); status != http.StatusOK {
				t.Fatalf("GET incident: status=%d", status)
			}
			if incident.Rollback != nil && incident.Rollback.Status != "claimed" {
				break
			}
		}
		time.Sleep(10 * time.Second)
	}
	rb := incident.Rollback
	if incident.DeploymentID != release.ID || rb == nil || rb.Status != "requested" || rb.TargetDeploymentID != stable.DeploymentID || rb.OperationID == "" {
		t.Fatalf("incident %s on %s rollback = %+v; want a requested rollback from %s to %s", incident.ID, incident.DeploymentID, rb, release.ID, stable.DeploymentID)
	}
	var operation api.RollbackOperation
	if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/rollbacks/"+rb.OperationID, nil, &operation); status != http.StatusOK ||
		operation.TargetDeploymentID != stable.DeploymentID || operation.CurrentDeploymentID != release.ID {
		t.Fatalf("checked rollback %s: status=%d operation=%+v", rb.OperationID, status, operation)
	}
}

// waitRouteMonitorReport waits until the monitor's report on deploymentID
// has status.
func waitRouteMonitorReport(t *testing.T, f *routesFixture, status, deploymentID string, timeout time.Duration) {
	t.Helper()
	var report api.RouteMonitorReport
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if code := f.api(t, http.MethodGet, "/v1/apps/"+f.app.Slug+"/route-monitor/report", nil, &report); code != http.StatusOK {
			t.Fatalf("GET route monitor report: status=%d", code)
		}
		if report.Status == status && report.DeploymentID == deploymentID {
			return
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("route monitor report = %s on %s (%s) after %s, want %s on %s", report.Status, report.DeploymentID, report.Reason, timeout, status, deploymentID)
}
