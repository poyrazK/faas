//go:build !no_pg

package state_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261119102741 §1–2: funnel units count once their deadline passes,
// conversions are marked from analytics, and CUPED sums use each unit's
// pre-period value.
func TestPg_FlagExperimentFunnelUnitsAndCuped(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-funnel")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b := f.tenant("a"), f.tenant("b")
	start := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	def := flags.MetricDefinition{Steps: []flags.MetricStep{{Event: "signup"}, {Event: "order_completed"}}, Window: "2h"}

	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "onboarding", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MetricKind: flags.ExperimentMetricFunnel, MetricDefinition: def, Cuped: true,
		MixingLift: 0.02, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 2)
	if err != nil || x.MetricKind != flags.ExperimentMetricFunnel || !x.Cuped || len(x.MetricDefinition.Steps) != 2 || x.MetricDefinition.Window != "2h" {
		t.Fatalf("CreateFlagExperiment = %+v, %v", x, err)
	}
	if x, err = s.StartFlagExperiment(ctx, f.env, "onboarding", x.Weights, [2]int{}, start, 1); err != nil {
		t.Fatal(err)
	}
	f.exposure(a, "legacy", start.Add(10*time.Minute), 200, 10)
	f.exposure(b, "new", start.Add(10*time.Minute), 200, 10)
	running, err := s.ListRunningFlagExperiments(ctx, "", 10)
	if err != nil || len(running) != 1 || running[0].MetricDefinition.Window != "2h" || !running[0].Cuped {
		t.Fatalf("running = %+v, %v", running, err)
	}
	x = running[0]
	if _, err := s.AdvanceFlagExperimentUnits(ctx, x, start, start.Add(time.Hour), 1000); err != nil {
		t.Fatal(err)
	}

	now := start.Add(3 * time.Hour)
	cands, err := s.FlagExperimentConversionCandidates(ctx, x, "", now, now.Add(-25*time.Hour), 10)
	if err != nil || len(cands) != 2 {
		t.Fatalf("candidates = %+v, %v; want both units", cands, err)
	}
	if err := s.MarkFlagExperimentUnitConversions(ctx, x.ID, map[string]time.Time{b: start.Add(30 * time.Minute), a: start}); err != nil {
		t.Fatal(err)
	}
	if cands, err = s.FlagExperimentConversionCandidates(ctx, x, "", now, now.Add(-25*time.Hour), 10); err != nil || len(cands) != 1 || cands[0].CustomerID != a {
		t.Fatalf("candidates after marking = %+v, %v; want only a (a conversion before exposure is refused)", cands, err)
	}

	x.TelemetryCursor = start.Add(time.Hour)
	if arms, _, err := s.FlagExperimentArms(ctx, x, 100); err != nil || len(arms) != 0 {
		t.Fatalf("arms before deadlines = %+v, %v; want none counted", arms, err)
	}
	x.TelemetryCursor = now
	arms, _, err := s.FlagExperimentArms(ctx, x, 100)
	want := []flags.Arm{{Variant: "legacy", Exposed: 1}, {Variant: "new", Exposed: 1, Converted: 1}}
	if err != nil || !slices.Equal(arms, want) {
		t.Fatalf("arms = %+v, %v; want %+v", arms, err, want)
	}

	if _, pending, err := s.FlagExperimentCupedArms(ctx, x, 100); err != nil || pending != 2 {
		t.Fatalf("pending = %d, %v; want 2 before pre values", pending, err)
	}
	missing, err := s.FlagExperimentUnitsWithoutPreValue(ctx, x.ID, 10)
	if err != nil || len(missing) != 2 {
		t.Fatalf("units without pre value = %v, %v", missing, err)
	}
	if err := s.SetFlagExperimentPreValues(ctx, x.ID, map[string]float64{a: 1, b: 0}); err != nil {
		t.Fatal(err)
	}
	cuped, pending, err := s.FlagExperimentCupedArms(ctx, x, 100)
	wantCuped := []flags.CupedArm{{Variant: "legacy", N: 1, SumX: 1, SumX2: 1}, {Variant: "new", N: 1, SumY: 1, SumY2: 1}}
	if err != nil || pending != 0 || !slices.Equal(cuped, wantCuped) {
		t.Fatalf("cuped arms = %+v pending %d, %v; want %+v", cuped, pending, err, wantCuped)
	}

	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env}
	if _, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 100, MaxNames: 10, Now: time.Now(), Analytics: true, MaxPropertyKeys: 5,
		Events: []state.FlagConversionEvent{
			{Name: "signup", CustomerID: a, At: start.Add(-2 * time.Hour), Value: 7},
			{Name: "signup", CustomerID: a, At: start.Add(-time.Hour), Value: 3},
			{Name: "signup", CustomerID: b, At: start.Add(time.Minute)},
		}}); err != nil {
		t.Fatal(err)
	}
	totals, err := s.AnalyticsCustomerTotals(ctx, state.AnalyticsMatch{EnvironmentID: f.env, Name: "signup", From: start.AddDate(0, 0, -14), Until: start}, []string{a, b})
	if err != nil || totals[a] != (state.AnalyticsTotal{Events: 2, Value: 10}) || totals[b].Events != 0 {
		t.Fatalf("totals = %+v, %v", totals, err)
	}
}

// ADR 20261119102741 §3–4: cohorts are bounded per environment, their
// members come from analytics queries and stay inside the account, and the
// gateway lookup sees a customer's cohorts through its deployment.
func TestPg_AnalyticsCohorts(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "insights-cohorts")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b, c := f.tenant("a"), f.tenant("b"), f.tenant("c")
	now := time.Now().UTC().Truncate(time.Second)
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env}
	if _, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 100, MaxNames: 10, Now: now, Analytics: true, MaxPropertyKeys: 5,
		Events: []state.FlagConversionEvent{
			{Name: "signup", CustomerID: a, At: now.Add(-3 * time.Hour), Properties: map[string]string{"plan": "pro"}},
			{Name: "signup", CustomerID: a, At: now.Add(-2 * time.Hour)},
			{Name: "signup", CustomerID: b, At: now.Add(-time.Hour)},
			{Name: "page_view", CustomerID: c, At: now.Add(-time.Hour)},
		}}); err != nil {
		t.Fatal(err)
	}

	def := state.AnalyticsCohortDefinition{Kind: state.CohortDid, Event: "signup", WithinDays: 7, MinCount: 1}
	cohort, err := s.CreateAnalyticsCohort(ctx, state.AnalyticsCohort{AccountID: acct, EnvironmentID: f.env, Key: "signed-up", Definition: def, CreatedBy: acct}, 1)
	if err != nil || cohort.RefreshedAt != nil || cohort.Definition.Event != "signup" {
		t.Fatalf("CreateAnalyticsCohort = %+v, %v", cohort, err)
	}
	if _, err := s.CreateAnalyticsCohort(ctx, state.AnalyticsCohort{AccountID: acct, EnvironmentID: f.env, Key: "signed-up", Definition: def, CreatedBy: acct}, 5); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate = %v; want ErrConflict", err)
	}
	if _, err := s.CreateAnalyticsCohort(ctx, state.AnalyticsCohort{AccountID: acct, EnvironmentID: f.env, Key: "other", Definition: def, CreatedBy: acct}, 1); !errors.Is(err, state.ErrAnalyticsCohortLimit) {
		t.Fatalf("over the bound = %v; want ErrAnalyticsCohortLimit", err)
	}

	since := now.AddDate(0, 0, -7)
	m := state.AnalyticsMatch{EnvironmentID: f.env, Name: "signup", From: since}
	if got, err := s.AnalyticsCohortDid(ctx, m, 2, 10); err != nil || !slices.Equal(got, []string{a}) {
		t.Fatalf("did min 2 = %v, %v; want a", got, err)
	}
	pro := m
	pro.Properties = map[string]string{"plan": "pro"}
	if got, err := s.AnalyticsCohortDid(ctx, pro, 1, 10); err != nil || !slices.Equal(got, []string{a}) {
		t.Fatalf("did with filter = %v, %v; want a", got, err)
	}
	if got, err := s.AnalyticsCohortDidNot(ctx, m, now.AddDate(0, 0, -30), 10); err != nil || !slices.Equal(got, []string{c}) {
		t.Fatalf("did_not = %v, %v; want c", got, err)
	}

	due, err := s.DueAnalyticsCohorts(ctx, now.Add(-time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].Plan == "" {
		t.Fatalf("due = %+v, %v; want the new cohort with its plan", due, err)
	}
	if err := s.ReplaceAnalyticsCohortMembers(ctx, cohort, []string{b, a, uuid.NewString()}, true, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnalyticsCohort(ctx, f.env, "signed-up")
	if err != nil || got.Members != 2 || !got.Truncated || got.RefreshedAt == nil {
		t.Fatalf("after refresh = %+v, %v; want 2 members of the account", got, err)
	}
	if due, err = s.DueAnalyticsCohorts(ctx, now.Add(-time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("due after refresh = %+v, %v; want none", due, err)
	}
	if _, err := s.RequestAnalyticsCohortRefresh(ctx, f.env, "signed-up", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if due, err = s.DueAnalyticsCohorts(ctx, now.Add(-time.Hour), 10); err != nil || len(due) != 1 {
		t.Fatalf("due after a requested refresh = %+v, %v", due, err)
	}

	if keys, err := s.AnalyticsCohortsForDeploymentCustomer(ctx, f.dep, a); err != nil || !slices.Equal(keys, []string{"signed-up"}) {
		t.Fatalf("gateway lookup for a = %v, %v", keys, err)
	}
	if keys, err := s.AnalyticsCohortsForDeploymentCustomer(ctx, f.dep, c); err != nil || len(keys) != 0 {
		t.Fatalf("gateway lookup for c = %v, %v; want none", keys, err)
	}
	if keys, err := s.AnalyticsCohortsForDeploymentCustomer(ctx, "not-a-uuid", a); err != nil || keys != nil {
		t.Fatalf("lookup without a deployment = %v, %v", keys, err)
	}

	if err := s.FailAnalyticsCohortRefresh(ctx, cohort.ID, now.Add(2*time.Second), "boom"); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetAnalyticsCohort(ctx, f.env, "signed-up"); err != nil || got.RefreshError != "boom" || got.Members != 2 {
		t.Fatalf("after failure = %+v, %v; want the error and the old members", got, err)
	}
	if ok, err := s.DeleteAnalyticsCohort(ctx, f.env, "signed-up"); err != nil || !ok {
		t.Fatalf("DeleteAnalyticsCohort = %v, %v", ok, err)
	}
	if keys, err := s.AnalyticsCohortsForDeploymentCustomer(ctx, f.dep, a); err != nil || len(keys) != 0 {
		t.Fatalf("lookup after delete = %v, %v", keys, err)
	}
}
