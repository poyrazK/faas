package state_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAlertRuleAnomalyEvaluation(t *testing.T) {
	s, ctx, acct, app := alertFixture(t)
	checkAlertAnomalyEvaluation(t, s, ctx, memSampleRule(acct.ID, app.ID))
}

func TestPgStore_AlertRuleAnomalyEvaluation(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	checkAlertAnomalyEvaluation(t, s, ctx, pgSampleAlertRule(acct, app))
}

type alertAnomalyTestStore interface {
	state.AlertAnomalyRecoveryStore
	state.AlertObservationStore
	state.AlertObservationCoverageStore
	CreateAlertRule(context.Context, state.AlertRule) (state.AlertRule, error)
	AlertRuleByID(context.Context, string) (state.AlertRule, error)
	UpdateAlertRule(context.Context, string, state.UpdateAlertRuleParams) (state.AlertRule, error)
	ClaimAlertFire(context.Context, string, string, []byte, float64, time.Time) (string, bool, state.AlertFireRecipients, error)
	SetAlertRuleState(context.Context, string, state.AlertState, time.Time) (bool, error)
}

func TestMemStoreAlertObservation(t *testing.T) {
	s, ctx, acct, app := alertFixture(t)
	checkAlertObservation(t, s, ctx, memSampleRule(acct.ID, app.ID))
	checkObservedIncidentOpeningEvidence(t, s, ctx, memSampleRule(acct.ID, app.ID))
	checkAlertObservationCoverage(t, s, ctx, memSampleRule(acct.ID, app.ID))
	checkAlertObservationEnvironment(t, s, ctx, memSampleRule(acct.ID, app.ID))
	checkAlertRecoveryConfirmation(t, s, ctx, memSampleRule(acct.ID, app.ID))
}

func TestPgStore_AlertObservation(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	checkAlertObservation(t, s, ctx, pgSampleAlertRule(acct, app))
	checkObservedIncidentOpeningEvidence(t, s, ctx, pgSampleAlertRule(acct, app))
	checkAlertObservationCoverage(t, s, ctx, pgSampleAlertRule(acct, app))
	checkAlertObservationEnvironment(t, s, ctx, pgSampleAlertRule(acct, app))
	checkAlertRecoveryConfirmation(t, s, ctx, pgSampleAlertRule(acct, app))
}

func checkAlertObservationCoverage(t *testing.T, s alertAnomalyTestStore, ctx context.Context, input state.AlertRule) {
	t.Helper()
	input.Metric, input.Comparison, input.Threshold = state.AlertMetricErrorRate, state.AlertAboveBaseline, 3
	input.ObserveOnly = true
	check := func(rule state.AlertRule, at time.Time, reason api.AlertAnomalyEvaluationReason, want bool) state.AlertRule {
		t.Helper()
		got, err := s.RecordAlertObservation(ctx, rule, at, api.AlertAnomalyEvaluation{Reason: reason})
		if err != nil || got != want {
			t.Fatalf("record at %s: got %v, err=%v, want %v", at, got, err, want)
		}
		current, err := s.AlertRuleByID(ctx, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current
	}
	read := func(rule state.AlertRule, asOf time.Time, attempted, completed int, lastCheck time.Time) api.AlertObservationCoverage {
		t.Helper()
		coverage, err := s.AlertObservationCoverage(ctx, rule, asOf)
		if err != nil || coverage.Attempted != attempted || coverage.Completed != completed || !coverage.LastCheckedAt.Equal(lastCheck) {
			t.Fatalf("coverage: %+v err=%v, want %d/%d completed, last %s", coverage, err, completed, attempted, lastCheck)
		}
		return coverage
	}
	input.Name = "Quiet rule"
	quiet, err := s.CreateAlertRule(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "Skipped rule"
	skipped, err := s.CreateAlertRule(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	first := skipped.UpdatedAt.UTC().Truncate(time.Microsecond).Add(time.Minute)
	second := first.Add(time.Minute)
	quiet = check(quiet, first, api.AlertAnomalyThresholdNotReached, true)
	quiet = check(quiet, second, api.AlertAnomalyThresholdNotReached, true)
	skipped = check(skipped, first, api.AlertAnomalyTelemetryMissing, true)
	stale := skipped
	skipped = check(skipped, second, api.AlertAnomalyInsufficientTraffic, true)
	check(stale, second.Add(time.Minute), api.AlertAnomalyTelemetryMissing, false)
	read(quiet, second.Add(time.Minute), 2, 2, second)
	coverage := read(skipped, second.Add(time.Minute), 2, 0, second)
	want := []api.AlertObservationSkipCount{
		{Reason: api.AlertAnomalyInsufficientTraffic, Count: 1},
		{Reason: api.AlertAnomalyTelemetryMissing, Count: 1},
	}
	if !slices.Equal(coverage.Skipped, want) || len(quiet.ObservedIncidents) != 0 || len(skipped.ObservedIncidents) != 0 {
		t.Fatalf("skip breakdown or incident history: skips=%+v, quiet=%v, skipped=%v", coverage.Skipped, quiet.ObservedIncidents, skipped.ObservedIncidents)
	}
	read(skipped, first.Add(24*time.Hour), 1, 0, second)
	read(skipped, second.Add(24*time.Hour), 0, 0, second)
	quiet, err = s.UpdateAlertRule(ctx, quiet.ID, state.UpdateAlertRuleParams{Name: new("Renamed quiet rule")})
	if err != nil {
		t.Fatal(err)
	}
	read(quiet, second, 2, 2, second)
	quiet, err = s.UpdateAlertRule(ctx, quiet.ID, state.UpdateAlertRuleParams{Threshold: new(4.0)})
	if err != nil {
		t.Fatal(err)
	}
	skipped, err = s.UpdateAlertRule(ctx, skipped.ID, state.UpdateAlertRuleParams{EnvironmentScope: new(api.DefaultEnvScope)})
	if err != nil {
		t.Fatal(err)
	}
	read(quiet, second, 0, 0, time.Time{})
	read(skipped, second, 0, 0, time.Time{})
}

func checkObservedIncidentOpeningEvidence(t *testing.T, s alertAnomalyTestStore, ctx context.Context, input state.AlertRule) {
	t.Helper()
	input.Name = "Record opening evidence"
	input.Metric, input.Comparison, input.Threshold = state.AlertMetricErrorRate, state.AlertAboveBaseline, 3
	input.ObserveOnly = true
	rule, err := s.CreateAlertRule(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	at := rule.UpdatedAt.UTC().Truncate(time.Microsecond).Add(time.Minute)
	write := func(evidence api.AlertAnomalyEvaluation) {
		t.Helper()
		ok, err := s.RecordAlertObservation(ctx, rule, at, evidence)
		if err != nil || !ok {
			t.Fatalf("record observation: accepted=%v err=%v", ok, err)
		}
		rule, err = s.AlertRuleByID(ctx, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
	}
	assertOpening := func(index int, observed, trigger float64) {
		t.Helper()
		want := api.AlertObservedIncidentOpeningEvidence{
			Metric: string(input.Metric), Window: string(input.WindowSpec), Comparison: string(input.Comparison),
			Observed: observed, Baseline: 1, TriggerValue: trigger, BaselineDays: 6,
		}
		if len(rule.ObservedIncidents) <= index {
			t.Fatalf("missing incident at index %d: %+v", index, rule.ObservedIncidents)
		}
		if got := rule.ObservedIncidents[index].OpeningEvidence; got == nil || *got != want {
			t.Fatalf("incident %d opening evidence: got %+v, want %+v", index, got, want)
		}
	}
	breach := api.AlertAnomalyEvaluation{
		Reason: api.AlertAnomalyObservedBreach, Observed: new(5.0), Baseline: new(1.0),
		TriggerValue: new(3.0), BaselineDays: new(6),
	}
	write(breach)
	assertOpening(0, 5, 3)
	breach.Observed = new(9.0)
	write(breach)
	assertOpening(0, 5, 3)
	write(api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyThresholdNotReached})
	if rule.ObservedIncidents[0].Outcome != api.AlertObservationRecovered {
		t.Fatalf("incident did not recover: %+v", rule.ObservedIncidents)
	}
	rule, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Threshold: new(4.0)})
	if err != nil {
		t.Fatal(err)
	}
	assertOpening(0, 5, 3)
	breach.TriggerValue = new(4.0)
	write(breach)
	assertOpening(0, 9, 4)
	assertOpening(1, 5, 3)
}

func checkAlertRecoveryConfirmation(t *testing.T, s alertAnomalyTestStore, ctx context.Context, input state.AlertRule) {
	t.Helper()
	input.Name = "Confirm recovery"
	input.Comparison = state.AlertAboveBaseline
	input.Threshold = 3
	input.RecoveryConfirmationChecks = 2
	rule, err := s.CreateAlertRule(ctx, input)
	if err != nil || rule.RecoveryConfirmationChecks != 2 {
		t.Fatalf("create rule with two-check recovery: %+v err=%v", rule, err)
	}
	at := rule.UpdatedAt.UTC().Truncate(time.Microsecond).Add(time.Second)
	if changed, err := s.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, at); err != nil || !changed {
		t.Fatalf("open incident: changed=%v err=%v", changed, err)
	}
	read := func() state.AlertRule {
		t.Helper()
		got, err := s.AlertRuleByID(ctx, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	write := func(snapshot state.AlertRule, when time.Time, evidence api.AlertAnomalyEvaluation, want bool) {
		t.Helper()
		ok, err := s.RecordAlertAnomalyEvaluation(ctx, snapshot, when, evidence)
		if err != nil || ok != want {
			t.Fatalf("record at %s: accepted=%v err=%v, want %v", when, ok, err, want)
		}
	}
	resolve := func(when time.Time, want bool) {
		t.Helper()
		changed, err := s.ResolveConfirmedAlertAnomaly(ctx, rule.ID, when)
		if err != nil || changed != want {
			t.Fatalf("resolve at %s: changed=%v err=%v, want %v", when, changed, err, want)
		}
	}
	healthy := api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyThresholdNotReached}
	snapshot := read()
	first := at.Add(time.Second)
	write(snapshot, first, state.ConfirmAlertAnomalyRecovery(snapshot, healthy), true)
	pending := read()
	if pending.AnomalyEvaluation.Reason != api.AlertAnomalyRecoveryPending || pending.AnomalyEvaluation.Count != 1 {
		t.Fatalf("persisted progress: %+v", pending.AnomalyEvaluation)
	}
	second := first.Add(time.Second)
	write(snapshot, second, state.ConfirmAlertAnomalyRecovery(snapshot, healthy), false)
	resolve(first, false)
	write(pending, second, api.AlertAnomalyEvaluation{}, true) // A fresh breach.
	third := second.Add(time.Second)
	reset := read()
	write(reset, third, state.ConfirmAlertAnomalyRecovery(reset, healthy), true)
	pending = read()
	if pending.AnomalyEvaluation.Reason != api.AlertAnomalyRecoveryPending || pending.AnomalyEvaluation.Count != 1 {
		t.Fatalf("the breach did not clear recovery progress: %+v", pending.AnomalyEvaluation)
	}
	fourth := third.Add(time.Second)
	write(pending, fourth, state.ConfirmAlertAnomalyRecovery(pending, healthy), true)
	resolve(fourth, true)
	resolve(fourth, false)
	open, err := s.AlertRuleEpisodeOpen(ctx, rule.ID)
	if err != nil || open {
		t.Fatalf("confirmed recovery left the episode open: %v, err=%v", open, err)
	}
}

func checkAlertObservationEnvironment(t *testing.T, s alertAnomalyTestStore, ctx context.Context, input state.AlertRule) {
	t.Helper()
	input.Name = "Observe by environment"
	input.Comparison, input.Threshold = state.AlertAboveBaseline, 3
	input.ObserveOnly, input.EnvironmentScope = true, api.DefaultEnvScope
	rule, err := s.CreateAlertRule(ctx, input)
	if err != nil || rule.EnvironmentScope != api.DefaultEnvScope {
		t.Fatalf("create environment rule: scope=%q err=%v", rule.EnvironmentScope, err)
	}
	at := rule.UpdatedAt.Add(time.Second).UTC().Truncate(time.Microsecond)
	evidence := api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyObservedBreach, BaselineDays: new(6)}
	if accepted, err := s.RecordAlertObservation(ctx, rule, at, evidence); err != nil || !accepted {
		t.Fatalf("record default observation: accepted=%v err=%v", accepted, err)
	}
	snapshot, err := s.AlertRuleByID(ctx, rule.ID)
	if err != nil || snapshot.AnomalyEvaluation.BaselineDays == nil || *snapshot.AnomalyEvaluation.BaselineDays != 6 {
		t.Fatalf("baseline coverage=%+v err=%v", snapshot.AnomalyEvaluation, err)
	}
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false), Action: new("demote")}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("scoped Notify unexpectedly accepted a deployment action: %v", err)
	}
	edited, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{EnvironmentScope: new("staging")})
	if err != nil || edited.EnvironmentScope != "staging" || edited.AnomalyEvaluation.Reason != "" ||
		len(edited.ObservedIncidents) != 1 || edited.ObservedIncidents[0].Outcome != api.AlertObservationEnded ||
		edited.ObservedIncidents[0].EnvironmentScope != api.DefaultEnvScope {
		t.Fatalf("edit did not retain the closed default incident: %+v err=%v", edited, err)
	}
	if accepted, err := s.RecordAlertObservation(ctx, snapshot, at.Add(time.Second), evidence); err != nil || accepted {
		t.Fatalf("stale observation: accepted=%v err=%v", accepted, err)
	}
	if accepted, err := s.RecordAlertObservation(ctx, edited, at.Add(time.Minute), evidence); err != nil || !accepted {
		t.Fatalf("record staging observation: accepted=%v err=%v", accepted, err)
	}
	got, err := s.AlertRuleByID(ctx, rule.ID)
	if err != nil || len(got.ObservedIncidents) != 2 || got.ObservedIncidents[0].EnvironmentScope != "staging" {
		t.Fatalf("staging did not open its own incident: %+v err=%v", got.ObservedIncidents, err)
	}
	live, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)})
	if err != nil || live.ObserveOnly || live.EnvironmentScope != "staging" ||
		len(live.ObservedIncidents) != 2 || live.ObservedIncidents[0].Outcome != api.AlertObservationEnded {
		t.Fatalf("scoped Notify did not retain the environment and observations: %+v err=%v", live, err)
	}
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{EnvironmentScope: new("production")}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("live scope edit unexpectedly succeeded: %v", err)
	}
	fireAt := at.Add(2 * time.Minute)
	if _, won, _, err := s.ClaimAlertFire(ctx, rule.ID, "scoped-fire", []byte(`{"environment_scope":"default"}`), 10, fireAt); !errors.Is(err, state.ErrConflict) || won {
		t.Fatalf("stale scope claimed a notification: won=%v err=%v", won, err)
	}
	if _, won, _, err := s.ClaimAlertFire(ctx, rule.ID, "scoped-fire", []byte(`{"environment_scope":"staging"}`), 10, fireAt); err != nil || !won {
		t.Fatalf("current scope failed to claim: won=%v err=%v", won, err)
	}
}

func checkAlertObservation(t *testing.T, s alertAnomalyTestStore, ctx context.Context, input state.AlertRule) {
	t.Helper()
	input.Comparison, input.Threshold, input.ObserveOnly = state.AlertAboveBaseline, 3, true
	rule, err := s.CreateAlertRule(ctx, input)
	if err != nil || !rule.Enabled || !rule.ObserveOnly {
		t.Fatalf("create observe-only rule: %+v err=%v", rule, err)
	}
	at := rule.UpdatedAt.Add(time.Second).UTC().Truncate(time.Microsecond)
	write := func(snapshot state.AlertRule, at time.Time, reason api.AlertAnomalyEvaluationReason, want bool) {
		t.Helper()
		ok, err := s.RecordAlertObservation(ctx, snapshot, at, api.AlertAnomalyEvaluation{Reason: reason})
		if err != nil || ok != want {
			t.Fatalf("record %s: accepted=%v err=%v, want %v", reason, ok, err, want)
		}
	}
	read := func() state.AlertRule {
		t.Helper()
		got, err := s.AlertRuleByID(ctx, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	write(rule, at, api.AlertAnomalyObservedBreach, true)
	write(rule, at.Add(time.Second), api.AlertAnomalyThresholdNotReached, false)
	current := read()
	if current.State != state.AlertStateOk || !current.LastFiredAt.IsZero() || len(current.ObservedIncidents) != 1 ||
		current.ObservedIncidents[0].EligibleNotifications != 1 || !current.ObservedIncidents[0].OpenedAt.Equal(at) {
		t.Fatalf("observation affected the live state or lost the incident: %+v", current)
	}
	paused, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: new(false)})
	if err != nil || paused.Enabled || !paused.ObserveOnly || len(paused.ObservedIncidents) != 1 || paused.ObservedIncidents[0].Outcome != api.AlertObservationEnded {
		t.Fatalf("pause observation: %+v err=%v", paused, err)
	}
	write(current, at.Add(2*time.Second), api.AlertAnomalyObservedBreach, false)
	_, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{
		ObserveOnly: new(false), Enabled: new(true), WebhookURL: new(""), PromotionChannelIDs: &[]string{},
	})
	if !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("promote without a destination: %v", err)
	}
	if got := read(); !got.ObserveOnly || got.Enabled || got.WebhookURL != rule.WebhookURL {
		t.Fatalf("failed promotion changed the rule: %+v", got)
	}
	promoted, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false), Enabled: new(true)})
	if err != nil || promoted.ObserveOnly || !promoted.Enabled || promoted.AnomalyEvaluation.Reason != "" ||
		len(promoted.ObservedIncidents) != 1 || !promoted.LastFiredAt.IsZero() {
		t.Fatalf("promote observation: %+v err=%v", promoted, err)
	}
}

func checkAlertAnomalyEvaluation(t *testing.T, s alertAnomalyTestStore, ctx context.Context, input state.AlertRule) {
	t.Helper()
	input.Comparison, input.Threshold = state.AlertAboveBaseline, 3
	rule, err := s.CreateAlertRule(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	read := func() state.AlertRule {
		t.Helper()
		got, err := s.AlertRuleByID(ctx, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	at := rule.UpdatedAt.Add(time.Second).Truncate(time.Microsecond)
	traffic := api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyInsufficientTraffic, RequiredCount: 50}
	degraded := api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyTelemetryMissing}
	record := func(base state.AlertRule, when time.Time, evidence api.AlertAnomalyEvaluation, want bool) {
		t.Helper()
		written, err := s.RecordAlertAnomalyEvaluation(ctx, base, when, evidence)
		if err != nil || written != want {
			t.Fatalf("record: written=%v err=%v, want written=%v", written, err, want)
		}
	}
	record(rule, at, traffic, true)
	current := read()
	if current.State != state.AlertStateUnknown || !current.LastEvaluatedAt.Equal(at) ||
		!current.AnomalyEvaluation.EvaluatedAt.Equal(at) ||
		current.AnomalyEvaluation.Reason != traffic.Reason || current.AnomalyEvaluation.RequiredCount != 50 ||
		current.AnomalyEvaluation.Count != 0 {
		t.Fatalf("want a stamped unknown state with 0 of 50 requests, got %+v", current)
	}
	record(current, at.Add(-time.Second), degraded, false)
	quiet := api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyZeroBaseline, Observed: new(5.0), Baseline: new(0.0)}
	record(current, at.Add(time.Second), quiet, true)
	current = read()
	if got := current.AnomalyEvaluation; got.Reason != quiet.Reason || got.Observed == nil || *got.Observed != 5 || got.Baseline == nil || *got.Baseline != 0 || got.TriggerValue != nil {
		t.Fatalf("want a completed check with a zero baseline, got %+v", got)
	}
	eligibleAt := at.Add(30 * time.Minute)
	cooldown := api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyNotificationCooldown, NextNotificationEligibleAt: &eligibleAt}
	record(current, at.Add(2*time.Second), cooldown, true)
	current = read()
	if got := current.AnomalyEvaluation; got.Reason != cooldown.Reason || got.NextNotificationEligibleAt == nil || !got.NextNotificationEligibleAt.Equal(eligibleAt) {
		t.Fatalf("want cooldown until %s, got %+v", eligibleAt, got)
	}
	record(current, at.Add(3*time.Second), api.AlertAnomalyEvaluation{}, true)
	current = read()
	if current.AnomalyEvaluation.Reason != "" {
		t.Fatalf("a breach must clear the old quiet explanation, got %+v", current.AnomalyEvaluation)
	}
	record(current, at.Add(4*time.Second), degraded, true)
	current = read()
	if current.State != state.AlertStateDegraded || current.AnomalyEvaluation.Reason != degraded.Reason {
		t.Fatalf("want a degraded evaluation, got state=%s evidence=%+v", current.State, current.AnomalyEvaluation)
	}
	threshold := 4.0
	if _, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Threshold: &threshold}); err != nil {
		t.Fatal(err)
	}
	record(current, at.Add(5*time.Second), traffic, false)
	if got := read(); got.AnomalyEvaluation.Reason != "" || got.Threshold != 4 {
		t.Fatalf("rule edit must clear the explanation, got threshold=%v evidence=%+v", got.Threshold, got.AnomalyEvaluation)
	}
}
