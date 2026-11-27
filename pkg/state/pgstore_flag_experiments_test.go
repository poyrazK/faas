//go:build !no_pg

package state_test

import (
	"context"
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

// flagExperimentPgFixture is a project environment with a live app, a way to
// add verified customers and a way to add request telemetry carrying the
// "checkout" flag's "weighted" rule decision.
type flagExperimentPgFixture struct {
	s        *state.PgStore
	ctx      context.Context
	acct     string
	project  string
	env      string
	dep      string
	tenant   func(name string) string
	exposure func(customer, variant string, at time.Time, status, latencyMS int32)
	// decision records a request whose checkout decision matched rule (or
	// the default, with rule "").
	decision func(customer, rule string, at time.Time)
	// held records a request whose checkout decision the environment
	// holdout served control on the weighted rule (ADR 20261119173702 §2).
	held func(customer string, at time.Time)
}

func newFlagExperimentPgFixture(t *testing.T, slug string) flagExperimentPgFixture {
	t.Helper()
	s, pool, ctx := pgStoreWithPool(t)
	acct, appID, depID := seedLiveDeploy(t, s, ctx)
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct, Slug: slug, ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
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
	f := flagExperimentPgFixture{s: s, ctx: ctx, acct: acct, project: project.ID, env: env.ID, dep: depID}
	f.tenant = func(name string) string {
		tn, _, err := s.CreatePlatformTenant(ctx, acct, name, name, 100)
		if err != nil {
			t.Fatalf("CreatePlatformTenant: %v", err)
		}
		return tn.ID
	}
	insert := func(customer, evidence string, at time.Time, status, latencyMS int32) {
		t.Helper()
		var tid, acctID, appPg, depPg pgtype.UUID
		_ = tid.Scan(customer)
		_ = acctID.Scan(acct)
		_ = appPg.Scan(appID)
		_ = depPg.Scan(depID)
		if err := s.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: acctID, AppID: appPg, DeploymentID: depPg, Route: "/checkout", Method: "GET", Status: status, LatencyMs: latencyMS,
			ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true}, Count: 1, PlatformTenantID: tid, FlagEvidenceJson: evidence,
			UaFamily: "__unknown__", ReferrerHost: "__unknown__", Country: "__unknown__",
		}); err != nil {
			t.Fatalf("InsertRequestTelemetry: %v", err)
		}
	}
	f.exposure = func(customer, variant string, at time.Time, status, latencyMS int32) {
		t.Helper()
		insert(customer, fmt.Sprintf(`[{"flag":"checkout","type":"variant","value":%q,"rule_id":"weighted","config_version":1,"reason":"rule_match","source":"configuration","used":false}]`, variant), at, status, latencyMS)
	}
	f.decision = func(customer, rule string, at time.Time) {
		t.Helper()
		if rule == "" {
			insert(customer, `[{"flag":"checkout","value":false,"config_version":1,"reason":"default","source":"configuration","used":false}]`, at, 200, 10)
			return
		}
		insert(customer, fmt.Sprintf(`[{"flag":"checkout","value":true,"rule_id":%q,"config_version":1,"reason":"rule_match","source":"configuration","used":false}]`, rule), at, 200, 10)
	}
	f.held = func(customer string, at time.Time) {
		t.Helper()
		insert(customer, `[{"flag":"checkout","type":"variant","value":"legacy","rule_id":"weighted","config_version":1,"reason":"holdout","source":"configuration","used":false}]`, at, 200, 10)
	}
	return f
}

// ADR 20261115163113 §1, §3: an experiment's units come from request
// telemetry evidence for its rule, keep the variant of their first exposure,
// are crossed when seen with another variant, and convert on a metric event
// sent at or after their first exposure.
func TestPg_FlagExperimentUnitsFromTelemetry(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-exp")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b, c := f.tenant("a"), f.tenant("b"), f.tenant("c")
	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)

	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "checkout-v2", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MixingLift: 0.02, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 2)
	if err != nil {
		t.Fatalf("CreateFlagExperiment: %v", err)
	}
	if _, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "checkout-v2", FlagKey: "checkout", RuleID: "other", Control: "legacy", MetricEvent: "x", MixingLift: 0.02, CreatedBy: acct}, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate key = %v; want ErrConflict", err)
	}
	if _, err := s.StopFlagExperiment(ctx, f.env, "checkout-v2", start, flags.StopManual, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stopping a draft = %v; want ErrNotFound", err)
	}
	x, err = s.StartFlagExperiment(ctx, f.env, "checkout-v2", map[string]int{"legacy": 50, "new": 50}, [2]int{}, start, 1)
	if err != nil || x.Status != flags.ExperimentRunning || x.StartedAt == nil {
		t.Fatalf("StartFlagExperiment = %+v, %v", x, err)
	}
	if ok, err := s.DeleteFlagExperiment(ctx, f.env, "checkout-v2"); err != nil || ok {
		t.Fatalf("deleting a running experiment = %v, %v; want refused", ok, err)
	}

	f.exposure(a, "legacy", start.Add(10*time.Minute), 200, 10)
	f.exposure(b, "new", start.Add(10*time.Minute), 200, 10)
	f.exposure(c, "legacy", start.Add(10*time.Minute), 200, 10)
	f.exposure(c, "new", start.Add(20*time.Minute), 200, 10)
	f.exposure(a, "legacy", start.Add(30*time.Minute), 200, 10) // a second exposure keeps the first

	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env}
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
	arms, crossed, err := s.FlagExperimentArms(ctx, x, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []flags.Arm{{Variant: "legacy", Exposed: 1, Converted: 0}, {Variant: "new", Exposed: 1, Converted: 1}}
	if !slices.Equal(arms, want) || crossed != 1 {
		t.Fatalf("arms = %+v crossed %d; want %+v and 1 crossed", arms, crossed, want)
	}

	if err := s.SaveFlagExperimentProgress(ctx, x.ID, start.Add(time.Hour), map[string]float64{"new": 0.4}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFlagExperiment(ctx, f.env, "checkout-v2")
	if err != nil || !got.TelemetryCursor.Equal(start.Add(time.Hour)) || got.MinPValues["new"] != 0.4 {
		t.Fatalf("progress = %+v, %v", got, err)
	}
	breach := map[string]any{"guardrail": flags.GuardrailServerErrorRate, "variant": "new"}
	if got, err = s.StopFlagExperiment(ctx, f.env, "checkout-v2", start.Add(2*time.Hour), flags.StopGuardrailBreach, breach); err != nil ||
		got.Status != flags.ExperimentStopped || got.StopReason != flags.StopGuardrailBreach || got.Breach["variant"] != "new" {
		t.Fatalf("StopFlagExperiment = %+v, %v", got, err)
	}
	if ok, err := s.DeleteFlagExperiment(ctx, f.env, "checkout-v2"); err != nil || !ok {
		t.Fatalf("DeleteFlagExperiment = %v, %v", ok, err)
	}
}

// A started experiment reads the exposures of its start minute: gateways
// stamp minute-collapsed telemetry at the minute's start, before the start
// instant (prod 2026-11-21: a journey's experiment counted no exposures).
func TestPg_FlagExperimentCountsStartMinuteExposures(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-exp-minute")
	s, ctx, acct := f.s, f.ctx, f.acct
	a := f.tenant("a")
	minute := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	start := minute.Add(13 * time.Second)
	if _, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "checkout-minute", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MixingLift: 0.02, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 2); err != nil {
		t.Fatalf("CreateFlagExperiment: %v", err)
	}
	x, err := s.StartFlagExperiment(ctx, f.env, "checkout-minute", map[string]int{"legacy": 50, "new": 50}, [2]int{}, start, 1)
	if err != nil {
		t.Fatalf("StartFlagExperiment: %v", err)
	}
	if want := state.FlagExperimentStartCursor(start); !x.TelemetryCursor.Equal(want) || !x.StartedAt.Equal(start) {
		t.Fatalf("started at %v cursor %v; want %v and %v", x.StartedAt, x.TelemetryCursor, start, want)
	}
	f.exposure(a, "new", minute, 200, 10) // a request at start+2s, collapsed into its minute
	running, err := s.ListRunningFlagExperiments(ctx, "", 10)
	if err != nil || len(running) != 1 {
		t.Fatalf("running = %+v, %v", running, err)
	}
	if read, err := s.AdvanceFlagExperimentUnits(ctx, running[0], running[0].TelemetryCursor, minute.Add(5*time.Minute), 1000); err != nil || read != 1 {
		t.Fatalf("AdvanceFlagExperimentUnits from the start cursor read %d, %v; want the start minute's exposure", read, err)
	}
}

// ADR 20261116190208 §1, §2: units carry request, server error and slow
// request counts from telemetry, each arm keeps a latency histogram, the
// latency threshold is fixed once, and a mean metric sums event values from
// first exposure, folding closed days into the unit exactly once.
func TestPg_FlagExperimentGuardrailCountsAndMeanValues(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-guard")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b := f.tenant("a"), f.tenant("b")
	start := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)

	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "revenue", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MetricKind: flags.ExperimentMetricMean, MixingLift: 0.1, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct,
		Guardrails: flags.GuardrailSettings{ServerErrorRate: flags.GuardrailSetting{MaxIncrease: 0.005}, P95Latency: flags.GuardrailSetting{Disabled: true}}}, 2)
	if err != nil || x.MetricKind != flags.ExperimentMetricMean || !x.Guardrails.P95Latency.Disabled || x.Guardrails.ServerErrorRate.MaxIncrease != 0.005 {
		t.Fatalf("CreateFlagExperiment = %+v, %v", x, err)
	}
	if _, err := s.StartFlagExperiment(ctx, f.env, "revenue", x.Weights, [2]int{}, start, 1); err != nil {
		t.Fatal(err)
	}

	f.exposure(a, "legacy", start.Add(time.Minute), 200, 100)
	f.exposure(a, "legacy", start.Add(2*time.Minute), 503, 900)
	f.exposure(b, "new", start.Add(time.Minute), 200, 50)
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env}
	if _, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 100, MaxNames: 10, Now: time.Now(), Events: []state.FlagConversionEvent{
		{Name: "order_completed", CustomerID: a, At: start.Add(30 * time.Minute), Value: 1500},
		{Name: "order_completed", CustomerID: b, At: start.Add(40 * time.Minute), Value: 2500},
		{Name: "order_completed", CustomerID: b, At: start.Add(50 * time.Minute), Value: 500},
	}}); err != nil {
		t.Fatalf("IngestFlagEvents: %v", err)
	}

	running, err := s.ListRunningFlagExperiments(ctx, "", 10)
	if err != nil || len(running) != 1 {
		t.Fatalf("running = %+v, %v", running, err)
	}
	if _, err := s.AdvanceFlagExperimentUnits(ctx, running[0], start, start.Add(time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	errs, slow, err := s.FlagExperimentGuardrailArms(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantErrs := []flags.RatioArm{
		{Variant: "legacy", Units: 1, Num: 1, Den: 2, NumSq: 1, DenSq: 4, NumDen: 2},
		{Variant: "new", Units: 1, Num: 0, Den: 1, DenSq: 1},
	}
	if !slices.Equal(errs, wantErrs) {
		t.Fatalf("error arms = %+v; want %+v", errs, wantErrs)
	}
	for _, arm := range slow {
		if arm.Den != 0 || arm.Units != 0 {
			t.Fatalf("slow arms before a threshold = %+v; want no latency-tested requests", slow)
		}
	}
	hist, err := s.FlagExperimentLatency(ctx, x.ID)
	if err != nil || hist["legacy"][flags.LatencyBucket(100)] != 1 || hist["legacy"][flags.LatencyBucket(900)] != 1 || hist["new"][flags.LatencyBucket(50)] != 1 {
		t.Fatalf("latency histogram = %v, %v", hist, err)
	}

	if err := s.SetFlagExperimentLatencyThreshold(ctx, x.ID, 200); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFlagExperimentLatencyThreshold(ctx, x.ID, 999); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFlagExperiment(ctx, f.env, "revenue")
	if err != nil || got.LatencyThresholdMS == nil || *got.LatencyThresholdMS != 200 {
		t.Fatalf("latency threshold = %+v, %v; want fixed once at 200", got.LatencyThresholdMS, err)
	}
	running[0].LatencyThresholdMS = got.LatencyThresholdMS
	f.exposure(a, "legacy", start.Add(70*time.Minute), 200, 500)
	if _, err := s.AdvanceFlagExperimentUnits(ctx, running[0], start.Add(time.Hour), start.Add(2*time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	if _, slow, err = s.FlagExperimentGuardrailArms(ctx, x.ID); err != nil || slow[0].Variant != "legacy" || slow[0].Num != 1 || slow[0].Den != 1 {
		t.Fatalf("slow arms after the threshold = %+v, %v; want legacy 1 of 1", slow, err)
	}

	arms, _, err := s.FlagExperimentArms(ctx, got, 100)
	want := []flags.Arm{{Variant: "legacy", Exposed: 1, Converted: 1, Sum: 1500, SumSq: 1500 * 1500}, {Variant: "new", Exposed: 1, Converted: 1, Sum: 3000, SumSq: 3000 * 3000}}
	if err != nil || !slices.Equal(arms, want) {
		t.Fatalf("mean arms = %+v, %v; want %+v", arms, err, want)
	}
	// Folding the open days into the units leaves the results unchanged, and
	// folding again adds nothing.
	through := start.Add(48 * time.Hour)
	for i := 0; i < 2; i++ {
		got, err = s.GetFlagExperiment(ctx, f.env, "revenue")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FoldFlagExperimentValues(ctx, got, through); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = s.GetFlagExperiment(ctx, f.env, "revenue")
	if got.ValueThroughDay == nil {
		t.Fatal("value_through_day not advanced")
	}
	if arms, _, err = s.FlagExperimentArms(ctx, got, 100); err != nil || !slices.Equal(arms, want) {
		t.Fatalf("mean arms after folding = %+v, %v; want %+v", arms, err, want)
	}
}
