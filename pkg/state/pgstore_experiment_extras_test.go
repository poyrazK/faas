//go:build !no_pg

package state_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261119173702 §1: SDK exposures add subject units only to a running
// subject experiment on their rule; a subject seen with another variant is
// crossed; candidates and pre-value lists carry the customer and hash.
func TestPg_FlagExperimentSubjectUnits(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-subjects")
	s, ctx, acct := f.s, f.ctx, f.acct
	a := f.tenant("a")
	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "by-user", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Unit: flags.ExperimentUnitSubject,
		MixingLift: 0.02, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 5)
	if err != nil || x.Unit != flags.ExperimentUnitSubject || x.Allocation != flags.ExperimentAllocationFixed {
		t.Fatalf("CreateFlagExperiment = %+v, %v", x, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	h1, h2 := state.AnalyticsSubjectHash(f.env, "user-1"), state.AnalyticsSubjectHash(f.env, "user-2")
	exp := []state.FlagExperimentExposure{{Flag: "checkout", RuleID: "weighted", Variant: "new", CustomerID: a, SubjectHash: h1, At: now}}
	if n, err := s.RecordFlagExperimentExposures(ctx, acct, f.env, exp); err != nil || n != 0 {
		t.Fatalf("exposures to a draft = %d, %v; want none", n, err)
	}
	if _, err := s.StartFlagExperiment(ctx, f.env, "by-user", x.Weights, [2]int{}, now.Add(-time.Hour), 5); err != nil {
		t.Fatal(err)
	}
	exp = append(exp,
		state.FlagExperimentExposure{Flag: "checkout", RuleID: "weighted", Variant: "legacy", CustomerID: a, SubjectHash: h2, At: now},
		state.FlagExperimentExposure{Flag: "checkout", RuleID: "other", Variant: "new", CustomerID: a, SubjectHash: h1, At: now},
		state.FlagExperimentExposure{Flag: "checkout", RuleID: "weighted", Variant: "legacy", CustomerID: a, SubjectHash: h1, At: now.Add(time.Minute)},
		state.FlagExperimentExposure{Flag: "checkout", RuleID: "weighted", Variant: "new", CustomerID: "00000000-0000-4000-8000-000000000099", SubjectHash: h1, At: now})
	if n, err := s.RecordFlagExperimentExposures(ctx, acct, f.env, exp); err != nil || n != 3 {
		t.Fatalf("exposures = %d, %v; want 3 on the experiment's rule", n, err)
	}
	running, err := s.ListRunningFlagExperiments(ctx, "", 10)
	if err != nil || len(running) != 1 || !running[0].SubjectUnits() {
		t.Fatalf("running = %+v, %v", running, err)
	}
	x = running[0]
	arms, crossed, err := s.FlagExperimentArms(ctx, x, 1)
	if err != nil || crossed != 1 || len(arms) != 1 || arms[0] != (flags.Arm{Variant: "legacy", Exposed: 1}) {
		t.Fatalf("arms = %+v crossed %d, %v; want user-2 on legacy and user-1 crossed", arms, crossed, err)
	}
	cands, err := s.FlagExperimentConversionCandidates(ctx, x, "", now.Add(time.Hour), now.Add(-25*time.Hour), 10)
	if err != nil || len(cands) != 1 || cands[0].Customer != a || !bytes.Equal(cands[0].SubjectHash, h2) || cands[0].CustomerID != state.SubjectUnitID(a, h2) {
		t.Fatalf("candidates = %+v, %v", cands, err)
	}
	if err := s.MarkFlagExperimentUnitConversions(ctx, x.ID, map[string]time.Time{cands[0].CustomerID: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if arms, _, _ = s.FlagExperimentArms(ctx, x, 1); arms[0].Converted != 1 {
		t.Fatalf("arms after conversion = %+v", arms)
	}
	missing, err := s.FlagExperimentUnitsWithoutPreValue(ctx, x.ID, 10)
	if err != nil || len(missing) != 2 || missing[0].SubjectHash == nil || missing[0].Customer != a {
		t.Fatalf("units without pre value = %+v, %v", missing, err)
	}
}

// ADR 20261119173702 §3–4: ratio arms sum numerator and denominator values
// per unit without counting a folded day twice; unit values feed
// percentiles; bandit state is stored; segment arms split by cohort.
func TestPg_FlagExperimentRatioPercentileBanditSegments(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-ratio")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b := f.tenant("a"), f.tenant("b")
	start := time.Now().UTC().AddDate(0, 0, -3).Truncate(time.Hour)
	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "aov", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "revenue", MetricKind: flags.ExperimentMetricRatio,
		DenominatorEvent: "orders", MixingLift: 0.1, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 5)
	if err != nil || x.DenominatorEvent != "orders" {
		t.Fatalf("CreateFlagExperiment = %+v, %v", x, err)
	}
	if _, err := s.StartFlagExperiment(ctx, f.env, "aov", x.Weights, [2]int{}, start, 5); err != nil {
		t.Fatal(err)
	}
	f.exposure(a, "legacy", start.Add(time.Minute), 200, 10)
	f.exposure(b, "new", start.Add(time.Minute), 200, 10)
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env}
	if _, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 100, MaxNames: 10, Now: time.Now(), Events: []state.FlagConversionEvent{
		{Name: "revenue", CustomerID: a, Value: 300, At: start.Add(2 * time.Hour)},
		{Name: "orders", CustomerID: a, Value: 3, At: start.Add(2 * time.Hour)},
		{Name: "revenue", CustomerID: b, Value: 500, At: start.AddDate(0, 0, 1)},
		{Name: "orders", CustomerID: b, Value: 2, At: start.AddDate(0, 0, 1)},
	}}); err != nil {
		t.Fatal(err)
	}
	running, _ := s.ListRunningFlagExperiments(ctx, "", 10)
	x = running[0]
	if _, err := s.AdvanceFlagExperimentUnits(ctx, x, start, time.Now(), 100); err != nil {
		t.Fatal(err)
	}
	want := []flags.RatioArm{
		{Variant: "legacy", Units: 1, Num: 300, Den: 3, NumSq: 90000, DenSq: 9, NumDen: 900},
		{Variant: "new", Units: 1, Num: 500, Den: 2, NumSq: 250000, DenSq: 4, NumDen: 1000},
	}
	arms, err := s.FlagExperimentRatioArms(ctx, x)
	if err != nil || len(arms) != 2 || arms[0] != want[0] || arms[1] != want[1] {
		t.Fatalf("ratio arms = %+v, %v; want %+v", arms, err, want)
	}
	if err := s.FoldFlagExperimentValues(ctx, x, time.Now().AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	x, _ = s.GetFlagExperiment(ctx, f.env, "aov")
	x.EnvironmentSlug = "production"
	if arms, err = s.FlagExperimentRatioArms(ctx, x); err != nil || arms[0] != want[0] || arms[1] != want[1] {
		t.Fatalf("ratio arms after a fold = %+v, %v; want the same totals", arms, err)
	}
	values, err := s.FlagExperimentUnitValues(ctx, x, 10)
	if err != nil || len(values["legacy"]) != 1 || values["legacy"][0] != 300 || values["new"][0] != 500 {
		t.Fatalf("unit values = %v, %v", values, err)
	}

	at := time.Now().UTC().Truncate(time.Second)
	post := []flags.BanditArm{{Variant: "legacy", BestProbability: 0.2, ExpectedLoss: 0.01}, {Variant: "new", BestProbability: 0.8}}
	if err := s.SetFlagExperimentBandit(ctx, x.ID, post, map[string]int{"legacy": 3000, "new": 7000}, &at); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFlagExperiment(ctx, f.env, "aov")
	if err != nil || len(got.Bandit) != 2 || got.Bandit[1].BestProbability != 0.8 || got.Weights["new"] != 7000 || got.BanditWeightedAt == nil {
		t.Fatalf("after a bandit pass = %+v, %v", got, err)
	}
	if err := s.SetFlagExperimentBandit(ctx, x.ID, post, map[string]int{"legacy": 1, "new": 9999}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetFlagExperiment(ctx, f.env, "aov"); got.Weights["new"] != 7000 {
		t.Fatalf("a pass that did not re-weight changed the weights: %v", got.Weights)
	}

	cohort, err := s.CreateAnalyticsCohort(ctx, state.AnalyticsCohort{AccountID: acct, EnvironmentID: f.env, Key: "big",
		Definition: state.AnalyticsCohortDefinition{Kind: state.CohortDid, Event: "revenue", WithinDays: 7}, CreatedBy: acct}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAnalyticsCohortMembers(ctx, cohort, []string{b}, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	members, others, err := s.FlagExperimentSegmentArms(ctx, x, cohort.ID)
	if err != nil || len(members) != 1 || members[0].Variant != "new" || len(others) != 1 || others[0].Variant != "legacy" {
		t.Fatalf("segment arms = %+v / %+v, %v", members, others, err)
	}
}

// ADR 20261119173702 §5: a rollout stage's treatment is the customers whose
// decision matched the rule, the comparison everyone else who evaluated the
// flag, and conversions count from first exposure.
func TestPg_FeatureFlagRolloutMetricArms(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-rollout")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b, c := f.tenant("a"), f.tenant("b"), f.tenant("c")
	since := time.Now().UTC().Add(-2 * time.Hour)
	f.decision(a, "stage", since.Add(time.Minute))
	f.decision(b, "", since.Add(time.Minute))
	f.decision(c, "", since.Add(time.Minute))
	f.decision(a, "stage", since.Add(-time.Hour))
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env}
	if _, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 100, MaxNames: 10, Now: time.Now(), Events: []state.FlagConversionEvent{
		{Name: "order", CustomerID: a, At: since.Add(time.Hour)},
		{Name: "order", CustomerID: b, At: since.Add(-30 * time.Minute)},
	}}); err != nil {
		t.Fatal(err)
	}
	comparison, treatment, err := s.FeatureFlagRolloutMetricArms(ctx, state.RolloutMetricQuery{AccountID: acct, ProjectID: f.project,
		EnvironmentID: f.env, EnvironmentSlug: "production", FlagKey: "checkout", RuleID: "stage", Event: "order", Since: since, MaxUnits: 100})
	if err != nil || treatment.Exposed != 1 || treatment.Converted != 1 || comparison.Exposed != 2 || comparison.Converted != 0 {
		t.Fatalf("rollout arms = %+v / %+v, %v", comparison, treatment, err)
	}
}

// ADR 20261119173702 §2: customers the environment holdout served control
// are not units of the experiment on their rule.
func TestPg_FlagExperimentHoldoutNotCounted(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flags-holdout")
	s, ctx, acct := f.s, f.ctx, f.acct
	a, b := f.tenant("a"), f.tenant("b")
	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	x, err := s.CreateFlagExperiment(ctx, state.FlagExperiment{AccountID: acct, ProjectID: f.project, EnvironmentID: f.env,
		Key: "held", FlagKey: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
		MixingLift: 0.02, Weights: map[string]int{"legacy": 50, "new": 50}, CreatedBy: acct}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartFlagExperiment(ctx, f.env, x.Key, x.Weights, [2]int{}, start, 5); err != nil {
		t.Fatal(err)
	}
	f.exposure(a, "new", start.Add(time.Minute), 200, 10)
	f.held(b, start.Add(time.Minute))
	running, err := s.ListRunningFlagExperiments(ctx, "", 10)
	if err != nil || len(running) != 1 {
		t.Fatalf("running = %+v, %v", running, err)
	}
	if read, err := s.AdvanceFlagExperimentUnits(ctx, running[0], start, start.Add(time.Hour), 1000); err != nil || read != 1 {
		t.Fatalf("AdvanceFlagExperimentUnits read %d, %v; want only the rule_match customer", read, err)
	}
	arms, _, err := s.FlagExperimentArms(ctx, running[0], 1)
	if err != nil || len(arms) != 1 || arms[0].Variant != "new" || arms[0].Exposed != 1 {
		t.Fatalf("arms = %+v, %v; want the held-out customer left out", arms, err)
	}
}
