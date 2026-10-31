// adr: 570
package gateway

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Each result echoes its owner and immutable deployment. Missing or mismatched
// results cannot certify a cached target's readiness.
type TargetReadinessSnapshot struct {
	AppID, InstanceID, DeploymentID, WakeID, NodeID string
	RequiredSources                                 []string
	States                                          map[string]ReadinessState
}

type TargetReadinessLoader func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error)

type TargetReadinessRefreshStatus struct {
	StartedAt, At        time.Time
	Checked, Unavailable int
	EventID              int64
	Succeeded            bool
}

func (b *PGBackend) WithTargetReadinessLoader(loader TargetReadinessLoader) *PGBackend {
	b.readinessLoader = loader
	return b
}

func (b *PGBackend) TargetReadinessRefreshStatus() TargetReadinessRefreshStatus {
	if b != nil {
		if status := b.readinessLast.Load(); status != nil {
			return *status
		}
	}
	return TargetReadinessRefreshStatus{}
}

func (b *PGBackend) loadAdmissionReadiness(ctx context.Context, target *Target) error {
	if b.readinessLoader == nil {
		return nil
	}
	target.readinessVerificationRequired = true
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficReadinessReadTimeout)
	defer cancel()
	snapshots, err := b.readinessLoader(readCtx, []Target{*target})
	if err == nil {
		err = readCtx.Err()
	}
	if err != nil {
		target.ReadinessUnavailable = true
		return fmt.Errorf("verify admitted target readiness: %w", err)
	}
	if !applyTargetReadiness(target, snapshots[target.InstanceID], time.Now()) {
		return fmt.Errorf("verify admitted target readiness: missing deployment identity")
	}
	return nil
}

// Cancellation owns this loop independently of the notification subscription.
// Refresh waits are bounded and serialized; no repair goroutine outlives stop.
func (b *PGBackend) RunTargetReadinessReconciler(ctx context.Context) {
	if b == nil || b.readinessLoader == nil {
		return
	}
	ticker := time.NewTicker(api.TrafficReadinessReconcileInterval)
	defer ticker.Stop()
	for {
		if err := b.ReconcileTargetReadiness(ctx); err != nil && ctx.Err() == nil {
			b.log.Warn("gateway: target readiness repair failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *PGBackend) ReconcileTargetReadiness(ctx context.Context) error {
	if b == nil || b.readinessLoader == nil {
		return nil
	}
	b.readinessRefreshMu.Lock()
	defer b.readinessRefreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	targets := b.nextReadinessBatch()
	if len(targets) == 0 {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficReadinessReadTimeout)
	started := time.Now()
	snapshots, err := b.readinessLoader(readCtx, targets)
	if err == nil {
		err = readCtx.Err()
	}
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now := time.Now()
	status := TargetReadinessRefreshStatus{StartedAt: started, At: now, Succeeded: err == nil}
	b.tgtMu.Lock()
	for _, captured := range targets {
		picker := b.appsPicker[captured.AppID]
		if picker == nil {
			continue
		}
		for _, set := range picker.sets {
			for i := range set.entries {
				target := &set.entries[i]
				if target.InstanceID != captured.InstanceID || target.readinessGeneration != captured.readinessGeneration {
					continue
				}
				status.Checked++
				if err != nil {
					target.ReadinessUnavailable = true
				} else {
					if !b.applyTargetReadinessLocked(target, snapshots[target.InstanceID], now) {
						status.Succeeded = false
					}
				}
				if target.ReadinessUnavailable {
					status.Unavailable++
				}
				status.EventID = max(status.EventID, target.ReadinessEventID)
			}
		}
	}
	b.tgtMu.Unlock()
	b.readinessLast.Store(&status)
	return err
}

func applyTargetReadiness(target *Target, snapshot TargetReadinessSnapshot, now time.Time) bool {
	if snapshot.AppID != target.AppID || snapshot.InstanceID != target.InstanceID || snapshot.DeploymentID != target.DeploymentID || (target.WakeID != "" && (snapshot.WakeID != target.WakeID || snapshot.NodeID != target.NodeID)) {
		target.ReadinessUnavailable = true
		return false
	}
	target.readinessConfiguration = &targetReadinessConfiguration{identity: readinessIdentity(*target), sources: append([]string(nil), snapshot.RequiredSources...)}
	target.RequiresReadiness = len(snapshot.RequiredSources) > 0
	if !target.RequiresReadiness {
		target.ReadinessGates, target.ReadinessVerifiedUntil = nil, time.Time{}
		target.ReadinessUnavailable, target.Ready = false, true
		return true
	}
	gates := cloneReadinessGates(target.ReadinessGates)
	if gates == nil {
		gates = &ReadinessGates{States: make(map[string]ReadinessState)}
	}
	gates.RequiredSources = append([]string(nil), snapshot.RequiredSources...)
	missing := false
	for _, source := range gates.RequiredSources {
		incoming, ok := snapshot.States[source]
		if !ok || incoming.UpdatedAt.IsZero() {
			missing = true
			continue
		}
		current, exists := gates.States[source]
		if !exists || readinessAfter(incoming.UpdatedAt, incoming.EventID, current.UpdatedAt, current.EventID) {
			gates.States[source] = incoming
		}
		noteReadinessFreshness(target, incoming.UpdatedAt, incoming.EventID)
	}
	target.ReadinessGates = gates
	target.ReadinessUnavailable = missing
	target.ReadinessVerifiedUntil = now.Add(api.TrafficReadinessLease)
	target.Ready = target.routeReady()
	return true
}

type readinessBatchItem struct {
	key    string
	target Target
}
type readinessBatchHeap []readinessBatchItem

func (h readinessBatchHeap) Len() int           { return len(h) }
func (h readinessBatchHeap) Less(i, j int) bool { return h[i].key > h[j].key }
func (h readinessBatchHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *readinessBatchHeap) Push(value any)    { *h = append(*h, value.(readinessBatchItem)) }
func (h *readinessBatchHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func retainReadinessBatch(h *readinessBatchHeap, item readinessBatchItem) {
	if len(*h) < api.TrafficReadinessBatchSize {
		heap.Push(h, item)
		return
	}
	if item.key < (*h)[0].key {
		(*h)[0] = item
		heap.Fix(h, 0)
	}
}

// Retained scan memory and database inputs are bounded even when the cache is
// larger than one batch. The cursor rotates across apps and retained cohorts.
func (b *PGBackend) nextReadinessBatch() []Target {
	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	after, before := readinessBatchHeap{}, readinessBatchHeap{}
	for app, picker := range b.appsPicker {
		for _, set := range picker.sets {
			for _, target := range set.entries {
				if !target.RequiresReadiness && !target.ReadinessUnavailable &&
					(!target.readinessVerificationRequired || target.hasReadinessConfiguration()) {
					continue
				}
				target.AppID = app
				item := readinessBatchItem{key: app + "\x00" + target.InstanceID + "\x00" + target.DeploymentID, target: target}
				if item.key > b.readinessCursor {
					retainReadinessBatch(&after, item)
				} else {
					retainReadinessBatch(&before, item)
				}
			}
		}
	}
	sort.Slice(after, func(i, j int) bool { return after[i].key < after[j].key })
	sort.Slice(before, func(i, j int) bool { return before[i].key < before[j].key })
	items := append(after, before[:min(len(before), api.TrafficReadinessBatchSize-len(after))]...)
	targets := make([]Target, 0, len(items))
	for _, item := range items {
		target := item.target
		target.ReadinessGates = cloneReadinessGates(target.ReadinessGates)
		targets = append(targets, target)
		b.readinessCursor = item.key
	}
	return targets
}
