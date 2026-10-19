package conformance

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testRuntimeConfigApplyReceiptsNeverRegress pins ADR-1700's receipt
// contract: one row per key, daemon and instance; an older version never
// replaces a newer one; a failure keeps its bounded error and an applied
// receipt clears it; listing filters by key and orders deterministically.
func testRuntimeConfigApplyReceiptsNeverRegress(t *testing.T, fx *Fixture) {
	t.Helper()
	key := "platform_flag.conformance_" + strings.ReplaceAll(fx.App.ID[:8], "-", "")
	other := key + "_other"
	record := func(r state.RuntimeConfigApplyReceipt) {
		t.Helper()
		if err := fx.Store.RecordRuntimeConfigApplyReceipt(fx.Ctx, r); err != nil {
			t.Fatalf("RecordRuntimeConfigApplyReceipt(%+v): %v", r, err)
		}
	}
	record(state.RuntimeConfigApplyReceipt{Key: key, Daemon: "outboundd", Instance: "cp-1", Version: 3, Status: state.RuntimeConfigReceiptApplied})
	record(state.RuntimeConfigApplyReceipt{Key: key, Daemon: "outboundd", Instance: "cp-1", Version: 2, Status: state.RuntimeConfigReceiptFailed, Error: "stale"})
	record(state.RuntimeConfigApplyReceipt{Key: key, Daemon: "apid", Instance: "cp-1", Version: 4, Status: state.RuntimeConfigReceiptFailed, Error: strings.Repeat("x", 2000)})
	record(state.RuntimeConfigApplyReceipt{Key: other, Daemon: "apid", Instance: "cp-1", Version: 1, Status: state.RuntimeConfigReceiptApplied})

	got, err := fx.Store.ListRuntimeConfigApplyReceipts(fx.Ctx, []string{key})
	if err != nil {
		t.Fatalf("ListRuntimeConfigApplyReceipts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("receipts for %s = %d, want 2: %+v", key, len(got), got)
	}
	if got[0].Daemon != "apid" || got[1].Daemon != "outboundd" {
		t.Fatalf("receipts not ordered by daemon: %+v", got)
	}
	if got[1].Version != 3 || got[1].Status != state.RuntimeConfigReceiptApplied || got[1].Error != "" {
		t.Fatalf("an older receipt regressed the record: %+v", got[1])
	}
	if got[0].Status != state.RuntimeConfigReceiptFailed || len(got[0].Error) != 1024 || got[0].Scope != state.RuntimeConfigScopeGlobal {
		t.Fatalf("failed receipt = status %s, error %d bytes, scope %s", got[0].Status, len(got[0].Error), got[0].Scope)
	}
	if got[0].ReportedAt.IsZero() {
		t.Fatal("receipt has no reported_at")
	}

	record(state.RuntimeConfigApplyReceipt{Key: key, Daemon: "apid", Instance: "cp-1", Version: 4, Status: state.RuntimeConfigReceiptApplied, Error: "ignored"})
	got, err = fx.Store.ListRuntimeConfigApplyReceipts(fx.Ctx, []string{key, other})
	if err != nil {
		t.Fatalf("ListRuntimeConfigApplyReceipts: %v", err)
	}
	if len(got) != 3 || got[0].Status != state.RuntimeConfigReceiptApplied || got[0].Error != "" {
		t.Fatalf("a successful retry of the same version must clear the error: %+v", got)
	}

	for _, bad := range []state.RuntimeConfigApplyReceipt{
		{Key: "", Daemon: "apid", Instance: "cp-1", Status: state.RuntimeConfigReceiptApplied},
		{Key: key, Daemon: "Not A Daemon", Instance: "cp-1", Status: state.RuntimeConfigReceiptApplied},
		{Key: key, Daemon: "apid", Instance: "", Status: state.RuntimeConfigReceiptApplied},
		{Key: key, Daemon: "apid", Instance: "cp-1", Version: -1, Status: state.RuntimeConfigReceiptApplied},
		{Key: key, Daemon: "apid", Instance: "cp-1", Status: "pending"},
	} {
		if err := fx.Store.RecordRuntimeConfigApplyReceipt(fx.Ctx, bad); err == nil {
			t.Fatalf("invalid receipt accepted: %+v", bad)
		}
	}
}
