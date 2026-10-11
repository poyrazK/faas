package sched

// Fleet-wide request rate for scale-in — ADR-038 amendment (H4-70).
//
// An app's home schedd decides scale-in from its local gateway's completions
// and its local vmmd's in-flight count. A gatewayd-internal forwards to
// instances on every node, so the home node can see almost no traffic for an
// app that is busy through the other node's gateway. On production-us a
// 50-106 rps closed-loop load on a 4-instance app read desired=0 and lost
// three instances at a time, five times in seven minutes, each loss followed
// by a cold start. instances.request_count is bumped by whichever gateway
// served the request (ReportActivity batches, every 250 ms), so its rate
// across an app's instances is the fleet-wide demand. Scale-in never goes
// below what that rate needs.

import (
	"math"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type requestSample struct {
	count int64
	at    time.Time
}

// fleetRequestRates remembers each running instance's request_count between
// reaper ticks.
type fleetRequestRates struct {
	mu   sync.Mutex
	last map[string]requestSample
}

// observe returns each app's request rate (per second) over the interval
// since the previous tick. An app is absent until at least one of its
// instances has a previous sample, so a first tick is "no signal", never
// zero.
func (f *fleetRequestRates) observe(snapshot []InstanceInfo, now time.Time) map[string]float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = make(map[string]requestSample)
	}
	rates := make(map[string]float64)
	seen := make(map[string]struct{}, len(snapshot))
	for _, in := range snapshot {
		if in.Instance == "" || in.AppID == "" {
			continue
		}
		seen[in.Instance] = struct{}{}
		prev, ok := f.last[in.Instance]
		f.last[in.Instance] = requestSample{count: in.RequestCount, at: now}
		if !ok {
			continue
		}
		dt := now.Sub(prev.at).Seconds()
		if dt <= 0 || in.RequestCount < prev.count {
			continue
		}
		rates[in.AppID] += float64(in.RequestCount-prev.count) / dt
	}
	for id := range f.last {
		if _, ok := seen[id]; !ok {
			delete(f.last, id)
		}
	}
	return rates
}

// fleetDemandReplicas is the instance count rps needs at targetRPS each.
func fleetDemandReplicas(rps float64, targetRPS int) int {
	if rps <= 0 || targetRPS <= 0 {
		return 0
	}
	return int(math.Ceil(rps / float64(targetRPS)))
}

func runningReaperInstances(snapshot []InstanceInfo) []InstanceInfo {
	out := make([]InstanceInfo, 0, len(snapshot))
	for _, in := range snapshot {
		if in.State == state.StateRunning {
			out = append(out, in)
		}
	}
	return out
}
