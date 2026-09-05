package meter

// Tests for the per-tick egress-byte reader the Sampler folds
// into usage_minutes.tx_bytes + usage_minutes.net_tx_bytes
// (ADR-046, step 8 / step 11). The tests use a fakeClock + a
// stub EgressSource to pin the egress path without standing up
// schedd / vmmd / gateway. The shared fakeEgressSource lives
// in fakes_test.go (mirror of fakeCPUSource).

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedMinuteUsageEgress is the test fixture: an account, app,
// instance, and a rolling minute. Mirrors seedMinuteUsageCPU
// in sampler_cpu_test.go but returns the accountID so the
// read-back path (UsageByHour / UsageByMonth) can key the
// query correctly.
func seedMinuteUsageEgress(t *testing.T) (state.Store, string, string, string, time.Time) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "u", RAMMB: 256, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	minute := time.Date(2026, 8, 17, 13, 25, 0, 0, time.UTC)
	return store, acct.ID, app.ID, ins.ID, minute
}

// TestSampler_AppendsEgressBytesToUsage asserts the happy
// path: the sampler's EgressSource reads are written to
// usage_minutes.tx_bytes and usage_minutes.net_tx_bytes. The
// fake source returns (1_000_000, 4_000_000); AppendUsage
// idempotently applies them, so the second SampleAndRoll in
// the same minute ADDS them (the additive-merge contract
// pinned by pkg/state's persistence test
// TestPg_AppendUsage_AddsTxBytesAndNetTxBytesOnConflict).
func TestSampler_AppendsEgressBytesToUsage(t *testing.T) {
	store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
	egress := newFakeEgressSource()
	egress.Set(instID, 1_000_000, 4_000_000)
	sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute })

	// First sample: tx_bytes = 1_000_000, net_tx_bytes = 4_000_000.
	if _, err := sampler.SampleAndRoll(context.Background()); err != nil {
		t.Fatalf("first SampleAndRoll: %v", err)
	}
	// Second sample in the same minute: ADDITIVE merge.
	// The fake returns the same per-tick values (production
	// vmmd polls every 250 ms, so within one minute the
	// sampler sees ~240 ticks; here we model two ticks).
	if _, err := sampler.SampleAndRoll(context.Background()); err != nil {
		t.Fatalf("second SampleAndRoll: %v", err)
	}

	// Read back: tx_bytes + net_tx_bytes should both be 2x
	// the per-tick value (additive merge).
	rows, err := store.UsageByHour(context.Background(), acctID,
		minute.Add(-time.Minute),
		minute.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	// tx_bytes: 1M + 1M = 2M (additive across the same minute).
	if rows[0].TXBytes != 2_000_000 {
		t.Errorf("TXBytes = %d, want 2_000_000 (1M + 1M additive merge)", rows[0].TXBytes)
	}
	// net_tx_bytes: 4M + 4M = 8M.
	if rows[0].NetTxBytes != 8_000_000 {
		t.Errorf("NetTxBytes = %d, want 8_000_000 (4M + 4M additive merge)", rows[0].NetTxBytes)
	}
}

// TestSampler_NoEgressSource_WritesZeroes asserts the
// egress=nil contract: when the sampler is wired without an
// EgressSource (the legacy PR-1 wiring or a meterd that hasn't
// loaded the schedd / gateway adapters), the sampler writes 0
// to BOTH egress columns with no error.
func TestSampler_NoEgressSource_WritesZeroes(t *testing.T) {
	store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
	_ = instID
	sampler := NewSamplerWithEgress(store, nil, nil, func() time.Time { return minute })

	for i := 0; i < 2; i++ {
		if _, err := sampler.SampleAndRoll(context.Background()); err != nil {
			t.Fatalf("SampleAndRoll[%d]: %v", i, err)
		}
	}
	rows, err := store.UsageByHour(context.Background(), acctID,
		minute.Add(-time.Minute),
		minute.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].TXBytes != 0 {
		t.Errorf("TXBytes = %d, want 0 (no source wired)", rows[0].TXBytes)
	}
	if rows[0].NetTxBytes != 0 {
		t.Errorf("NetTxBytes = %d, want 0 (no source wired)", rows[0].NetTxBytes)
	}
}

// TestSampler_EgressSourceMissingRow_WritesZeroes asserts the
// "no row for this instance" branch: when the source has no
// row for an instance (e.g. instance was just torn down, or
// the schedd reader has not yet polled it), the sampler writes
// 0 to BOTH egress columns. This is the same contract as the
// cpu path's "no row" branch (issue #279 / PR-B) — the
// additive-merge baseline stays put so the next valid sample
// picks up from the new counter.
func TestSampler_EgressSourceMissingRow_WritesZeroes(t *testing.T) {
	store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
	egress := newFakeEgressSource()
	egress.SetMissing(instID)
	sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute })

	for i := 0; i < 2; i++ {
		if _, err := sampler.SampleAndRoll(context.Background()); err != nil {
			t.Fatalf("SampleAndRoll[%d]: %v", i, err)
		}
	}
	rows, err := store.UsageByHour(context.Background(), acctID,
		minute.Add(-time.Minute),
		minute.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].TXBytes != 0 || rows[0].NetTxBytes != 0 {
		t.Errorf("egress bytes = (%d, %d), want (0, 0) when source has no row",
			rows[0].TXBytes, rows[0].NetTxBytes)
	}
}

// TestSampler_RolledRow_EgressFieldsPopulated asserts the
// RolledRow shape: when the source returns a row, the sampler
// stamps TXBytes + NetTxBytes on the returned row (not just on
// the persisted usage_minutes row). Tests that observe the
// returned []RolledRow (the test surface, telemetry) rely on
// this contract.
func TestSampler_RolledRow_EgressFieldsPopulated(t *testing.T) {
	store, _, _, instID, minute := seedMinuteUsageEgress(t)
	egress := newFakeEgressSource()
	egress.Set(instID, 7_000_000, 9_000_000)
	sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute })

	out, err := sampler.SampleAndRoll(context.Background())
	if err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rolled rows = %d, want 1", len(out))
	}
	if out[0].TXBytes != 7_000_000 {
		t.Errorf("RolledRow.TXBytes = %d, want 7_000_000", out[0].TXBytes)
	}
	if out[0].NetTxBytes != 9_000_000 {
		t.Errorf("RolledRow.NetTxBytes = %d, want 9_000_000", out[0].NetTxBytes)
	}
}

func TestSampler_DrainsFinalParkedInstanceActivity(t *testing.T) {
	store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
	if err := store.UpdateInstanceState(context.Background(), instID, string(state.StateParked)); err != nil {
		t.Fatal(err)
	}
	egress := newFakeEgressSource()
	egress.SetUsage(instID, UsageDeltas{
		TXBytes:       123,
		NetRXBytes:    456,
		Requests:      4,
		ColdBootCount: 1,
	})
	sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute })
	rows, err := sampler.SampleAndRoll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MBSeconds != 0 || rows[0].TXBytes != 123 || rows[0].NetRxBytes != 456 || rows[0].ColdBootCount != 1 {
		t.Fatalf("parked telemetry row = %+v", rows)
	}
	usage, err := store.UsageByHour(context.Background(), acctID, minute.Add(-time.Minute), minute.Add(time.Minute))
	if err != nil || len(usage) != 1 || usage[0].Requests != 4 {
		t.Fatalf("parked usage = %+v, err=%v", usage, err)
	}
}
