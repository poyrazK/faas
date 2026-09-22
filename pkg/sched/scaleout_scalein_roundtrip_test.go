// spec: §6.2 — scale-out and scale-in must not undo each other.
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched/targets"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// This file closes a gap the scaling test audit found: scale-out and
// scale-in were each well covered in isolation and NOTHING drove them
// together, so the interaction between "a signal says grow" and "the reaper
// says park" was unpinned in both directions.
//
// The two halves are deliberately asymmetric today and these tests pin that
// asymmetry rather than assume it away:
//
//   - Scale-OUT reads six signals through pkg/sched/targets and
//     pkg/sched/scaleup.
//   - Aggressive scale-IN reads ONE: loop.go builds desiredByApp from
//     `a.AutoscaleTargetRPS` and `continue`s past any app without it, so an
//     app scaling on queue_lag / custom / concurrent_requests is never
//     offered to ReapAggressive at all and falls through to the slower
//     idle-timeout path in ReapIdle.
//
// That is conservative — the failure direction is holding capacity too long,
// not parking a busy app — but it is a real cost asymmetry and it was
// undocumented and untested.

// roundTripStore is the minimal AppStore the targets trigger needs, sharing
// one app slice with the reaper half of each test so both halves see the
// same policy rather than two hand-built copies that can silently diverge.
type roundTripStore struct{ apps []state.App }

func (s *roundTripStore) ListAllApps(context.Context) ([]state.App, error) { return s.apps, nil }
func (s *roundTripStore) ListAppsByNodeID(context.Context, string) ([]state.App, error) {
	return s.apps, nil
}

type roundTripLedger struct{ conc map[string]int }

func (l *roundTripLedger) Concurrency(appID string) int { return l.conc[appID] }

type roundTripEngine struct{ admitted []string }

func (e *roundTripEngine) AdmitInstance(_ context.Context, appID, _, _ string) (targets.AdmitResult, error) {
	e.admitted = append(e.admitted, appID)
	return targets.AdmitResult{InstanceID: "ins-" + appID}, nil
}

func (e *roundTripEngine) EnsureWake(_ context.Context, appID, _ string) (targets.WakeOutcome, error) {
	return targets.WakeOutcome{InstanceID: "ins-" + appID}, nil
}

// customMetricApp declares a single `custom` target and nothing else — the
// shape most exposed to the asymmetry, because a pushed metric describes a
// backlog the platform cannot see and need not correspond to any request
// traffic at all.
func customMetricApp(id string) state.App {
	return state.App{
		ID:             id,
		MaxConcurrency: 20,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{
				{Metric: api.ScalingMetricCustom, Name: "orders_pending", Value: 100},
			},
		},
	}
}

type staticCustomMetrics struct{ value float64 }

func (s staticCustomMetrics) ListCustomMetrics(_ context.Context, _ string) ([]state.CustomMetric, error) {
	return []state.CustomMetric{{Name: "orders_pending", Value: s.value, ObservedAt: time.Now()}}, nil
}

// TestScaleOutThenReapIdle_ParksWhatTheSignalJustAsked is the oscillation
// this pins, and it is a REAL behaviour rather than a hypothetical: a custom
// metric is pushed by infrastructure outside the app, so a hot backlog does
// not by itself generate the request traffic that keeps an instance out of
// ReapIdle's candidate set.
//
// The sequence is: the signal admits capacity, the instances take no
// requests, ReapIdle parks them on the idle timeout, and the signal — still
// hot — admits again on the next tick. The period is the idle timeout
// (30–600 s by plan) rather than a tick, so it is slow churn and not a spin,
// but every cycle is billed.
//
// The test asserts the churn EXISTS so the behaviour is visible and costed.
// If a future change adds a backlog-aware guard to the reaper, this test
// should be updated to assert the guard, not deleted.
func TestScaleOutThenReapIdle_ParksWhatTheSignalJustAsked(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	app := customMetricApp("app1")
	store := &roundTripStore{apps: []state.App{app}}
	engine := &roundTripEngine{}

	// 1. The signal is hot: 400 pending against a target of 100.
	trigger := targets.New(store, nil, engine, &roundTripLedger{conc: map[string]int{"app1": 1}},
		targets.Options{
			Metrics:            wire.NewOpsMetrics("schedd"),
			CustomMetricReader: staticCustomMetrics{value: 400},
		})
	if err := trigger.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitted) == 0 {
		t.Fatal("the custom signal did not admit; the round trip cannot be exercised")
	}

	// 2. Those instances receive no requests, because nothing about a
	//    pushed backlog implies inbound traffic. Idle past the Pro
	//    timeout (300 s).
	idle := []InstanceInfo{
		{Instance: "ins-1", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-400 * time.Second)},
		{Instance: "ins-2", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-400 * time.Second)},
	}
	parked := ReapIdle(now, idle, nil, nil)

	if len(parked) == 0 {
		t.Fatal("ReapIdle kept instances that took no requests for 400s — if a " +
			"backlog-aware guard was added, update this test to assert it rather than " +
			"letting the oscillation assertion below silently invert")
	}
	// The floor is 0 here, so the reaper takes everything it can. That is
	// the churn: the very next trigger tick sees the same hot metric and
	// admits again.
	if len(parked) != 2 {
		t.Errorf("parked %d of 2 idle instances, want 2", len(parked))
	}

	// 3. The signal has not changed, so the next tick re-admits. This is
	//    the closing half of the loop.
	engine.admitted = nil
	if err := trigger.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if len(engine.admitted) == 0 {
		t.Error("the still-hot custom metric did not re-admit after the park; if scale-out " +
			"now consults park history, pin that instead")
	}
}

// TestNewSignalApps_AreNotOfferedToAggressiveReaper pins the asymmetry
// directly at its source.
//
// loop.go skips any app with AutoscaleTargetRPS <= 0 when building
// desiredByApp, so an app scaling on the ADR-194/198/202 signals is never
// aggressively reaped. Parking it is left to ReapIdle's per-instance
// timeout. The consequence is that multi-signal apps scale down on the slow
// path only — worth knowing before someone "fixes" desiredByApp to read the
// new signals without also reckoning with how much more aggressive that
// makes scale-in for queue-backed workers.
func TestNewSignalApps_AreNotOfferedToAggressiveReaper(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	// desiredByApp empty == "no app was considered", which is exactly what
	// loop.go produces for an app with no legacy RPS column.
	snapshot := []InstanceInfo{
		{Instance: "ins-1", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-10 * time.Second)},
		{Instance: "ins-2", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-10 * time.Second)},
	}
	if got := ReapAggressive(now, snapshot, map[string]int{}, nil, nil); len(got) != 0 {
		t.Errorf("ReapAggressive parked %v for an app absent from desiredByApp; apps with no "+
			"legacy RPS target must defer to ReapIdle", got)
	}
}

// TestScheduledFloor_SurvivesTheReaper is the scale-out/scale-in property
// that MUST hold: an ADR-195 window promises warm capacity and the customer
// is billed for it, so the reaper must not park below it.
//
// The floor reaches the reaper as InstanceInfo.MinInstances, which loop.go
// stamps from App.EffectiveMinInstances() — schedule-aware since ADR-195.
// This pins the reaper half of that contract; the sampler half is pinned in
// pkg/meter.
func TestScheduledFloor_SurvivesTheReaper(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	// Three long-idle instances under a floor of 2: only the surplus may
	// be parked, however idle the rest are.
	snapshot := []InstanceInfo{
		{Instance: "a", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			MinInstances: 2, LastRequest: now.Add(-9999 * time.Second)},
		{Instance: "b", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			MinInstances: 2, LastRequest: now.Add(-9999 * time.Second)},
		{Instance: "c", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			MinInstances: 2, LastRequest: now.Add(-9999 * time.Second)},
	}
	parked := ReapIdle(now, snapshot, nil, nil)
	if len(parked) != 1 {
		t.Fatalf("parked %d instances under a floor of 2 with 3 running, want exactly 1 "+
			"(the surplus): a scheduled floor is billed warm capacity and must survive "+
			"the reaper", len(parked))
	}
}

// TestScaleOutStampAnchorsScaleInCooldown pins the guard that stops the two
// halves fighting within one window: a scale-out must start the same
// stabilization window a scale-in would, or the aggressive reaper can undo a
// successful admit on its very next tick while request completions still lag
// request starts.
func TestScaleOutStampAnchorsScaleInCooldown(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	justScaledOut := now.Add(-1 * time.Second)
	in := InstanceInfo{
		AppID:          "app1",
		LastScaleOutAt: &justScaledOut,
	}
	anchor := scaleInCooldownAnchor(in)
	if anchor == nil || !anchor.Equal(justScaledOut) {
		t.Fatalf("scaleInCooldownAnchor = %v, want the scale-out stamp %v: without it the "+
			"reaper can undo an admit on the next tick", anchor, justScaledOut)
	}
	// A LATER scale-in must win, so the window always tracks the most
	// recent capacity change in either direction.
	laterScaleIn := now
	in.LastScaleInAt = &laterScaleIn
	if anchor := scaleInCooldownAnchor(in); anchor == nil || !anchor.Equal(laterScaleIn) {
		t.Errorf("scaleInCooldownAnchor = %v, want the later scale-in stamp %v", anchor, laterScaleIn)
	}
}
