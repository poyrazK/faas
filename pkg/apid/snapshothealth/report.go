// Package snapshothealth builds the operator snapshot health report
// (ADR-20261122135018): the fleet's parked footprint against the 130 MB
// target, how snapshots split by tier, Firecracker version and CPU template,
// the largest and recently stale snapshots, and the live deployments that
// cold boot because they have no usable snapshot.
package snapshothealth

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const mb = 1 << 20

// Input is what the store returned: the snapshot inventory (largest first,
// with Truncated when it hit the read cap) and the live deployments with no
// usable snapshot (with UnsnapshottedTruncated likewise).
type Input struct {
	Now                    time.Time
	Snapshots              []state.SnapshotInventoryRow
	Truncated              bool
	Unsnapshotted          []state.UnsnapshottedDeployment
	UnsnapshottedTruncated bool
}

// Build aggregates in into the report.
func Build(in Input) api.ObsSnapshotHealthReport {
	out := api.ObsSnapshotHealthReport{
		AsOf: in.Now, TargetMB: api.FleetSnapshotAvgTargetMB, WarnMB: api.ObsSnapshotFleetAvgWarnMB, PageMB: api.ObsSnapshotFleetAvgPageMB,
		Truncated: in.Truncated, Tiers: []api.ObsSnapshotGroup{}, FCVersions: []api.ObsSnapshotGroup{}, CPUTemplates: []api.ObsSnapshotGroup{},
		Largest: []api.ObsSnapshotRow{}, RecentlyStale: []api.ObsSnapshotRow{}, Unsnapshotted: []api.ObsUnsnapshottedDeployment{},
		UnsnapshottedTruncated: in.UnsnapshottedTruncated,
	}
	tiers, versions, cpus := groups{}, groups{}, groups{}
	var live []int64
	var stale []state.SnapshotInventoryRow
	for _, r := range in.Snapshots {
		foot := r.SnapshotBytes + r.LayerBytes
		cpu := r.CPUTemplate + "/" + r.CPUModel
		if r.CPUTemplate == "" && r.CPUModel == "" {
			cpu = "unpinned"
		}
		tiers.add(r.Tier, r.Stale, foot)
		versions.add(r.FCVersion, r.Stale, foot)
		cpus.add(cpu, r.Stale, foot)
		if r.DeletePending {
			out.Totals.DeletePending++
		}
		if r.Stale {
			out.Totals.Stale++
			stale = append(stale, r)
			continue
		}
		out.Totals.Live++
		out.Totals.LiveBytes += foot
		live = append(live, foot)
		if foot > api.ObsSnapshotFleetAvgWarnMB*mb {
			out.Totals.OverWarn++
		}
		if len(out.Largest) < api.ObsSnapshotListMax {
			out.Largest = append(out.Largest, row(r))
		}
	}
	if len(live) > 0 {
		out.Totals.AvgBytes = out.Totals.LiveBytes / int64(len(live))
		out.Totals.P95Bytes = p95(live)
	}
	sort.SliceStable(stale, func(i, j int) bool { return stale[i].StaleAt.After(stale[j].StaleAt) })
	for _, r := range stale[:min(len(stale), api.ObsSnapshotListMax)] {
		out.RecentlyStale = append(out.RecentlyStale, row(r))
	}
	out.Tiers, out.FCVersions, out.CPUTemplates = tiers.list(), versions.list(), cpus.list()
	for _, d := range in.Unsnapshotted {
		out.Totals.Unsnapshotted++
		if !d.ColdBootable {
			out.Totals.NotColdBootable++
		}
		out.Unsnapshotted = append(out.Unsnapshotted, api.ObsUnsnapshottedDeployment{DeploymentID: d.DeploymentID, AppID: d.AppID,
			AppSlug: d.AppSlug, AccountID: d.AccountID, ColdBootable: d.ColdBootable, CreatedAt: d.CreatedAt, LastStaleAt: timePtr(d.LastStaleAt)})
	}
	return out
}

func row(r state.SnapshotInventoryRow) api.ObsSnapshotRow {
	return api.ObsSnapshotRow{ID: r.ID, DeploymentID: r.DeploymentID, DeploymentStatus: r.DeploymentStatus, AppID: r.AppID,
		AppSlug: r.AppSlug, AccountID: r.AccountID, Tier: r.Tier, FCVersion: r.FCVersion, CPUTemplate: r.CPUTemplate,
		CPUModel: r.CPUModel, Stale: r.Stale, DeletePending: r.DeletePending, SnapshotBytes: r.SnapshotBytes,
		LayerBytes: r.LayerBytes, FootprintBytes: r.SnapshotBytes + r.LayerBytes, CreatedAt: r.CreatedAt, StaleAt: timePtr(r.StaleAt)}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

type group struct {
	live, stale int
	liveBytes   int64
}

type groups map[string]*group

func (g groups) add(key string, stale bool, foot int64) {
	if key == "" {
		key = "unknown"
	}
	if g[key] == nil {
		g[key] = &group{}
	}
	if stale {
		g[key].stale++
		return
	}
	g[key].live++
	g[key].liveBytes += foot
}

// list is the groups with the most live snapshots first.
func (g groups) list() []api.ObsSnapshotGroup {
	out := make([]api.ObsSnapshotGroup, 0, len(g))
	for key, v := range g {
		s := api.ObsSnapshotGroup{Key: key, Live: v.live, Stale: v.stale}
		if v.live > 0 {
			s.AvgBytes = v.liveBytes / int64(v.live)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Live != out[j].Live {
			return out[i].Live > out[j].Live
		}
		if out[i].Stale != out[j].Stale {
			return out[i].Stale > out[j].Stale
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// p95 is the nearest-rank 95th percentile of ds.
func p95(ds []int64) int64 {
	s := append([]int64(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := (95*len(s) + 99) / 100
	return s[max(i, 1)-1]
}
