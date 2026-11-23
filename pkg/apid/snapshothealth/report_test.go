package snapshothealth_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/snapshothealth"
	"github.com/onebox-faas/faas/pkg/state"
)

const mb = 1 << 20

var t0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func snap(id string, snapMB, layerMB int64, stale bool) state.SnapshotInventoryRow {
	r := state.SnapshotInventoryRow{ID: id, DeploymentID: "d-" + id, AppID: "a-" + id, AppSlug: "app-" + id, Tier: "init",
		FCVersion: "1.12.0", CPUTemplate: "T2CL", CPUModel: "icelake", Stale: stale, CreatedAt: t0.Add(-time.Hour),
		SnapshotBytes: snapMB * mb, LayerBytes: layerMB * mb}
	if stale {
		r.FCVersion = "1.11.0"
	}
	return r
}

func TestBuildFleetFigures(t *testing.T) {
	big := snap("big", 200, 40, false)
	warm := snap("warm", 90, 10, false)
	warm.Tier = "warm"
	pending := snap("pending", 60, 0, false)
	pending.DeletePending = true
	unpinned := snap("unpinned", 50, 10, false)
	unpinned.CPUTemplate, unpinned.CPUModel = "", ""
	oldStale := snap("old", 300, 0, true)
	oldStale.StaleAt = t0.Add(-48 * time.Hour)
	newStale := snap("new", 100, 0, true)
	newStale.StaleAt = t0.Add(-time.Hour)

	r := snapshothealth.Build(snapshothealth.Input{
		Now:       t0,
		Snapshots: []state.SnapshotInventoryRow{oldStale, big, newStale, warm, pending, unpinned},
		Unsnapshotted: []state.UnsnapshottedDeployment{
			{DeploymentID: "d-broken", AppID: "a-broken", ColdBootable: false, CreatedAt: t0},
			{DeploymentID: "d-cold", AppID: "a-cold", ColdBootable: true, CreatedAt: t0, LastStaleAt: t0.Add(-time.Hour)},
		},
	})

	want := api.ObsSnapshotTotals{Live: 4, Stale: 2, DeletePending: 1, LiveBytes: 460 * mb, AvgBytes: 115 * mb, P95Bytes: 240 * mb,
		OverWarn: 1, Unsnapshotted: 2, NotColdBootable: 1}
	if r.Totals != want {
		t.Fatalf("totals = %+v, want %+v", r.Totals, want)
	}
	if r.TargetMB != 130 || r.WarnMB != 160 || r.PageMB != 200 {
		t.Fatalf("thresholds = %d/%d/%d", r.TargetMB, r.WarnMB, r.PageMB)
	}
	if len(r.Largest) != 4 || r.Largest[0].ID != "big" || r.Largest[0].FootprintBytes != 240*mb {
		t.Fatalf("largest = %+v", r.Largest)
	}
	if len(r.RecentlyStale) != 2 || r.RecentlyStale[0].ID != "new" || r.RecentlyStale[0].StaleAt == nil {
		t.Fatalf("recently stale = %+v", r.RecentlyStale)
	}
	if r.FCVersions[0] != (api.ObsSnapshotGroup{Key: "1.12.0", Live: 4, AvgBytes: 115 * mb}) || r.FCVersions[1] != (api.ObsSnapshotGroup{Key: "1.11.0", Stale: 2}) {
		t.Fatalf("fc versions = %+v", r.FCVersions)
	}
	if len(r.Tiers) != 2 || r.Tiers[0].Key != "init" || r.Tiers[1] != (api.ObsSnapshotGroup{Key: "warm", Live: 1, AvgBytes: 100 * mb}) {
		t.Fatalf("tiers = %+v", r.Tiers)
	}
	cpus := map[string]api.ObsSnapshotGroup{}
	for _, g := range r.CPUTemplates {
		cpus[g.Key] = g
	}
	if cpus["T2CL/icelake"].Live != 3 || cpus["unpinned"].Live != 1 {
		t.Fatalf("cpu templates = %+v", r.CPUTemplates)
	}
	if len(r.Unsnapshotted) != 2 || r.Unsnapshotted[0].ColdBootable || r.Unsnapshotted[0].LastStaleAt != nil || r.Unsnapshotted[1].LastStaleAt == nil {
		t.Fatalf("unsnapshotted = %+v", r.Unsnapshotted)
	}
}

func TestBuildCapsLists(t *testing.T) {
	var rows []state.SnapshotInventoryRow
	for i := 0; i < api.ObsSnapshotListMax+5; i++ {
		rows = append(rows, snap(string(rune('a'+i)), 100, 0, false))
	}
	r := snapshothealth.Build(snapshothealth.Input{Now: t0, Snapshots: rows, Truncated: true, UnsnapshottedTruncated: true})
	if len(r.Largest) != api.ObsSnapshotListMax || r.Totals.Live != api.ObsSnapshotListMax+5 || !r.Truncated || !r.UnsnapshottedTruncated {
		t.Fatalf("report = largest %d live %d truncated %v/%v", len(r.Largest), r.Totals.Live, r.Truncated, r.UnsnapshottedTruncated)
	}
}

func TestBuildEmpty(t *testing.T) {
	raw, err := json.Marshal(snapshothealth.Build(snapshothealth.Input{Now: t0}))
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tiers", "fc_versions", "cpu_templates", "largest", "recently_stale", "unsnapshotted"} {
		if _, ok := back[k].([]any); !ok {
			t.Errorf("%s = %v, want an empty list", k, back[k])
		}
	}
}
