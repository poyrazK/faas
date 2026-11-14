package sched

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// oomBackoff stops a release that OOMs on every request from looping
// forever (bug hunt H34). Each OOM kill destroyed the instance and marked
// the deployment's warm and init snapshots stale on every platform, so the
// next wake cold-booted, OOMed again and re-staled both tiers, with no
// window like the liveness path's. Now only the first kill in
// api.WorkloadOOMBackoffWindow re-stales the snapshots, and once
// api.WorkloadOOMBackoffKills kills land in the window, wakes of the
// deployment get a 503 that names the OOM for a backoff that doubles from
// api.WorkloadOOMBackoffInitial up to api.WorkloadOOMBackoffMax. The release
// keeps its traffic (ADR-121 amendment: a live release is not failed over a
// memory spike); a kill-free window clears the backoff. The state is
// schedd-local: a restart forgets it, and the next kills rebuild it.
// The zero value is ready to use.
type oomBackoff struct {
	mu   sync.Mutex
	deps map[string]*oomDeploymentWindow
}

type oomDeploymentWindow struct {
	kills    []time.Time
	staledAt time.Time
	until    time.Time
	backoff  time.Duration
}

// record notes one OOM kill of deploymentID at now. It reports whether this
// kill should mark the deployment's snapshots stale (the first kill in the
// window) and whether it started or extended a wake backoff.
func (b *oomBackoff) record(deploymentID string, now time.Time) (staleSnapshots, backingOff bool) {
	if deploymentID == "" {
		return true, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deps == nil {
		b.deps = map[string]*oomDeploymentWindow{}
	}
	b.trimLocked(now)
	w := b.deps[deploymentID]
	if w == nil {
		w = &oomDeploymentWindow{}
		b.deps[deploymentID] = w
	}
	w.kills = append(w.kills, now)
	staleSnapshots = w.staledAt.IsZero() || now.Sub(w.staledAt) >= api.WorkloadOOMBackoffWindow
	if staleSnapshots {
		w.staledAt = now
	}
	if len(w.kills) < api.WorkloadOOMBackoffKills {
		return staleSnapshots, false
	}
	switch {
	case w.backoff <= 0:
		w.backoff = api.WorkloadOOMBackoffInitial
	case w.backoff < api.WorkloadOOMBackoffMax:
		w.backoff = min(2*w.backoff, api.WorkloadOOMBackoffMax)
	}
	w.until = now.Add(w.backoff)
	return staleSnapshots, true
}

// active reports how long wakes of deploymentID still back off.
func (b *oomBackoff) active(deploymentID string, now time.Time) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.deps[deploymentID]
	if w == nil || !now.Before(w.until) {
		return 0, false
	}
	return w.until.Sub(now), true
}

// trimLocked drops kills older than the window, and the windows of
// deployments with no kill and no backoff left.
func (b *oomBackoff) trimLocked(now time.Time) {
	for id, w := range b.deps {
		kept := w.kills[:0]
		for _, at := range w.kills {
			if now.Sub(at) < api.WorkloadOOMBackoffWindow {
				kept = append(kept, at)
			}
		}
		w.kills = kept
		if len(w.kills) == 0 && !now.Before(w.until) && now.Sub(w.staledAt) >= api.WorkloadOOMBackoffWindow {
			delete(b.deps, id)
		}
	}
}

// errWorkloadOOMBackoff is the wake refusal while a deployment backs off.
func errWorkloadOOMBackoff(retryAfter time.Duration) *api.Problem {
	seconds := max(int(retryAfter.Round(time.Second)/time.Second), 1)
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeAppRuntimeOOM,
		"App is out of memory",
		fmt.Sprintf("the app's instances were killed for exceeding the plan's memory %d times in %s; "+
			"Gregale waits %d s before starting it again. Raise the app's memory or fix the leak, then redeploy.",
			api.WorkloadOOMBackoffKills, api.WorkloadOOMBackoffWindow, seconds)).
		WithHeader("Retry-After", strconv.Itoa(seconds))
}
