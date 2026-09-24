package sched

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// adr: 005 — a wake that does not restore must say why. Every rejection
// branch of the snapshot choice maps to exactly one closed-set reason, and a
// usable snapshot carries none.
func TestChooseWakeSnapshotColdReasons(t *testing.T) {
	for _, tc := range []struct {
		name     string
		seed     *state.Snapshot // nil: no snapshot row at all
		protocol string
		wantOK   bool
		want     string
		rejected bool // the refused row is reported
	}{
		{name: "no snapshot", want: ColdReasonNoSnapshot},
		{name: "only a stale row", seed: &state.Snapshot{FCVersion: "1.10.0", Stale: true}, want: ColdReasonNoSnapshot},
		{name: "firecracker version changed", seed: &state.Snapshot{FCVersion: "1.7.0"}, want: ColdReasonFCVersion, rejected: true},
		{name: "legacy capture without drive", seed: &state.Snapshot{FCVersion: "1.10.0", StorageKey: "legacy"}, want: ColdReasonNoDrive, rejected: true},
		{name: "guest RAM changed", seed: &state.Snapshot{FCVersion: "1.10.0", MemBytes: 512 << 20}, want: ColdReasonRAM, rejected: true},
		{name: "h2 base image changed", seed: &state.Snapshot{FCVersion: "1.10.0", BaseImageVersion: "old"}, protocol: api.AppProtocolHTTP2, want: ColdReasonBaseImage, rejected: true},
		{name: "usable", seed: &state.Snapshot{FCVersion: "1.10.0"}, wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, _, dep := seedApp(t, store, api.PlanFree, 256, 5)
			var seeded state.Snapshot
			if tc.seed != nil {
				snap := *tc.seed
				snap.DeploymentID = dep.ID
				snap.Tier = state.SnapshotTierInit
				switch snap.StorageKey {
				case "":
					snap.StorageKey = state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "c1")
				case "legacy":
					snap.StorageKey = "snap/" + dep.ID + "/mem"
				}
				if snap.MemBytes == 0 {
					snap.MemBytes = 256 << 20
				}
				var err error
				if seeded, err = store.CreateSnapshot(context.Background(), snap); err != nil {
					t.Fatalf("CreateSnapshot: %v", err)
				}
			}
			protocol := tc.protocol
			if protocol == "" {
				protocol = api.AppProtocolHTTP1
			}
			e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			got := e.chooseWakeSnapshot(context.Background(), dep.ID, string(api.PlanFree), 256, protocol)
			if got.ok != tc.wantOK || got.coldReason != tc.want {
				t.Fatalf("choice ok=%v reason=%q, want ok=%v reason=%q", got.ok, got.coldReason, tc.wantOK, tc.want)
			}
			if tc.wantOK && (got.tier != wakeTierInit || got.snap.ID != seeded.ID) {
				t.Fatalf("usable choice = tier %q snap %q, want init %q", got.tier, got.snap.ID, seeded.ID)
			}
			if !tc.wantOK && got.tier != wakeTierColdBootFallback {
				t.Fatalf("cold choice tier = %q, want %q", got.tier, wakeTierColdBootFallback)
			}
			if tc.rejected && got.rejected.ID != seeded.ID {
				t.Fatalf("rejected snapshot = %q, want the refused row %q", got.rejected.ID, seeded.ID)
			}
		})
	}
	if fcvm.FAAS_BASE_IMAGE_VERSION == "old" {
		t.Fatal("test fixture collides with the current base image version")
	}
}

// adr: 005 — pkg/wire cannot import pkg/sched, so it carries its own copy of
// the reason set for metric pre-instantiation; the two must never drift.
func TestColdReasonsMatchWireLabels(t *testing.T) {
	if !reflect.DeepEqual(ColdReasons, wire.WakeColdReasons) {
		t.Fatalf("sched.ColdReasons %v != wire.WakeColdReasons %v", ColdReasons, wire.WakeColdReasons)
	}
}

// adr: 005 — the reason reaches the customer-facing timeline on
// wake.boot_started, and a restore carries none.
func TestEngineWake_BootStartedCarriesColdReason(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fcVersion string // "" seeds no snapshot
		want      string
	}{
		{name: "never snapshotted", want: ColdReasonNoSnapshot},
		{name: "firecracker version changed", fcVersion: "1.7.0", want: ColdReasonFCVersion},
		{name: "restores", fcVersion: "1.10.0", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			if tc.fcVersion != "" {
				if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
					DeploymentID: dep.ID, FCVersion: tc.fcVersion, MemBytes: 512 << 20,
					StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "c1"),
					Tier:       state.SnapshotTierInit,
				}); err != nil {
					t.Fatalf("CreateSnapshot: %v", err)
				}
			}
			e := wakeEngineWithEvents(t, store, &fakeVMM{}, &fakeNotifier{})
			res, err := e.Wake(context.Background(), app.ID, "", "", "")
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			rows := eventuallyEventsForInstance(t, store, res.WakeID, 3)
			for _, r := range rows {
				if r.Kind != "wake.boot_started" {
					continue
				}
				var data map[string]any
				if err := json.Unmarshal(r.Data, &data); err != nil {
					t.Fatal(err)
				}
				got, present := data["cold_reason"].(string)
				if tc.want == "" && present {
					t.Fatalf("restore carries cold_reason %q", got)
				}
				if got != tc.want {
					t.Fatalf("cold_reason = %q, want %q (data=%v)", got, tc.want, data)
				}
				return
			}
			t.Fatalf("no wake.boot_started row among %v", kindsOf(rows))
		})
	}
}

// historyStore reports snapshot history the way PgStore does (any row,
// stale or not). MemStore.HasSnapshotHistory always answers false.
type historyStore struct{ *state.MemStore }

func (historyStore) HasSnapshotHistory(context.Context, string) (bool, error) { return true, nil }

// adr: 005 — a deployment whose captures were all marked stale is reported
// as snapshots_stale, not as one that never had a snapshot.
func TestEngineWake_AllSnapshotsStaleReason(t *testing.T) {
	mem := state.NewMemStore()
	_, app, dep := seedApp(t, mem, api.PlanPro, 512, 5)
	if _, err := mem.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 512 << 20, Stale: true,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "c1"),
		Tier:       state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	store := historyStore{mem}
	e := wakeEngineWithEvents(t, store, &fakeVMM{}, &fakeNotifier{})
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	for _, r := range eventuallyEventsForInstance(t, store, res.WakeID, 3) {
		if r.Kind != "wake.boot_started" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(r.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["cold_reason"] != ColdReasonSnapshotsStale {
			t.Fatalf("cold_reason = %v, want %s", data["cold_reason"], ColdReasonSnapshotsStale)
		}
		return
	}
	t.Fatal("no wake.boot_started row")
}

// adr: 005 — a drive-less legacy row is retired when a wake refuses it, so
// the next park's capture takes the (deployment, tier) slot and the wake
// after that restores. Before, the legacy row kept the slot forever.
func TestChooseWakeSnapshotRetiresDrivelessRow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanFree, 256, 5)
	if _, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 256 << 20,
		StorageKey: "snap/" + dep.ID + "/captures/old/mem", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if got := e.chooseWakeSnapshot(ctx, dep.ID, string(api.PlanFree), 256, api.AppProtocolHTTP1); got.coldReason != ColdReasonNoDrive {
		t.Fatalf("first wake reason = %q, want %q", got.coldReason, ColdReasonNoDrive)
	}
	fresh, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 256 << 20,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "new1"),
		Tier:       state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatalf("the next capture still conflicts with the retired row: %v", err)
	}
	got := e.chooseWakeSnapshot(ctx, dep.ID, string(api.PlanFree), 256, api.AppProtocolHTTP1)
	if !got.ok || got.snap.ID != fresh.ID {
		t.Fatalf("second wake = ok %v snap %q reason %q, want a restore of %q", got.ok, got.snap.ID, got.coldReason, fresh.ID)
	}
}
