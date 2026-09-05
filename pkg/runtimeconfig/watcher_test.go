package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestBoolRequiresJSONBoolean(t *testing.T) {
	if got, err := Bool(json.RawMessage(`true`)); err != nil || !got {
		t.Fatalf("Bool(true) = %v, %v", got, err)
	}
	if got, err := Bool(json.RawMessage(`"true"`)); err == nil || got {
		t.Fatalf("Bool(string) = %v, %v; want an error", got, err)
	}
}

func TestBoolFlagLoadStore(t *testing.T) {
	flag := NewBoolFlag(false)
	if flag.Load() {
		t.Fatal("new flag unexpectedly enabled")
	}
	flag.Store(true)
	if !flag.Load() {
		t.Fatal("stored flag did not become enabled")
	}
}

func TestWatcherReconcileRecordsFailedAcknowledgementAndRetries(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key:          KeyGatewayStreaming,
		Scope:        state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`),
		ApplyMode:    state.RuntimeConfigApplyHot,
	})
	if err != nil {
		t.Fatalf("upsert config: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatalf("mark config applied: %v", err)
	}

	applyErr := errors.New("runtime gate rejected value")
	applyCalls := 0
	w := New(store, nil, []string{KeyGatewayStreaming}, func(context.Context, string, json.RawMessage, int64) error {
		applyCalls++
		return applyErr
	}, nil).WithIdentity("gatewayd-internal", "node-a")
	if err := w.Reconcile(ctx); !errors.Is(err, applyErr) {
		t.Fatalf("reconcile error = %v, want %v", err, applyErr)
	}
	acks, err := store.ListRuntimeConfigAcks(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil {
		t.Fatalf("list acknowledgements: %v", err)
	}
	if len(acks) != 1 || acks[0].Status != state.RuntimeConfigAckFailed || acks[0].Version != row.Version || acks[0].Error != applyErr.Error() {
		t.Fatalf("failed acknowledgement = %#v", acks)
	}
	if applyCalls != 1 {
		t.Fatalf("apply calls after first reconcile = %d, want 1", applyCalls)
	}
	if err := w.Reconcile(ctx); !errors.Is(err, applyErr) {
		t.Fatalf("retry reconcile error = %v, want %v", err, applyErr)
	}
	if applyCalls != 2 {
		t.Fatalf("failed version was not retried, apply calls = %d", applyCalls)
	}
}

func TestWatcherReconcileAppliesAcknowledgedVersionsOnly(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key:          KeyTenantSurfaces,
		Scope:        state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`),
		ApplyMode:    state.RuntimeConfigApplyHot,
	})
	if err != nil {
		t.Fatalf("upsert pending row: %v", err)
	}
	var applied []struct {
		key     string
		value   bool
		version int64
	}
	w := New(store, nil, []string{KeyTenantSurfaces}, func(_ context.Context, key string, value json.RawMessage, version int64) error {
		enabled, err := Bool(value)
		if err != nil {
			return err
		}
		applied = append(applied, struct {
			key     string
			value   bool
			version int64
		}{key: key, value: enabled, version: version})
		return nil
	}, nil).WithIdentity("gatewayd-internal", "node-a")
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile pending row: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("pending row was applied: %#v", applied)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatalf("acknowledge row: %v", err)
	}
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile applied row: %v", err)
	}
	if len(applied) != 1 || !applied[0].value || applied[0].version != row.Version {
		t.Fatalf("applied calls = %#v, want one true v%d", applied, row.Version)
	}
	acks, err := store.ListRuntimeConfigAcks(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil {
		t.Fatalf("list runtime config acknowledgements: %v", err)
	}
	if len(acks) != 1 || acks[0].Consumer != "gatewayd-internal" || acks[0].NodeID != "node-a" || acks[0].Version != row.Version || acks[0].Status != state.RuntimeConfigAckApplied {
		t.Fatalf("runtime config acknowledgements = %#v, want gatewayd-internal/node-a applied v%d", acks, row.Version)
	}
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile duplicate row: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("duplicate version was re-applied: %#v", applied)
	}
}

func TestWatcherReconcileResolvesScopedOverridesByPrecedence(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	seed := func(update state.RuntimeConfigUpdate) state.RuntimeConfig {
		t.Helper()
		row, err := store.UpsertRuntimeConfig(ctx, update)
		if err != nil {
			t.Fatalf("upsert %s/%s: %v", update.Scope, update.ScopeID, err)
		}
		if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
			t.Fatalf("apply %s/%s: %v", row.Scope, row.ScopeID, err)
		}
		return row
	}
	seed(state.RuntimeConfigUpdate{Key: KeyGatewayStreaming, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage(`true`)})
	seed(state.RuntimeConfigUpdate{Key: KeyGatewayStreaming, Scope: state.RuntimeConfigScopeDaemon, ScopeID: "gatewayd-internal", DesiredValue: json.RawMessage(`false`)})
	seed(state.RuntimeConfigUpdate{Key: KeyGatewayStreaming, Scope: state.RuntimeConfigScopeNode, ScopeID: "node-a", DesiredValue: json.RawMessage(`true`)})

	var got []bool
	w := New(store, nil, []string{KeyGatewayStreaming}, func(_ context.Context, _ string, value json.RawMessage, _ int64) error {
		enabled, err := Bool(value)
		if err != nil {
			return err
		}
		got = append(got, enabled)
		return nil
	}, nil).WithIdentity("gatewayd-internal", "node-a")
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile node override: %v", err)
	}
	if len(got) != 1 || !got[0] {
		t.Fatalf("node override result = %#v, want [true]", got)
	}

	w.NodeID = "node-b"
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile daemon override: %v", err)
	}
	if len(got) != 2 || got[1] {
		t.Fatalf("daemon override result = %#v, want [true false]", got)
	}

	w.Consumer = "other-daemon"
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile global fallback: %v", err)
	}
	if len(got) != 3 || !got[2] {
		t.Fatalf("global fallback result = %#v, want [true false true]", got)
	}
}

func TestWatcherReconcileCanaryFallsBackAndIsStable(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	global, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: KeyGatewayRawStream, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`false`),
	})
	if err != nil {
		t.Fatalf("upsert global: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, global.Key, global.Scope, global.ScopeID, global.Version, global.DesiredValue, ""); err != nil {
		t.Fatalf("apply global: %v", err)
	}
	percent := 0
	canary, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: KeyGatewayRawStream, Scope: state.RuntimeConfigScopeDaemon, ScopeID: "gatewayd-internal",
		DesiredValue: json.RawMessage(`true`), RolloutPercent: &percent,
	})
	if err != nil {
		t.Fatalf("upsert canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, canary.Key, canary.Scope, canary.ScopeID, canary.Version, canary.DesiredValue, ""); err != nil {
		t.Fatalf("apply canary: %v", err)
	}

	var applied []bool
	w := New(store, nil, []string{KeyGatewayRawStream}, func(_ context.Context, _ string, value json.RawMessage, _ int64) error {
		enabled, err := Bool(value)
		if err == nil {
			applied = append(applied, enabled)
		}
		return err
	}, nil).WithIdentity("gatewayd-internal", "node-a")
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile zero-percent canary: %v", err)
	}
	if len(applied) != 1 || applied[0] {
		t.Fatalf("zero-percent canary result = %#v, want [false]", applied)
	}

	// The same identity must remain in the same bucket after a later
	// percentage update; widening the rollout cannot reshuffle it.
	percent = 100
	canary, err = store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: canary.Key, Scope: canary.Scope, ScopeID: canary.ScopeID,
		DesiredValue: canary.DesiredValue, RolloutPercent: &percent,
		ExpectedVersion: &canary.Version,
	})
	if err != nil {
		t.Fatalf("widen canary: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, canary.Key, canary.Scope, canary.ScopeID, canary.Version, canary.DesiredValue, ""); err != nil {
		t.Fatalf("apply widened canary: %v", err)
	}
	if err := w.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile widened canary: %v", err)
	}
	if len(applied) != 2 || !applied[1] {
		t.Fatalf("widened canary result = %#v, want [false true]", applied)
	}
}
