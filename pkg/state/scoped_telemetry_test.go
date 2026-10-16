package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScopedWorkerObservationMem(t *testing.T) {
	scopedWorkerObservationSuite(t, state.NewMemStore(), state.DefaultLocalNodeName)
}
func TestScopedWorkerObservationPG(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	scopedWorkerObservationSuite(t, s, resolveDefaultLocal(t, ctx, s))
}
func scopedWorkerObservationSuite(t *testing.T, s state.Store, node string) {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "scoped-observation@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "scoped-observation", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(scope string) state.Deployment {
		d, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, Status: state.DeployPending, AppID: app.ID, Scope: scope, ImageDigest: "sha256:" + uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := seed("default")
	old, err := s.CreateInstanceWithMode(ctx, app.ID, base.ID, "draining", 256, node, uuid.NewString(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	target := seed("default")
	if _, err := s.CreateInstanceWithMode(ctx, app.ID, target.ID, "running", 256, node, uuid.NewString(), "worker"); err != nil {
		t.Fatal(err)
	}
	neighbor := seed("staging")
	if _, err := s.CreateInstanceWithMode(ctx, app.ID, neighbor.ID, "running", 256, node, uuid.NewString(), "worker"); err != nil {
		t.Fatal(err)
	}
	reader := s.(state.WorkerRolloutObservationStore)
	observation, err := reader.ReadWorkerRolloutObservation(ctx, acct.ID, app.ID, target.ID)
	if err != nil || observation.SelectedDeploymentID != target.ID || observation.RetiredResidents != 1 || observation.Running != 1 || observation.GenerationConverged {
		t.Fatalf("mixed scope/residency: %+v %v", observation, err)
	}
	if err := s.UpdateInstanceState(ctx, old.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	observation, err = reader.ReadWorkerRolloutObservation(ctx, acct.ID, app.ID, target.ID)
	if err != nil || !observation.GenerationConverged || observation.QueueProcessingConfirmed {
		t.Fatalf("convergence/drain claim: %+v %v", observation, err)
	}
	if _, err := reader.ReadWorkerRolloutObservation(ctx, uuid.NewString(), app.ID, target.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account observation accepted")
	}
}

func TestScopedTelemetryPGFiltersBeforeLimitAndExcludesUnattributed(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, target, serving := checkedRollbackFixture(t, s, false)
	now := time.Now().UTC()
	// Add more neighboring route names than the route page permits.
	for i := 0; i < api.RouteMetricsPerAppCap+2; i++ {
		productionMonitorExec(t, pool, "INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,received_at) VALUES($1,$2,$3,$4,'GET',200,1,$5)", acct.ID, app.ID, serving.ID, "/neighbor-"+uuid.NewString(), now)
	}
	other, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, Status: state.DeployPending, AppID: app.ID, Scope: "staging", ImageDigest: "sha256:stage"})
	if err != nil {
		t.Fatal(err)
	}
	productionMonitorExec(t, pool, "INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,received_at) VALUES($1,$2,$3,'/stage','GET',200,1,$4)", acct.ID, app.ID, other.ID, now)
	routes, err := s.ListScopedObservedRoutes(ctx, acct.ID, app.ID, "staging", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil || len(routes) != 1 || routes[0] != "GET /stage" {
		t.Fatalf("scope filtered after limiting: %v %v", routes, err)
	}
	if _, err := s.ListScopedObservedRoutes(ctx, uuid.NewString(), app.ID, "staging", now.Add(-time.Minute), now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("route ownership not checked")
	}
	operation, err := s.CreateCheckedRollback(ctx, acct.ID, app.ID, target.ID, serving.ID, "checked")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListScopedPendingRollbacks(ctx, acct.ID, app.ID, "staging")
	if err != nil || len(rows) != 0 {
		t.Fatal("default recovery leaked into staging")
	}
	rows, err = s.ListScopedPendingRollbacks(ctx, acct.ID, app.ID, "default")
	if err != nil || len(rows) != 1 || rows[0].ID != operation.ID {
		t.Fatal("scoped recovery missing")
	}
	seed := func(wake, scope, status string) {
		payload := `{"app_id":"` + app.ID + `","wake_id":"` + wake + `"`
		if scope != "" {
			payload += `,"scope":"` + scope + `"`
		}
		payload += `}`
		productionMonitorExec(t, pool, "INSERT INTO notification_outbox(channel,payload,state,last_error) VALUES('runtime_config_restart',$1,$2,'PRIVATE reason=requests_active')", payload, status)
	}
	wake := uuid.NewString()
	seed(wake, "staging", "pending")
	seed(wake, "", "delivered")
	expected := uuid.NewString()
	seed(expected, "staging", "pending")
	seed(uuid.NewString(), "default", "pending")
	seed(uuid.NewString(), "", "pending")
	restarts, err := s.ListScopedPendingRestarts(ctx, acct.ID, app.ID, "staging")
	if err != nil || len(restarts) != 1 || restarts[0].WakeID != expected || strings.Contains(restarts[0].FailureReason, "PRIVATE") {
		t.Fatalf("latest handoff or scope/redaction lost: %+v %v", restarts, err)
	}
}
