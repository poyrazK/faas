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
