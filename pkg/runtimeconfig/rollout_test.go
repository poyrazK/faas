package runtimeconfig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRolloutControllerAutomaticallyRollsBackUnhealthyCanary(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	stable, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "gateway_streaming_enabled", Scope: state.RuntimeConfigScopeDaemon,
		ScopeID: "gatewayd-internal", DesiredValue: json.RawMessage(`false`), RolloutPercent: intPtr(100),
	})
	if err != nil {
		t.Fatalf("upsert stable: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, stable.Key, stable.Scope, stable.ScopeID, stable.Version, stable.DesiredValue, ""); err != nil {
		t.Fatalf("apply stable: %v", err)
	}
	canary, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: stable.Key, Scope: stable.Scope, ScopeID: stable.ScopeID,
		DesiredValue: json.RawMessage(`true`), RolloutPercent: intPtr(10),
	})
	if err != nil {
		t.Fatalf("upsert canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, canary.Key, canary.Scope, canary.ScopeID, canary.Version, canary.DesiredValue, ""); err != nil {
		t.Fatalf("apply canary: %v", err)
	}
	if err := store.AcknowledgeRuntimeConfig(ctx, state.RuntimeConfigAck{
		Key: canary.Key, Scope: canary.Scope, ScopeID: canary.ScopeID,
		Consumer: canary.ScopeID, NodeID: "node-a", Version: canary.Version,
		Status: state.RuntimeConfigAckApplied, EffectiveValue: canary.DesiredValue,
	}); err != nil {
		t.Fatalf("ack canary: %v", err)
	}

	controller := NewRolloutController(store, PrometheusHealthProvider{
		Client: &fakePromQL{values: []float64{100, 10, 100}},
		Policy: HealthPolicy{MinRequests: 1, MaxErrorRatePct: 2, MaxP95LatencyMs: 500},
	}, nil, nil, nil)
	controller.MinAge = -1
	stats, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if stats.RolledBack != 1 || stats.Unhealthy != 1 {
		t.Fatalf("stats = %#v, want one rollback", stats)
	}
	row, err := store.GetRuntimeConfig(ctx, canary.Key, canary.Scope, canary.ScopeID)
	if err != nil {
		t.Fatalf("get rolled back row: %v", err)
	}
	if row.Version != canary.Version+1 || string(row.DesiredValue) != "false" || row.RolloutPercent != 100 || row.RolloutState != state.RuntimeConfigRolloutRolledBack || row.Status != state.RuntimeConfigApplied {
		t.Fatalf("rolled back row = %#v", row)
	}
}

func TestRolloutControllerPausesWhenNoStableRevisionExists(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	canary, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "gateway_raw_stream_enabled", Scope: state.RuntimeConfigScopeDaemon,
		ScopeID: "gatewayd-internal", DesiredValue: json.RawMessage(`true`), RolloutPercent: intPtr(10),
	})
	if err != nil {
		t.Fatalf("upsert canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, canary.Key, canary.Scope, canary.ScopeID, canary.Version, canary.DesiredValue, ""); err != nil {
		t.Fatalf("apply canary: %v", err)
	}
	if err := store.AcknowledgeRuntimeConfig(ctx, state.RuntimeConfigAck{
		Key: canary.Key, Scope: canary.Scope, ScopeID: canary.ScopeID,
		Consumer: canary.ScopeID, Version: canary.Version,
		Status: state.RuntimeConfigAckFailed, Error: "invalid value",
	}); err != nil {
		t.Fatalf("ack canary: %v", err)
	}

	controller := NewRolloutController(store, PrometheusHealthProvider{}, nil, nil, nil)
	controller.MinAge = -1
	stats, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if stats.Paused != 1 || stats.RolledBack != 0 {
		t.Fatalf("stats = %#v, want one pause", stats)
	}
	row, err := store.GetRuntimeConfig(ctx, canary.Key, canary.Scope, canary.ScopeID)
	if err != nil {
		t.Fatalf("get paused row: %v", err)
	}
	if row.RolloutState != state.RuntimeConfigRolloutPaused || row.RolloutPercent != 10 || !strings.Contains(row.LastError, "no stable revision") {
		t.Fatalf("paused row = %#v", row)
	}
}

func TestRolloutControllerDoesNotMutateWhenHealthUnavailable(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "gateway_raw_stream_enabled", Scope: state.RuntimeConfigScopeDaemon,
		ScopeID: "gatewayd-internal", DesiredValue: json.RawMessage(`true`), RolloutPercent: intPtr(10),
	})
	if err != nil {
		t.Fatalf("upsert canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatalf("apply canary: %v", err)
	}
	if err := store.AcknowledgeRuntimeConfig(ctx, state.RuntimeConfigAck{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		Consumer: row.ScopeID, Version: row.Version, Status: state.RuntimeConfigAckApplied,
	}); err != nil {
		t.Fatalf("ack canary: %v", err)
	}

	controller := NewRolloutController(store, PrometheusHealthProvider{}, nil, nil, nil)
	controller.MinAge = -1
	stats, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if stats.RolledBack != 0 || stats.Paused != 0 || stats.Unhealthy != 0 {
		t.Fatalf("stats = %#v, want no mutation", stats)
	}
	got, err := store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil || got.Version != row.Version {
		t.Fatalf("row after unavailable health = %#v, err=%v", got, err)
	}
}

func TestRolloutControllerAutomaticallyPromotesHealthyCanary(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	stable, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "gateway_streaming_enabled", Scope: state.RuntimeConfigScopeDaemon,
		ScopeID: "gatewayd-internal", DesiredValue: json.RawMessage(`false`), RolloutPercent: intPtr(100),
	})
	if err != nil {
		t.Fatalf("upsert stable: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, stable.Key, stable.Scope, stable.ScopeID, stable.Version, stable.DesiredValue, ""); err != nil {
		t.Fatalf("apply stable: %v", err)
	}
	canary, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: stable.Key, Scope: stable.Scope, ScopeID: stable.ScopeID,
		DesiredValue: json.RawMessage(`true`), RolloutPercent: intPtr(1), AutoPromote: true,
	})
	if err != nil {
		t.Fatalf("upsert canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, canary.Key, canary.Scope, canary.ScopeID, canary.Version, canary.DesiredValue, ""); err != nil {
		t.Fatalf("apply canary: %v", err)
	}

	controller := NewRolloutController(store, PrometheusHealthProvider{
		Client: &fakePromQL{values: []float64{
			100, 1, 100, // 1% → 5%
			100, 1, 100, // 5% → 25%
			100, 1, 100, // 25% → 50%
			100, 1, 100, // 50% → 100%
		}},
		Policy: HealthPolicy{MinRequests: 1, MaxErrorRatePct: 2, MaxP95LatencyMs: 500},
	}, nil, nil, nil)
	controller.MinAge = -1

	wantPercents := []int{5, 25, 50, 100}
	for _, wantPercent := range wantPercents {
		row, err := store.GetRuntimeConfig(ctx, canary.Key, canary.Scope, canary.ScopeID)
		if err != nil {
			t.Fatalf("get current canary: %v", err)
		}
		if err := store.AcknowledgeRuntimeConfig(ctx, state.RuntimeConfigAck{
			Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
			Consumer: row.ScopeID, Version: row.Version, Status: state.RuntimeConfigAckApplied,
		}); err != nil {
			t.Fatalf("ack v%d: %v", row.Version, err)
		}
		stats, err := controller.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce() = %v", err)
		}
		if stats.Promoted != 1 || stats.RolledBack != 0 {
			t.Fatalf("stats = %#v, want one promotion", stats)
		}
		canary, err = store.GetRuntimeConfig(ctx, canary.Key, canary.Scope, canary.ScopeID)
		if err != nil {
			t.Fatalf("get promoted canary: %v", err)
		}
		if canary.RolloutPercent != wantPercent {
			t.Fatalf("rollout percent = %d, want %d", canary.RolloutPercent, wantPercent)
		}
		if wantPercent < 100 && canary.RolloutState != state.RuntimeConfigRolloutPromoting {
			t.Fatalf("intermediate rollout state = %q, want promoting", canary.RolloutState)
		}
		if wantPercent == 100 && canary.RolloutState != state.RuntimeConfigRolloutStable {
			t.Fatalf("final rollout state = %q, want stable", canary.RolloutState)
		}
	}
	if canary.AutoPromote {
		t.Fatal("final 100% revision should not remain auto-promoting")
	}
}

func TestRolloutControllerWaitsForTrafficBeforeAutoPromotion(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "gateway_raw_stream_enabled", Scope: state.RuntimeConfigScopeDaemon,
		ScopeID: "gatewayd-internal", DesiredValue: json.RawMessage(`true`), RolloutPercent: intPtr(1), AutoPromote: true,
	})
	if err != nil {
		t.Fatalf("upsert canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatalf("apply canary: %v", err)
	}
	if err := store.AcknowledgeRuntimeConfig(ctx, state.RuntimeConfigAck{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		Consumer: row.ScopeID, Version: row.Version, Status: state.RuntimeConfigAckApplied,
	}); err != nil {
		t.Fatalf("ack canary: %v", err)
	}

	controller := NewRolloutController(store, PrometheusHealthProvider{
		Client: &fakePromQL{values: []float64{0, 0, 0}},
		Policy: HealthPolicy{MinRequests: 10, MaxErrorRatePct: 2, MaxP95LatencyMs: 500},
	}, nil, nil, nil)
	controller.MinAge = -1
	stats, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if stats.Promoted != 0 || stats.RolledBack != 0 || stats.Unhealthy != 0 {
		t.Fatalf("stats = %#v, want no mutation while traffic is insufficient", stats)
	}
	got, err := store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil || got.Version != row.Version || got.RolloutPercent != 1 {
		t.Fatalf("row after insufficient traffic = %#v, err=%v", got, err)
	}
}

func intPtr(value int) *int { return &value }
