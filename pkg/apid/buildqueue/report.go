// Package buildqueue builds the operator build queue report
// (ADR-20261123163340): the builds waiting and running now, builder slot use
// per compute node, and the durations, failures and cache outcomes of the
// builds that finished in the window.
package buildqueue

import (
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Input is what the store and Prometheus returned. Active holds queued and
// running builds and Finished the builds that finished in the window, each
// with its truncation flag. Slots is builderd's slot gauges per node, valid
// only when SlotsState is observed.
type Input struct {
	Now               time.Time
	Window            time.Duration
	Active            []state.OperatorBuildRow
	ActiveTruncated   bool
	Finished          []state.OperatorBuildRow
	FinishedTruncated bool
	Slots             map[string]api.ObsBuilderNodeSlots
	SlotsState        string
}

// Build aggregates in into the report.
func Build(in Input) api.ObsBuildQueueReport {
	out := api.ObsBuildQueueReport{
		AsOf: in.Now, WindowSeconds: int(in.Window.Seconds()), BuildTimeoutSeconds: api.BuildTimeoutSeconds,
		WaitWarnSeconds: api.ObsBuildQueueWaitWarnSeconds, ActiveTruncated: in.ActiveTruncated,
		FinishedTruncated: in.FinishedTruncated, SlotsState: in.SlotsState,
	}
	kinds := map[string]*kindAcc{}
	kind := func(k string) *kindAcc {
		if kinds[k] == nil {
			kinds[k] = &kindAcc{}
		}
		return kinds[k]
	}
	running := map[string]int{}
	queued, runs := active(in, &out.Totals, kind, running)
	sort.SliceStable(queued, func(i, j int) bool { return queued[i].WaitSeconds > queued[j].WaitSeconds })
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].DurationSeconds > runs[j].DurationSeconds })
	out.Queued, out.Running = capped(queued, api.ObsBuildQueueListMax), capped(runs, api.ObsBuildQueueListMax)
	done, failures, failed := finished(in, &out.Totals, kind)
	sort.SliceStable(done, func(i, j int) bool { return done[i].DurationSeconds > done[j].DurationSeconds })
	out.Slowest, out.RecentFailures = capped(done, api.ObsBuildQueueSlowestMax), capped(failed, api.ObsBuildQueueSlowestMax)
	out.Failures = failureCounts(failures)
	out.Kinds = kindStats(kinds)
	out.Nodes = nodeSlots(running, in.Slots)
	return out
}

type kindAcc struct {
	queued, running, finished, failed int
	durations                         []float64
}

// active counts the queued and running builds. A queued build whose
// not_before is still ahead is deferred: counted apart and not listed. A
// queued build waiting on an identical build is held: it is queued and
// listed, but its wait is deliberate, so it does not raise the oldest wait
// or the warn count.
func active(in Input, t *api.ObsBuildQueueTotals, kind func(string) *kindAcc, running map[string]int) (queued, runs []api.ObsBuildRow) {
	queued, runs = []api.ObsBuildRow{}, []api.ObsBuildRow{}
	for _, b := range in.Active {
		r := row(b, in.Now)
		switch b.Status {
		case string(state.BuildQueued):
			if b.NotBefore.After(in.Now) {
				t.Deferred++
				continue
			}
			t.Queued++
			kind(b.Kind).queued++
			queued = append(queued, r)
			if r.Coalesced {
				t.Held++
				continue
			}
			t.OldestWaitSeconds = math.Max(t.OldestWaitSeconds, r.WaitSeconds)
			if r.WaitSeconds > api.ObsBuildQueueWaitWarnSeconds {
				t.WaitingOverWarn++
			}
		case string(state.BuildRunning):
			t.Running++
			kind(b.Kind).running++
			running[b.NodeName]++
			if r.DurationSeconds > api.BuildTimeoutSeconds {
				t.RunningOverTimeout++
			}
			runs = append(runs, r)
		}
	}
	return queued, runs
}

// finished counts the window's terminal builds. Durations and waits come
// from builds that started; a build cancelled while queued has neither.
func finished(in Input, t *api.ObsBuildQueueTotals, kind func(string) *kindAcc) (done []api.ObsBuildRow, failures map[string]int, failed []api.ObsBuildRow) {
	done, failed, failures = []api.ObsBuildRow{}, []api.ObsBuildRow{}, map[string]int{}
	var durations, waits []float64
	for _, b := range in.Finished {
		r := row(b, in.Now)
		k := kind(b.Kind)
		t.Finished++
		k.finished++
		switch b.Status {
		case string(state.BuildSucceeded):
			t.Succeeded++
		case string(state.BuildFailed):
			t.Failed++
			k.failed++
			failures[orDefault(b.FailureClass, "unclassified")]++
			failed = append(failed, r)
		case string(state.BuildCancelled):
			t.Cancelled++
		}
		switch b.CacheStatus {
		case "hit":
			t.CacheHits++
		case "miss":
			t.CacheMisses++
		case "invalidated":
			t.CacheInvalidated++
		}
		if b.Preemptions > 0 {
			t.Preempted++
		}
		if b.StartedAt.IsZero() || b.Status == string(state.BuildCancelled) {
			continue
		}
		durations, waits = append(durations, r.DurationSeconds), append(waits, r.WaitSeconds)
		k.durations = append(k.durations, r.DurationSeconds)
		done = append(done, r)
	}
	t.DurationP50Seconds, t.DurationP95Seconds = quantile(durations, 50), quantile(durations, 95)
	t.WaitP50Seconds, t.WaitP95Seconds = quantile(waits, 50), quantile(waits, 95)
	return done, failures, failed
}

func row(b state.OperatorBuildRow, now time.Time) api.ObsBuildRow {
	r := api.ObsBuildRow{ID: b.ID, DeploymentID: b.DeploymentID, AppID: b.AppID, AppSlug: b.AppSlug, AccountID: b.AccountID,
		Kind: b.Kind, Status: b.Status, FailureClass: b.FailureClass, CacheStatus: b.CacheStatus, Node: b.NodeName,
		SourceBytes: b.SourceBytes, Preemptions: b.Preemptions, Coalesced: b.CoalescedWith != "", EnqueuedAt: b.EnqueuedAt,
		NotBefore: timePtr(b.NotBefore), StartedAt: timePtr(b.StartedAt), FinishedAt: timePtr(b.FinishedAt)}
	// A deferred build waits only from its not_before (ADR-20261021221906).
	waitingSince := b.EnqueuedAt
	if b.NotBefore.After(waitingSince) {
		waitingSince = b.NotBefore
	}
	switch {
	case b.StartedAt.IsZero() && b.Status == string(state.BuildQueued):
		r.WaitSeconds = seconds(now.Sub(waitingSince))
	case !b.StartedAt.IsZero():
		r.WaitSeconds = seconds(b.StartedAt.Sub(waitingSince))
		end := b.FinishedAt
		if end.IsZero() {
			end = now
		}
		r.DurationSeconds = seconds(end.Sub(b.StartedAt))
	}
	return r
}

func failureCounts(failures map[string]int) []api.ObsBuildFailureCount {
	out := make([]api.ObsBuildFailureCount, 0, len(failures))
	for class, n := range failures {
		out = append(out, api.ObsBuildFailureCount{Class: class, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Class < out[j].Class
	})
	return out
}

func kindStats(kinds map[string]*kindAcc) []api.ObsBuildKindStat {
	out := make([]api.ObsBuildKindStat, 0, len(kinds))
	for k, a := range kinds {
		out = append(out, api.ObsBuildKindStat{Kind: k, Queued: a.queued, Running: a.running, Finished: a.finished,
			Failed: a.failed, DurationP95Seconds: quantile(a.durations, 95)})
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].Queued+out[i].Running+out[i].Finished, out[j].Queued+out[j].Running+out[j].Finished
		if ti != tj {
			return ti > tj
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// nodeSlots joins the database's running builds per node with builderd's
// slot gauges. A running build that has no node recorded yet is listed
// under "unrecorded".
func nodeSlots(running map[string]int, slots map[string]api.ObsBuilderNodeSlots) []api.ObsBuilderNodeSlots {
	byNode := map[string]api.ObsBuilderNodeSlots{}
	for node, s := range slots {
		s.Node, s.Observed = node, !s.Conflicting
		byNode[node] = s
	}
	for node, n := range running {
		if node == "" {
			node = "unrecorded"
		}
		s := byNode[node]
		s.Node = node
		s.RunningBuilds += n
		byNode[node] = s
	}
	out := make([]api.ObsBuilderNodeSlots, 0, len(byNode))
	for _, s := range byNode {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

func capped(rows []api.ObsBuildRow, n int) []api.ObsBuildRow {
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}

// quantile is the nearest-rank p-th percentile of xs, 0 when empty.
func quantile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := (p*len(s) + 99) / 100
	return s[max(i, 1)-1]
}

func seconds(d time.Duration) float64 {
	if d < 0 {
		return 0
	}
	return math.Round(d.Seconds()*1000) / 1000
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
