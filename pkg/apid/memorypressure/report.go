// Package memorypressure builds the operator memory pressure report
// (ADR-20261123190507): how close each compute node is to its RAM admission
// ceiling, measured and booked, and who was pushed out: RAM pressure
// evictions, frozen instances parked under memory pressure, and workload OOM
// kills.
package memorypressure

import (
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Node states.
const (
	StateOver       = "over"
	StateNear       = "near"
	StateOK         = "ok"
	StateStale      = "stale"
	StateUnmeasured = "unmeasured"
)

// Event kinds in the report.
const (
	KindEviction   = "eviction"
	KindFrozenPark = "frozen_park"
	KindOOMKill    = "oom_kill"
)

// Input is what the store returned: the capacity projection's nodes, the
// latest memory report per node, and the window's events newest first with
// Truncated when they hit the read cap.
type Input struct {
	Now       time.Time
	Window    time.Duration
	Nodes     []state.OperatorCapacityNode
	Reports   []state.NodeMemoryReport
	Events    []state.MemoryPressureEvent
	Truncated bool
}

// Build aggregates in into the report.
func Build(in Input) api.ObsMemoryPressureReport {
	out := api.ObsMemoryPressureReport{
		AsOf: in.Now, WindowSeconds: int(in.Window.Seconds()), OvercommitPercent: api.MeasuredAdmissionMaxOvercommitPercent,
		EvictionTargetPercent: api.MeasuredEvictionTargetPercent, MeasurementStaleSeconds: api.ObsMemoryMeasurementStaleSeconds,
		Truncated: in.Truncated, Nodes: []api.ObsMemoryNode{}, Reasons: []api.ObsMemoryReasonCount{}, Apps: []api.ObsMemoryApp{},
		Events: []api.ObsMemoryEvent{},
	}
	perNode := map[string]*api.ObsMemoryNode{}
	reports := map[string]state.NodeMemoryReport{}
	for _, r := range in.Reports {
		reports[r.NodeID] = r
	}
	for _, n := range in.Nodes {
		row := node(n, reports, in.Now)
		out.Nodes = append(out.Nodes, row)
	}
	for i := range out.Nodes {
		perNode[out.Nodes[i].NodeID] = &out.Nodes[i]
		total(&out.Totals, out.Nodes[i])
	}
	events(&out, in.Events, perNode)
	sort.SliceStable(out.Nodes, func(i, j int) bool {
		if ri, rj := stateRank(out.Nodes[i].State), stateRank(out.Nodes[j].State); ri != rj {
			return ri < rj
		}
		return out.Nodes[i].Name < out.Nodes[j].Name
	})
	return out
}

// node grades one node: over its ceiling, near it (inside the eviction band
// or with bookings at the overcommit cap), ok, or without a usable
// measurement. The memory graded is what schedd's measured-memory eviction
// compares with the ceiling: vmmd's UsedMB when reported, else admission's
// MeasuredMB.
func node(n state.OperatorCapacityNode, reports map[string]state.NodeMemoryReport, now time.Time) api.ObsMemoryNode {
	row := api.ObsMemoryNode{NodeID: n.ID, Name: n.Name, Active: n.Active, MemMB: n.MemMB, CeilingMB: n.AdmissionCeilingMB,
		BookedMB: n.RAMUsedMB, BookedLimitMB: int64(n.AdmissionCeilingMB) * api.MeasuredAdmissionMaxOvercommitPercent / 100,
		Running: n.InstancesRunning, Waking: n.InstancesWaking, ColdBooting: n.InstancesColdBooting, State: StateUnmeasured}
	r, ok := reports[n.ID]
	if !ok {
		return row
	}
	measured, at := r.MeasuredMB, r.MeasuredAt
	row.MeasuredMB, row.UsedMB, row.MeasuredAt, row.Frozen = &measured, r.UsedMB, &at, r.FrozenInstances
	age := math.Max(0, math.Round(now.Sub(at).Seconds()*10)/10)
	row.MeasurementAgeSeconds = &age
	ceiling, used := int64(n.AdmissionCeilingMB), int64(gradedMB(row))
	switch {
	case age > api.ObsMemoryMeasurementStaleSeconds:
		row.State = StateStale
	case used > ceiling:
		row.State = StateOver
	case used*100 >= ceiling*api.MeasuredEvictionTargetPercent || row.BookedMB >= row.BookedLimitMB:
		row.State = StateNear
	default:
		row.State = StateOK
	}
	return row
}

// gradedMB is the memory a measured node is graded on: UsedMB when reported,
// else MeasuredMB.
func gradedMB(n api.ObsMemoryNode) int {
	if n.UsedMB != nil {
		return *n.UsedMB
	}
	if n.MeasuredMB != nil {
		return *n.MeasuredMB
	}
	return 0
}

// total adds n to the fleet totals; report-derived figures (measured, used,
// frozen) count only nodes with a fresh report.
func total(t *api.ObsMemoryPressureTotals, n api.ObsMemoryNode) {
	t.Nodes++
	t.CeilingMB += int64(n.CeilingMB)
	t.BookedMB += n.BookedMB
	t.Running += n.Running
	if n.MeasuredMB != nil && n.State != StateStale {
		t.MeasuredMB += int64(*n.MeasuredMB)
		t.UsedMB += int64(gradedMB(n))
		t.Frozen += n.Frozen
	}
	switch n.State {
	case StateOver:
		t.NodesOver++
	case StateNear:
		t.NodesNear++
	case StateStale:
		t.NodesStale++
	}
}

type reasonKey struct{ kind, reason string }

// events counts the window's events by node, reason and app and keeps the
// newest. The input is newest first.
func events(out *api.ObsMemoryPressureReport, evs []state.MemoryPressureEvent, perNode map[string]*api.ObsMemoryNode) {
	reasons := map[reasonKey]int{}
	apps := map[string]*api.ObsMemoryApp{}
	for _, e := range evs {
		ev := event(e)
		reasons[reasonKey{ev.Kind, ev.Reason}]++
		n := perNode[e.NodeID]
		var a *api.ObsMemoryApp
		if e.AppID != "" {
			if a = apps[e.AppID]; a == nil {
				a = &api.ObsMemoryApp{AppID: e.AppID, AppSlug: e.AppSlug, AccountID: e.AccountID, Plan: e.Plan, LastAt: e.At}
				apps[e.AppID] = a
			}
		}
		switch ev.Kind {
		case KindEviction:
			out.Totals.Evictions++
			bump(n, a, func(n *api.ObsMemoryNode) { n.Evictions++ }, func(a *api.ObsMemoryApp) { a.Evictions++ })
		case KindFrozenPark:
			out.Totals.FrozenParks++
			bump(n, a, func(n *api.ObsMemoryNode) { n.FrozenParks++ }, func(a *api.ObsMemoryApp) { a.FrozenParks++ })
		case KindOOMKill:
			out.Totals.OOMKills++
			bump(n, a, func(n *api.ObsMemoryNode) { n.OOMKills++ }, func(a *api.ObsMemoryApp) {
				a.OOMKills++
				a.MaxPeakMB = max(a.MaxPeakMB, e.PeakMB)
				if a.PlanMB == 0 {
					a.PlanMB = e.PlanMB
				}
			})
		}
		if len(out.Events) < api.ObsMemoryPressureListMax {
			out.Events = append(out.Events, ev)
		}
	}
	out.Totals.AppsAffected = len(apps)
	for k, n := range reasons {
		out.Reasons = append(out.Reasons, api.ObsMemoryReasonCount{Kind: k.kind, Reason: k.reason, Count: n})
	}
	sort.Slice(out.Reasons, func(i, j int) bool {
		if out.Reasons[i].Count != out.Reasons[j].Count {
			return out.Reasons[i].Count > out.Reasons[j].Count
		}
		return out.Reasons[i].Kind+out.Reasons[i].Reason < out.Reasons[j].Kind+out.Reasons[j].Reason
	})
	for _, a := range apps {
		out.Apps = append(out.Apps, *a)
	}
	sort.Slice(out.Apps, func(i, j int) bool {
		ti, tj := appTotal(out.Apps[i]), appTotal(out.Apps[j])
		if ti != tj {
			return ti > tj
		}
		if !out.Apps[i].LastAt.Equal(out.Apps[j].LastAt) {
			return out.Apps[i].LastAt.After(out.Apps[j].LastAt)
		}
		return out.Apps[i].AppID < out.Apps[j].AppID
	})
	if len(out.Apps) > api.ObsMemoryPressureAppsMax {
		out.Apps = out.Apps[:api.ObsMemoryPressureAppsMax]
	}
}

func bump(n *api.ObsMemoryNode, a *api.ObsMemoryApp, onNode func(*api.ObsMemoryNode), onApp func(*api.ObsMemoryApp)) {
	if n != nil {
		onNode(n)
	}
	if a != nil {
		onApp(a)
	}
}

func appTotal(a api.ObsMemoryApp) int { return a.Evictions + a.FrozenParks + a.OOMKills }

// event maps a store row to the report's kinds.
func event(e state.MemoryPressureEvent) api.ObsMemoryEvent {
	kind := KindEviction
	switch e.Kind {
	case state.MemoryPressureKindFrozenPark:
		kind = KindFrozenPark
	case state.MemoryPressureKindOOMKill:
		kind = KindOOMKill
	}
	return api.ObsMemoryEvent{At: e.At, Kind: kind, Reason: e.Reason, InstanceID: e.InstanceID, AppID: e.AppID, AppSlug: e.AppSlug,
		AccountID: e.AccountID, Plan: e.Plan, NodeID: e.NodeID, NodeName: e.NodeName, RAMMB: e.RAMMB, PeakMB: e.PeakMB,
		PlanMB: e.PlanMB, DeploymentFailed: e.DeploymentFailed}
}

func stateRank(s string) int {
	switch s {
	case StateOver:
		return 0
	case StateNear:
		return 1
	case StateStale:
		return 2
	case StateUnmeasured:
		return 3
	default:
		return 4
	}
}
