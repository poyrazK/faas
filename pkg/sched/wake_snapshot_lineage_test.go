package sched

// spec: §12 — wake telemetry identifies the snapshot selected for this attempt.
// adr: 064 — the canonical boot event is the wake timeline's lineage anchor.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEngineWakeRecordsSelectedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		haveInit bool
		haveWarm bool
		stale    bool
		fallback bool
	}{
		{name: "cold boot"},
		{name: "init restore", haveInit: true},
		{name: "warm selected over init", haveInit: true, haveWarm: true},
		{name: "rejected snapshot omitted", haveInit: true, stale: true},
		{name: "attempt retained after cold fallback", haveInit: true, fallback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			var selected state.Snapshot
			if tc.haveInit {
				version := "1.10.0"
				if tc.stale {
					version = "old-version"
				}
				var err error
				selected, err = store.CreateSnapshot(ctx, state.Snapshot{
					DeploymentID: dep.ID, FCVersion: version, MemBytes: 512 << 20,
					StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "init-lineage"),
					Tier:       state.SnapshotTierInit,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.haveWarm {
				enableWarmSnapshot(t, store, app.ID)
				var err error
				selected, err = store.CreateSnapshot(ctx, state.Snapshot{
					DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 512 << 20,
					StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, "warm-lineage"),
					Tier:       state.SnapshotTierWarm,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			vmm := &fakeVMM{forceColdFallback: tc.fallback}
			e := wakeEngineWithEvents(t, store, vmm, &fakeNotifier{})
			res, err := e.Wake(ctx, app.ID, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			rows := eventuallyEventsForInstance(t, store, res.WakeID, 4)
			for _, row := range rows {
				if row.Kind != events.WakeBootStarted {
					continue
				}
				var data map[string]any
				if err := json.Unmarshal(row.Data, &data); err != nil {
					t.Fatal(err)
				}
				got, present := data["selected_snapshot_id"]
				if !tc.haveInit || tc.stale {
					if present || data["method"] != "cold_boot" || vmm.restores != 0 {
						t.Fatalf("cold boot claimed snapshot lineage: %v", data)
					}
					return
				}
				if got != selected.ID || data["method"] != "restore" || data["tier"] != selected.Tier {
					t.Fatalf("boot lineage = %v, want selected snapshot %s (%s)", data, selected.ID, selected.Tier)
				}
				if vmm.restores != 1 || vmm.lastSnapRef.StorageKey != selected.StorageKey {
					t.Fatalf("lineage differs from the snapshot dispatched to vmmd: %+v", vmm.lastSnapRef)
				}
				return
			}
			t.Fatal("missing canonical boot_started event")
		})
	}
}
