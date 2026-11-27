// Package wakelatency builds the operator wake-latency report from the wake.*
// events (ADR-20261121200409): wake durations by method and node, the phases
// vmmd and schedd report for them, why wakes cold booted, and the slowest
// wakes and the apps that keep cold booting.
package wakelatency

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// Kinds are the event kinds the report reads.
var Kinds = []string{
	events.WakeBootStarted,
	events.WakeBootCompleted,
	events.WakeBootFailed,
	events.WakeRestoreBreakdown,
	events.WakeColdBootBreakdown,
	events.WakeThawBreakdown,
}

// Method labels. Schedd's own spans around the vmmd call are reported as
// phases of the "schedd" method so they rank beside vmmd's.
const (
	MethodRestore  = "restore"
	MethodColdBoot = "cold_boot"
	MethodThaw     = "thaw"
	MethodSchedd   = "schedd"
)

// triggerSnapshotRefresh is pkg/sched.TriggerSnapshotRefresh; apid must not
// import the scheduler.
const triggerSnapshotRefresh = "platform.snapshot_refresh"

// Input is one window's events, by kind, and the request's filters.
type Input struct {
	Window    string
	Since     time.Time
	Now       time.Time
	Events    map[string][]state.Event
	Truncated []string
	NodeID    string
	AppID     string
}

// Lookup names nodes and apps; a miss returns empty strings.
type Lookup interface {
	NodeName(id string) string
	App(id string) (slug, accountID string)
}

type started struct {
	method, coldReason, trigger string
	phases                      map[string]int64
}

type wake struct {
	key, wakeID, instanceID, appID, nodeID string
	method, tier, trigger, coldReason      string
	fallbackReason                         string
	fallback                               bool
	durationMS                             int64
	completedAt                            time.Time
	phases                                 map[string]int64
}

// Build aggregates in into the report.
func Build(in Input, look Lookup) api.ObsWakeLatencyReport {
	out := api.ObsWakeLatencyReport{
		Window: in.Window, Since: in.Since, AsOf: in.Now, TargetMS: api.ObsWakeLatencyRestoreTargetMS,
		Truncated: in.Truncated, Methods: []api.ObsWakeMethodStat{}, Nodes: []api.ObsWakeNodeStat{},
		Phases: []api.ObsWakePhaseStat{}, ColdReasons: []api.ObsWakeReasonCount{},
		FailureReasons: []api.ObsWakeReasonCount{}, Slowest: []api.ObsWakeSample{}, ColdBootApps: []api.ObsWakeAppStat{},
	}
	starts := map[string]started{}
	for _, e := range in.Events[events.WakeBootStarted] {
		d := payload(e.Data)
		k := key(d)
		if _, seen := starts[k]; seen && e.Actor != "schedd" {
			continue
		}
		starts[k] = started{method: d.str("method"), coldReason: d.str("cold_reason"), trigger: d.str("trigger"), phases: d.phases("pre_rpc_", "pre_wake_")}
	}
	breakdowns := map[string]map[string]map[string]int64{
		MethodRestore:  breakdownsOf(in.Events[events.WakeRestoreBreakdown]),
		MethodColdBoot: breakdownsOf(in.Events[events.WakeColdBootBreakdown]),
	}
	var wakes []wake
	for _, e := range in.Events[events.WakeBootCompleted] {
		w, refresh := completedWake(e, starts, breakdowns)
		if !in.selects(w.nodeID, w.appID) {
			continue
		}
		if refresh {
			out.Totals.PlatformRefreshes++
			continue
		}
		wakes = append(wakes, w)
	}
	for _, e := range in.Events[events.WakeThawBreakdown] {
		d := payload(e.Data)
		w := wake{key: key(d), wakeID: d.str("wake_id"), instanceID: d.str("instance_id"), appID: d.str("app_id"), nodeID: d.str("node_id"),
			method: MethodThaw, durationMS: d.num("total_ms"), completedAt: e.At, phases: map[string]int64{}}
		for name, ms := range d.phases("") {
			w.phases[MethodThaw+"/"+name] = ms
		}
		if in.selects(w.nodeID, w.appID) {
			wakes = append(wakes, w)
		}
	}
	failures := map[string]int{}
	failuresByNode := map[string]int{}
	for _, e := range in.Events[events.WakeBootFailed] {
		d := payload(e.Data)
		if !in.selects(d.str("node_id"), d.str("app_id")) || starts[key(d)].trigger == triggerSnapshotRefresh {
			continue
		}
		out.Totals.Failures++
		failures[orUnknown(d.str("reason"))]++
		failuresByNode[d.str("node_id")]++
	}
	out.FailureReasons = reasons(failures)
	summarize(&out, wakes, failuresByNode, look)
	return out
}

func (in Input) selects(nodeID, appID string) bool {
	return (in.NodeID == "" || in.NodeID == nodeID) && (in.AppID == "" || in.AppID == appID)
}

func completedWake(e state.Event, starts map[string]started, breakdowns map[string]map[string]map[string]int64) (wake, bool) {
	d := payload(e.Data)
	k := key(d)
	st := starts[k]
	w := wake{key: k, wakeID: d.str("wake_id"), instanceID: d.str("instance_id"), appID: d.str("app_id"), nodeID: d.str("node_id"),
		method: d.str("method"), tier: d.str("tier"), trigger: d.str("trigger"), coldReason: st.coldReason,
		fallbackReason: d.str("restore_fallback_reason"), completedAt: e.At}
	if w.trigger == "" {
		w.trigger = st.trigger
	}
	if s, c := d.time("started_at"), d.time("completed_at"); !s.IsZero() && !c.IsZero() && !c.Before(s) {
		w.durationMS, w.completedAt = c.Sub(s).Milliseconds(), c
	}
	w.fallback = w.method == MethodColdBoot && (st.method == MethodRestore || w.fallbackReason != "")
	if w.method == MethodColdBoot && w.coldReason == "" {
		w.coldReason = "unreported"
		if w.fallback {
			w.coldReason = "restore_fallback"
		}
	}
	w.phases = map[string]int64{}
	for name, ms := range breakdowns[w.method][k] {
		w.phases[w.method+"/"+name] = ms
	}
	for name, ms := range st.phases {
		w.phases[MethodSchedd+"/"+name] = ms
	}
	for name, ms := range d.phases("post_rpc_") {
		w.phases[MethodSchedd+"/"+name] = ms
	}
	return w, w.trigger == triggerSnapshotRefresh
}

func breakdownsOf(rows []state.Event) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	for _, e := range rows {
		d := payload(e.Data)
		if _, seen := out[key(d)]; !seen {
			out[key(d)] = d.phases("")
		}
	}
	return out
}

// summarize fills the method, node, phase, reason, slowest and app sections.
func summarize(out *api.ObsWakeLatencyReport, wakes []wake, failuresByNode map[string]int, look Lookup) {
	byMethod := map[string][]int64{}
	nodes := map[string]*nodeAcc{}
	phases := map[string][]int64{}
	cold := map[string]int{}
	apps := map[string]*appAcc{}
	node := func(id string) *nodeAcc {
		if nodes[id] == nil {
			nodes[id] = &nodeAcc{}
		}
		return nodes[id]
	}
	for _, w := range wakes {
		out.Totals.Wakes++
		byMethod[w.method] = append(byMethod[w.method], w.durationMS)
		n := node(w.nodeID)
		a := apps[w.appID]
		if a == nil {
			a = &appAcc{reasons: map[string]int{}}
			apps[w.appID] = a
		}
		a.wakes++
		a.durations = append(a.durations, w.durationMS)
		switch w.method {
		case MethodRestore:
			out.Totals.Restores++
			n.restores = append(n.restores, w.durationMS)
			a.restores++
			if w.durationMS > api.ObsWakeLatencyRestoreTargetMS {
				out.Totals.RestoresOverTarget++
				n.overTarget++
			}
		case MethodColdBoot:
			out.Totals.ColdBoots++
			n.coldBoots++
			a.coldBoots++
			cold[w.coldReason]++
			a.reasons[w.coldReason]++
		case MethodThaw:
			out.Totals.Thaws++
			n.thaws++
		}
		if w.fallback {
			out.Totals.Fallbacks++
			n.fallbacks++
			a.fallbacks++
		}
		for name, ms := range w.phases {
			phases[name] = append(phases[name], ms)
		}
	}
	for id, count := range failuresByNode {
		node(id).failures = count
	}
	out.Methods = methodStats(byMethod)
	out.Nodes = nodeStats(nodes, look)
	out.Phases = phaseStats(phases)
	out.ColdReasons = reasons(cold)
	out.Slowest = slowest(wakes, look)
	out.ColdBootApps = coldBootApps(apps, look)
}

type nodeAcc struct {
	restores                                          []int64
	coldBoots, thaws, failures, fallbacks, overTarget int
}

type appAcc struct {
	wakes, restores, coldBoots, fallbacks int
	durations                             []int64
	reasons                               map[string]int
}

func methodStats(byMethod map[string][]int64) []api.ObsWakeMethodStat {
	out := []api.ObsWakeMethodStat{}
	for method, ds := range byMethod {
		q := quantiles(ds)
		stat := api.ObsWakeMethodStat{Method: method, Count: len(ds), P50MS: q.p50, P95MS: q.p95, P99MS: q.p99, MaxMS: q.max}
		if method == MethodRestore {
			for _, d := range ds {
				if d > api.ObsWakeLatencyRestoreTargetMS {
					stat.OverTarget++
				}
			}
		}
		out = append(out, stat)
	}
	sort.Slice(out, func(i, j int) bool {
		return methodRank(out[i].Method) < methodRank(out[j].Method) || methodRank(out[i].Method) == methodRank(out[j].Method) && out[i].Method < out[j].Method
	})
	return out
}

func methodRank(m string) int {
	switch m {
	case MethodRestore:
		return 0
	case MethodColdBoot:
		return 1
	case MethodThaw:
		return 2
	}
	return 3
}

func nodeStats(nodes map[string]*nodeAcc, look Lookup) []api.ObsWakeNodeStat {
	out := []api.ObsWakeNodeStat{}
	for id, n := range nodes {
		q := quantiles(n.restores)
		out = append(out, api.ObsWakeNodeStat{NodeID: id, NodeName: look.NodeName(id), Restores: len(n.restores), ColdBoots: n.coldBoots,
			Thaws: n.thaws, Failures: n.failures, Fallbacks: n.fallbacks, RestoreP50MS: q.p50, RestoreP95MS: q.p95, RestoresOverTarget: n.overTarget})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RestoreP95MS != out[j].RestoreP95MS {
			return out[i].RestoreP95MS > out[j].RestoreP95MS
		}
		return out[i].NodeName+out[i].NodeID < out[j].NodeName+out[j].NodeID
	})
	return out
}

func phaseStats(phases map[string][]int64) []api.ObsWakePhaseStat {
	out := []api.ObsWakePhaseStat{}
	for name, ds := range phases {
		q := quantiles(ds)
		if q.max == 0 {
			continue
		}
		method, phase, _ := strings.Cut(name, "/")
		out = append(out, api.ObsWakePhaseStat{Method: method, Phase: phase, Samples: len(ds), P50MS: q.p50, P95MS: q.p95, MaxMS: q.max})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].P95MS != out[j].P95MS {
			return out[i].P95MS > out[j].P95MS
		}
		return out[i].Method+out[i].Phase < out[j].Method+out[j].Phase
	})
	return out
}

func slowest(wakes []wake, look Lookup) []api.ObsWakeSample {
	sorted := append([]wake(nil), wakes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].durationMS > sorted[j].durationMS })
	if len(sorted) > api.ObsWakeLatencySlowestMax {
		sorted = sorted[:api.ObsWakeLatencySlowestMax]
	}
	out := make([]api.ObsWakeSample, 0, len(sorted))
	for _, w := range sorted {
		slug, account := look.App(w.appID)
		s := api.ObsWakeSample{WakeID: w.wakeID, InstanceID: w.instanceID, AppID: w.appID, AppSlug: slug, AccountID: account,
			NodeID: w.nodeID, NodeName: look.NodeName(w.nodeID), Method: w.method, Tier: w.tier, Trigger: w.trigger,
			ColdReason: w.coldReason, FallbackReason: w.fallbackReason, DurationMS: w.durationMS, CompletedAt: w.completedAt}
		s.SlowPhase, s.SlowPhaseMS = slowPhase(w)
		out = append(out, s)
	}
	return out
}

// slowPhase is the wake's longest vmmd or thaw phase, falling back to
// schedd's spans when vmmd reported none.
func slowPhase(w wake) (string, int64) {
	best, bestMS, bestSchedd, bestScheddMS := "", int64(0), "", int64(0)
	for name, ms := range w.phases {
		method, phase, _ := strings.Cut(name, "/")
		if method == MethodSchedd {
			if ms > bestScheddMS || ms == bestScheddMS && ms > 0 && phase < bestSchedd {
				bestSchedd, bestScheddMS = name, ms
			}
			continue
		}
		if ms > bestMS || ms == bestMS && ms > 0 && phase < best {
			best, bestMS = name, ms
		}
	}
	if bestMS == 0 {
		return bestSchedd, bestScheddMS
	}
	return best, bestMS
}

func coldBootApps(apps map[string]*appAcc, look Lookup) []api.ObsWakeAppStat {
	out := []api.ObsWakeAppStat{}
	for id, a := range apps {
		if a.coldBoots == 0 {
			continue
		}
		top := reasons(a.reasons)
		out = append(out, api.ObsWakeAppStat{AppID: id, Wakes: a.wakes, Restores: a.restores, ColdBoots: a.coldBoots,
			Fallbacks: a.fallbacks, TopColdReason: top[0].Reason, P95MS: quantiles(a.durations).p95})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ColdBoots != out[j].ColdBoots {
			return out[i].ColdBoots > out[j].ColdBoots
		}
		return out[i].AppID < out[j].AppID
	})
	if len(out) > api.ObsWakeLatencyAppsMax {
		out = out[:api.ObsWakeLatencyAppsMax]
	}
	for i := range out {
		out[i].AppSlug, out[i].AccountID = look.App(out[i].AppID)
	}
	return out
}

func reasons(counts map[string]int) []api.ObsWakeReasonCount {
	out := make([]api.ObsWakeReasonCount, 0, len(counts))
	for r, c := range counts {
		out = append(out, api.ObsWakeReasonCount{Reason: r, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

type quant struct{ p50, p95, p99, max int64 }

// quantiles is the nearest-rank p50/p95/p99 and max of ds.
func quantiles(ds []int64) quant {
	if len(ds) == 0 {
		return quant{}
	}
	s := append([]int64(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := func(p int) int64 {
		i := (p*len(s) + 99) / 100
		if i < 1 {
			i = 1
		}
		return s[i-1]
	}
	return quant{p50: rank(50), p95: rank(95), p99: rank(99), max: s[len(s)-1]}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

type data map[string]any

func payload(raw json.RawMessage) data {
	d := data{}
	_ = json.Unmarshal(raw, &d)
	return d
}

func key(d data) string { return d.str("wake_id") + "/" + d.str("instance_id") }

func (d data) str(k string) string {
	s, _ := d[k].(string)
	return s
}

func (d data) num(k string) int64 {
	f, _ := d[k].(float64)
	if f < 0 {
		return 0
	}
	return int64(f)
}

func (d data) time(k string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, d.str(k))
	if err != nil {
		return time.Time{}
	}
	return t
}

// phases returns the payload's *_ms spans whose names start with one of
// prefixes (every span when prefixes is just ""), keyed by the span name
// without the _ms suffix. total_ms is the sum, not a phase.
func (d data) phases(prefixes ...string) map[string]int64 {
	out := map[string]int64{}
	for k, v := range d {
		if _, ok := v.(float64); !ok || !strings.HasSuffix(k, "_ms") || k == "total_ms" {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				out[strings.TrimSuffix(k, "_ms")] = d.num(k)
				break
			}
		}
	}
	return out
}
