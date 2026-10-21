// adr: 1252
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScopedRouteMonitorPG(t *testing.T) {
	pool, s, a, app, _, d := productionMonitorPG(t)
	ctx := t.Context()
	project, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "scoped-budget", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	productionMonitorExec(t, pool, "UPDATE apps SET project_id=$1 WHERE id=$2", project.ID, app.ID)
	app.ProjectID = project.ID
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:staged", Scope: "staging", ExpectedReview: &review, ReviewedSettings: &settings, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=100,created_at=clock_timestamp()-interval '2 hours',rollout_completed_at=clock_timestamp()-interval '1 hour',canary_step_started_at=clock_timestamp()-interval '1 hour' WHERE id=$1", target.ID)
	productionMonitorExec(t, pool, "UPDATE route_monitors SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID)
	// Neighbor traffic violates both budgets, while weighted staging traffic
	// satisfies them. Exact deployment attribution prevents contamination.
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 500, 500, 1000, false)
	productionMonitorTraffic(t, pool, a, app, target.ID, "POST", "/checkout", 200, 100, 50, false)
	r, err := s.ReadScopedRouteMonitor(ctx, a.ID, app.ID, "staging")
	if err != nil || r.Status != "healthy" || r.DeploymentID != target.ID || r.Customers != nil || r.CustomerGroupBy != "" {
		t.Fatalf("scoped healthy=%+v %v", r, err)
	}
	if len(r.Routes[0].PooledWindows) != 0 || r.Routes[0].Windows[0].Observed.Requests != 100 {
		t.Fatal("scoped observations reused neighboring or pooled data")
	}
	r, err = s.ReadScopedRouteMonitor(ctx, a.ID, app.ID, "default")
	if err != nil || r.Status != "violated" || r.DeploymentID != d.ID {
		t.Fatalf("default violated=%+v %v", r, err)
	}

	// A staging-only latency override changes only its scoped evaluation.
	budgets, err := s.GetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	proposal := api.SetEnvironmentRouteBudgetsRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &budgets.Revision, ExpectedAppRevision: &budgets.AppRevision, Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 20}}}
	saved, err := s.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", proposal)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := s.ReadScopedRouteBudgetReport(ctx, a.ID, app.ID, "staging")
	if err != nil || scoped.BudgetSource != "environment_policy" || scoped.BudgetRevision != saved.Revision || scoped.Report.Status != "unknown" || scoped.Report.Routes[0].Windows[0].LatencyReason != "observation_window_not_elapsed" {
		t.Fatalf("override reset=%+v %v", scoped, err)
	}
	productionMonitorExec(t, pool, "UPDATE app_environment_route_budgets SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1 AND scope='staging'", app.ID)
	scoped, err = s.ReadScopedRouteBudgetReport(ctx, a.ID, app.ID, "staging")
	if err != nil || scoped.Report.Status != "violated" || scoped.Report.Routes[0].Route.MaxP95MS != 20 {
		t.Fatalf("override not evaluated=%+v %v", scoped, err)
	}
	neighbor, err := s.ReadScopedRouteBudgetReport(ctx, a.ID, app.ID, "default")
	if err != nil || neighbor.BudgetSource != "app_policy" || neighbor.Report.Routes[0].Route.MaxP95MS != 300 {
		t.Fatal("staging threshold changed default policy")
	}
	proposal.RequestID = uuid.NewString()
	proposal.ExpectedRevision = &saved.Revision
	proposal.Inherit, proposal.Enabled, proposal.Routes = true, false, []api.RouteMonitorRoute{}
	if _, err := s.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", proposal); err != nil {
		t.Fatal(err)
	}
	scoped, err = s.ReadScopedRouteBudgetReport(ctx, a.ID, app.ID, "staging")
	if err != nil || scoped.BudgetSource != "app_policy" || scoped.Report.Routes[0].Windows[0].LatencyReason != "observation_window_not_elapsed" {
		t.Fatal("return to inheritance reused old windows")
	}
	productionMonitorExec(t, pool, "UPDATE app_environment_route_budgets SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1 AND scope='staging'", app.ID)
	if _, err := s.ReadScopedRouteMonitor(ctx, uuid.NewString(), app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross account read=%v", err)
	}
	productionMonitorExec(t, pool, "UPDATE request_telemetry SET received_at=received_at-interval '10 minutes' WHERE deployment_id=$1", target.ID)
	r, err = s.ReadScopedRouteMonitor(ctx, a.ID, app.ID, "staging")
	if err != nil || r.Status != "unknown" || r.Routes[0].Windows[0].ErrorReason != "insufficient_requests" {
		t.Fatalf("stale became healthy=%+v %v", r, err)
	}
	productionMonitorTraffic(t, pool, a, app, target.ID, "POST", "/checkout", 200, 1, 50, false)
	r, err = s.ReadScopedRouteMonitor(ctx, a.ID, app.ID, "staging")
	if err != nil || r.Status != "unknown" {
		t.Fatal("sparse observations claimed health")
	}
	productionMonitorExec(t, pool, "UPDATE route_monitors SET updated_at=clock_timestamp() WHERE app_id=$1", app.ID)
	r, err = s.ReadScopedRouteMonitor(ctx, a.ID, app.ID, "staging")
	if err != nil || r.Routes[0].Windows[0].ErrorReason != "observation_window_not_elapsed" {
		t.Fatal("new policy reused old observations")
	}
	// Recreated environment identity cannot inherit a previous incarnation's
	// deployment binding, even if the same slug is registered again.
	productionMonitorExec(t, pool, "DELETE FROM project_environments WHERE id=$1", env.ID)
	if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	r, err = s.ReadScopedRouteMonitor(ctx, a.ID, app.ID, "staging")
	if err != nil || r.Status != "unknown" || r.DeploymentID != "" {
		t.Fatalf("recreated environment inherited telemetry=%+v %v", r, err)
	}
	// Current evaluation windows retain the shared ingestion allowance.
	for _, w := range routehealth.Windows(r.CheckedAt) {
		if !w.End.Before(time.Now()) {
			t.Fatal("monitor selected an open window")
		}
	}
	if strings.Contains(r.Reason, "rollback") {
		t.Fatal("scoped read claimed a recovery action")
	}
}
