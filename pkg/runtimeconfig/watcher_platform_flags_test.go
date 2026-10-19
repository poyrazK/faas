package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1700 — watchers apply platform flags and report apply receipts.

const testAccount = "11111111-1111-4111-8111-111111111111"

func publishApplied(t *testing.T, store *state.MemStore, key string, value json.RawMessage) state.RuntimeConfig {
	t.Helper()
	ctx := context.Background()
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: key, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: value, ApplyMode: state.RuntimeConfigApplyHot})
	if err != nil {
		t.Fatalf("upsert %s: %v", key, err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, key, row.Scope, row.ScopeID, row.Version, value, ""); err != nil {
		t.Fatalf("acknowledge %s: %v", key, err)
	}
	return row
}

func TestWatcherKeepsPlatformFlagsCurrentAndReportsReceipts(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	set := platformflags.NewSet(nil)
	var hsts []bool
	keys, apply := WithPlatformFlags([]string{KeyHSTS}, func(_ context.Context, key string, value json.RawMessage, _ int64) error {
		enabled, err := Bool(value)
		if err != nil {
			return err
		}
		hsts = append(hsts, enabled)
		return nil
	}, set)
	w := New(store, nil, keys, apply, nil)
	w.Daemon, w.Instance = "outboundd", "cp-1"

	value := true
	def := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{{ID: "one", Accounts: []string{testAccount}, Value: &value}}}
	publishApplied(t, store, platformflags.ConfigKey(platformflags.AIGateway), def.Marshal())
	publishApplied(t, store, KeyHSTS, json.RawMessage(`false`))
	if err := w.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !set.Enabled(platformflags.AIGateway, platformflags.Subject{AccountID: testAccount}) {
		t.Fatal("platform flag row did not reach the set")
	}
	if len(hsts) != 1 || hsts[0] {
		t.Fatalf("non-flag key not routed to the daemon apply: %v", hsts)
	}
	receipts, err := store.ListRuntimeConfigApplyReceipts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 {
		t.Fatalf("receipts = %+v, want one per applied key", receipts)
	}
	for _, r := range receipts {
		if r.Daemon != "outboundd" || r.Instance != "cp-1" || r.Status != state.RuntimeConfigReceiptApplied || r.Version != 1 {
			t.Fatalf("receipt = %+v", r)
		}
	}
}

func TestWatcherReportsAFailingVersionOnce(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	calls := 0
	w := New(store, nil, []string{KeyS3}, func(context.Context, string, json.RawMessage, int64) error {
		calls++
		return errors.New("provider not configured")
	}, nil)
	w.Daemon, w.Instance = "s3-gatewayd", "cp-1"
	publishApplied(t, store, KeyS3, json.RawMessage(`true`))
	for i := 0; i < 3; i++ {
		if err := w.Reconcile(ctx); err == nil {
			t.Fatal("a failing apply must surface an error")
		}
	}
	if calls != 3 {
		t.Fatalf("apply calls = %d, want a retry on every reconcile", calls)
	}
	receipts, err := store.ListRuntimeConfigApplyReceipts(ctx, []string{KeyS3})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].Status != state.RuntimeConfigReceiptFailed || receipts[0].Error == "" {
		t.Fatalf("receipts = %+v, want one failed receipt with the error", receipts)
	}
}

func TestWatcherWithoutDaemonWritesNoReceipts(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	w := New(store, nil, []string{KeyHSTS}, func(context.Context, string, json.RawMessage, int64) error { return nil }, nil)
	publishApplied(t, store, KeyHSTS, json.RawMessage(`true`))
	if err := w.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if receipts, _ := store.ListRuntimeConfigApplyReceipts(ctx, nil); len(receipts) != 0 {
		t.Fatalf("receipts written without a daemon name: %+v", receipts)
	}
}
