//go:build !no_pg

package state_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR 20261115163113 §1, §3: an experiment's units come from request
// telemetry evidence for its rule, keep the variant of their first exposure,
// are crossed when seen with another variant, and convert on a metric event
// sent at or after their first exposure.
func TestPg_FlagExperimentUnitsFromTelemetry(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, appID, depID := seedLiveDeploy(t, s, ctx)
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-exp", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET project_id = $1 WHERE id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app: %v", err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatalf("environment: %v", err)
	}
	tenant := func(name string) string {
		tn, _, err := s.CreatePlatformTenant(ctx, acct, name, name, 100)
		if err != nil {
			t.Fatalf("CreatePlatformTenant: %v", err)
		}
		return tn.ID
	}
	a, b, c := tenant("a"), tenant("b"), tenant("c")
	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)

	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID,
		Key: "checkout-v2", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MixingLift: 0.02, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 2)
	if err != nil {
		t.Fatalf("CreateFlagExperiment: %v", err)
	}
	if _, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID,
		Key: "checkout-v2", FlagKey: "checkout", RuleID: "other", Control: "legacy", MetricEvent: "x", MixingLift: 0.02, CreatedBy: acct}, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate key = %v; want ErrConflict", err)
	}
	if _, err := s.StopFlagExperiment(ctx, env.ID, "checkout-v2", start); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stopping a draft = %v; want ErrNotFound", err)
	}
	x, err = s.StartFlagExperiment(ctx, env.ID, "checkout-v2", map[string]int{"legacy": 50, "new": 50}, start, 1)
	if err != nil || x.Status != flags.ExperimentRunning || x.StartedAt == nil {
		t.Fatalf("StartFlagExperiment = %+v, %v", x, err)
	}
	if ok, err := s.DeleteFlagExperiment(ctx, env.ID, "checkout-v2"); err != nil || ok {
		t.Fatalf("deleting a running experiment = %v, %v; want refused", ok, err)
	}

	exposure := func(customer, variant string, at time.Time) {
		t.Helper()
		evidence := fmt.Sprintf(`[{"flag":"checkout","type":"variant","value":%q,"rule_id":"weighted","config_version":1,"reason":"rule_match","source":"configuration","used":false}]`, variant)
		var tid pgtype.UUID
		_ = tid.Scan(customer)
		var acctID, appPg, depPg pgtype.UUID
		_ = acctID.Scan(acct)
		_ = appPg.Scan(appID)
		_ = depPg.Scan(depID)
		if err := s.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: acctID, AppID: appPg, DeploymentID: depPg, Route: "/checkout", Method: "GET", Status: 200,
			ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true}, Count: 1, PlatformTenantID: tid, FlagEvidenceJson: evidence,
			UaFamily: "__unknown__", ReferrerHost: "__unknown__", Country: "__unknown__",
		}); err != nil {
			t.Fatalf("InsertRequestTelemetry: %v", err)
		}
	}
	exposure(a, "legacy", start.Add(10*time.Minute))
	exposure(b, "new", start.Add(10*time.Minute))
	exposure(c, "legacy", start.Add(10*time.Minute))
	exposure(c, "new", start.Add(20*time.Minute))
	exposure(a, "legacy", start.Add(30*time.Minute)) // a second exposure keeps the first

	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID}
	if _, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 100, MaxNames: 10, Now: time.Now(), Events: []state.FlagConversionEvent{
		{Name: "order_completed", CustomerID: b, At: start.Add(15 * time.Minute)},
		{Name: "order_completed", CustomerID: a, At: start.Add(5 * time.Minute)}, // before a's exposure
	}}); err != nil {
		t.Fatalf("IngestFlagEvents: %v", err)
	}

	running, err := s.ListRunningFlagExperiments(ctx, "", 10)
	if err != nil || len(running) != 1 || running[0].EnvironmentSlug != "production" || running[0].Plan == "" {
		t.Fatalf("running = %+v, %v", running, err)
	}
	read, err := s.AdvanceFlagExperimentUnits(ctx, running[0], start, start.Add(time.Hour), 1000)
	if err != nil || read != 4 {
		t.Fatalf("AdvanceFlagExperimentUnits read %d, %v; want 4 customer-variant rows", read, err)
	}
	// A second pass over the same window changes nothing.
	if _, err := s.AdvanceFlagExperimentUnits(ctx, running[0], start, start.Add(time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	arms, crossed, err := s.FlagExperimentArms(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []flags.Arm{{Variant: "legacy", Exposed: 1, Converted: 0}, {Variant: "new", Exposed: 1, Converted: 1}}
	if !slices.Equal(arms, want) || crossed != 1 {
		t.Fatalf("arms = %+v crossed %d; want %+v and 1 crossed", arms, crossed, want)
	}

	if err := s.SaveFlagExperimentProgress(ctx, x.ID, start.Add(time.Hour), map[string]float64{"new": 0.4}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFlagExperiment(ctx, env.ID, "checkout-v2")
	if err != nil || !got.TelemetryCursor.Equal(start.Add(time.Hour)) || got.MinPValues["new"] != 0.4 {
		t.Fatalf("progress = %+v, %v", got, err)
	}
	if got, err = s.StopFlagExperiment(ctx, env.ID, "checkout-v2", start.Add(2*time.Hour)); err != nil || got.Status != flags.ExperimentStopped {
		t.Fatalf("StopFlagExperiment = %+v, %v", got, err)
	}
	if ok, err := s.DeleteFlagExperiment(ctx, env.ID, "checkout-v2"); err != nil || !ok {
		t.Fatalf("DeleteFlagExperiment = %v, %v", ok, err)
	}
}
