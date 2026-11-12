package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// fingerprintStore counts full reads; fingerprint and fingerprintErr are what
// RuntimeConfigFingerprint answers.
type fingerprintStore struct {
	rows           []state.RuntimeConfig
	lists          int
	fingerprint    string
	fingerprintErr error
}

func (s *fingerprintStore) ListRuntimeConfigs(context.Context, state.RuntimeConfigScope, string) ([]state.RuntimeConfig, error) {
	s.lists++
	return append([]state.RuntimeConfig(nil), s.rows...), nil
}

func (s *fingerprintStore) RecordRuntimeConfigApplyReceipt(context.Context, state.RuntimeConfigApplyReceipt) error {
	return nil
}

func (s *fingerprintStore) RuntimeConfigFingerprint(context.Context, state.RuntimeConfigScope) (string, error) {
	return s.fingerprint, s.fingerprintErr
}

func appliedRow(version int64, value string) state.RuntimeConfig {
	return state.RuntimeConfig{Key: KeyHSTS, Scope: state.RuntimeConfigScopeGlobal, Version: version,
		Status: state.RuntimeConfigApplied, EffectiveValue: json.RawMessage(value)}
}

func TestWatcherRepairSkipsTheReadWhileTheFingerprintHolds(t *testing.T) {
	ctx := context.Background()
	store := &fingerprintStore{rows: []state.RuntimeConfig{appliedRow(1, `true`)}, fingerprint: "1:a"}
	var applied []int64
	w := New(store, nil, []string{KeyHSTS}, func(_ context.Context, _ string, _ json.RawMessage, version int64) error {
		applied = append(applied, version)
		return nil
	}, nil)

	for range 3 {
		if err := w.Repair(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if store.lists != 1 || len(applied) != 1 {
		t.Fatalf("three repairs over unchanged rows read %d times and applied %v, want 1 read and [1]", store.lists, applied)
	}

	store.rows, store.fingerprint = []state.RuntimeConfig{appliedRow(2, `false`)}, "1:b"
	if err := w.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	if store.lists != 2 || len(applied) != 2 || applied[1] != 2 {
		t.Fatalf("a changed fingerprint read %d times and applied %v, want 2 reads and [1 2]", store.lists, applied)
	}

	// A notification always reads, whatever the fingerprint says.
	if err := w.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if store.lists != 3 {
		t.Fatalf("a notification read %d times in all, want 3", store.lists)
	}
}

func TestWatcherRepairRetriesAFailedApplyEveryRepair(t *testing.T) {
	ctx := context.Background()
	store := &fingerprintStore{rows: []state.RuntimeConfig{appliedRow(1, `true`)}, fingerprint: "1:a"}
	fail := true
	w := New(store, nil, []string{KeyHSTS}, func(context.Context, string, json.RawMessage, int64) error {
		if fail {
			return errors.New("not yet")
		}
		return nil
	}, nil)

	for range 2 {
		if err := w.Repair(ctx); err == nil {
			t.Fatal("a failing apply reported no error")
		}
	}
	if store.lists != 2 {
		t.Fatalf("a failing apply was retried with %d reads, want 2", store.lists)
	}
	fail = false
	for range 2 {
		if err := w.Repair(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if store.lists != 3 {
		t.Fatalf("after the apply succeeded the rows were read %d times, want 3", store.lists)
	}
}

func TestWatcherRepairReadsWhenTheFingerprintFails(t *testing.T) {
	ctx := context.Background()
	store := &fingerprintStore{rows: []state.RuntimeConfig{appliedRow(1, `true`)}, fingerprintErr: errors.New("database away")}
	w := New(store, nil, []string{KeyHSTS}, func(context.Context, string, json.RawMessage, int64) error { return nil }, nil)
	for range 2 {
		if err := w.Repair(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if store.lists != 2 {
		t.Fatalf("without a fingerprint the rows were read %d times, want every repair (2)", store.lists)
	}
}

func TestWatcherRepairReadsEveryTimeWithoutAFingerprinter(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: KeyHSTS, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatal(err)
	}
	var applied int
	w := New(store, nil, []string{KeyHSTS}, func(context.Context, string, json.RawMessage, int64) error {
		applied++
		return nil
	}, nil)
	if err := w.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("repair without a fingerprinter applied %d rows, want 1", applied)
	}
}
