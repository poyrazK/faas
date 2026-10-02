// adr: 375
package gateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type TargetPlacement struct {
	Target
	DeploymentLive bool
}

type TargetPlacementSnapshot struct {
	AppID     string
	Complete  bool
	Targets   []TargetPlacement
	readiness map[string]TargetReadinessSnapshot
}

type TargetPlacementLoader func(context.Context, []string) (map[string]TargetPlacementSnapshot, error)

type TargetPlacementRefreshStatus struct {
	StartedAt, At                            time.Time
	Checked, Removed, Unavailable, Discarded int
	Succeeded                                bool
}

func (b *PGBackend) WithTargetPlacementLoader(loader TargetPlacementLoader) *PGBackend {
	b.placementLoader = loader
	return b
}

func (b *PGBackend) TargetPlacementRefreshStatus() TargetPlacementRefreshStatus {
	if b != nil {
		if status := b.placementLast.Load(); status != nil {
			return *status
		}
	}
	return TargetPlacementRefreshStatus{}
}

func (b *PGBackend) RunTargetPlacementReconciler(ctx context.Context) {
	if b == nil || b.placementLoader == nil {
		return
	}
	ticker := time.NewTicker(api.TrafficPlacementReconcileInterval)
	defer ticker.Stop()
	for {
		b.tgtMu.RLock()
		batches := (len(b.appsPicker) + api.TrafficPlacementAppBatchSize - 1) / api.TrafficPlacementAppBatchSize
		b.tgtMu.RUnlock()
		for range batches {
			if err := b.ReconcileTargetPlacements(ctx); err != nil && ctx.Err() == nil {
				b.log.Warn("gateway: target placement repair failed", "err", err)
			}
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *PGBackend) ReconcileTargetPlacements(ctx context.Context) error {
	if b == nil || b.placementLoader == nil {
		return nil
	}
	b.placementRefreshMu.Lock()
	defer b.placementRefreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	batch := b.nextPlacementBatch()
	if len(batch) == 0 {
		return nil
	}
	apps := make([]string, 0, len(batch))
	for _, captured := range batch {
		apps = append(apps, captured.app)
	}
	started := time.Now()
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficPlacementReadTimeout)
	snapshots, err := b.placementLoader(readCtx, apps)
	var readinessErr error
	if err == nil {
		for _, app := range apps {
			snapshot := snapshots[app]
			if validPlacementSnapshot(app, snapshot) && b.readinessLoader != nil {
				targets := make([]Target, 0, len(snapshot.Targets))
				for _, candidate := range snapshot.Targets {
					targets = append(targets, candidate.Target)
				}
				// A readiness error still permits removal of authoritatively absent
				// placements. Missing observations keep the remaining residents unready.
				states, loadErr := b.readinessLoader(readCtx, targets)
				if loadErr != nil {
					readinessErr = errors.Join(readinessErr, fmt.Errorf("placement readiness app %s: %w", app, loadErr))
				} else {
					snapshot.readiness = states
				}
				snapshots[app] = snapshot
			}
		}
		err = readCtx.Err()
	}
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now := time.Now()
	status := TargetPlacementRefreshStatus{StartedAt: started, At: now, Succeeded: err == nil && readinessErr == nil}
	b.tgtMu.Lock()
	for _, captured := range batch {
		picker := b.appsPicker[captured.app]
		if picker != captured.picker || picker.placementGeneration != captured.generation {
			status.Discarded++
			continue
		}
		status.Checked++
		snapshot := snapshots[captured.app]
		if err != nil || !validPlacementSnapshot(captured.app, snapshot) {
			status.Succeeded = false
			status.Unavailable++
			for _, set := range picker.sets {
				for i := range set.entries {
					set.entries[i].PlacementUnavailable = true
				}
			}
			continue
		}
		status.Removed += b.applyPlacementSnapshotLocked(picker, snapshot, now)
	}
	b.tgtMu.Unlock()
	b.placementLast.Store(&status)
	return errors.Join(err, readinessErr)
}

func validPlacementSnapshot(app string, snapshot TargetPlacementSnapshot) bool {
	if snapshot.AppID != app || !snapshot.Complete || len(snapshot.Targets) > api.TrafficPlacementTargetsPerApp {
		return false
	}
	seen := make(map[string]bool, len(snapshot.Targets))
	for _, target := range snapshot.Targets {
		if target.AppID != app || target.InstanceID == "" || target.NodeID == "" || target.DeploymentID == "" ||
			seen[target.InstanceID] || target.Port < 0 || target.Port > 65535 {
			return false
		}
		seen[target.InstanceID] = true
	}
	return true
}

func sameTargetPlacement(a, b Target) bool {
	port := func(p int) int {
		if p == 0 {
			return api.DefaultAppPort
		}
		return p
	}
	return (a.AppID == "" || a.AppID == b.AppID) && a.InstanceID == b.InstanceID && a.DeploymentID == b.DeploymentID &&
		a.NodeID == b.NodeID && a.WakeID == b.WakeID && port(a.Port) == port(b.Port)
}

func (b *PGBackend) applyPlacementSnapshotLocked(picker *appPicker, snapshot TargetPlacementSnapshot, now time.Time) int {
	candidates := make(map[string]Target, len(snapshot.Targets))
	for _, target := range snapshot.Targets {
		candidates[target.InstanceID] = target.Target
	}
	known := make(map[string]Target, len(snapshot.Targets))
	for _, set := range picker.sets {
		for _, target := range set.entries {
			if candidate, exists := candidates[target.InstanceID]; exists && candidate.DeploymentID == target.DeploymentID {
				known[target.InstanceID] = target
			}
		}
	}
	desired := make(map[string]Target, len(snapshot.Targets))
	for _, candidate := range snapshot.Targets {
		old, exists := known[candidate.InstanceID]
		if !candidate.DeploymentLive && (!exists || old.DeploymentID != candidate.DeploymentID) {
			continue // Preserve retained revision residents, without discovering retired cohorts.
		}
		target := candidate.Target
		if exists && sameTargetPlacement(old, target) {
			fresh := target
			target = old // Retain newer readiness notifications and activity.
			target.Region, target.CommitSHA, target.DeploymentTag = fresh.Region, fresh.CommitSHA, fresh.DeploymentTag
			target.DeploymentCreatedAt, target.ImageDigest = fresh.DeploymentCreatedAt, fresh.ImageDigest
		}
		target.AppID = snapshot.AppID
		if target.AddedAt.IsZero() {
			target.AddedAt = now
		}
		target.PlacementUnavailable = false
		target.PlacementVerifiedUntil = now.Add(api.TrafficPlacementLease)
		if b.readinessLoader != nil {
			b.applyTargetReadinessLocked(&target, snapshot.readiness[target.InstanceID], now)
		}
		desired[target.InstanceID] = target
	}
	removed := 0
	for _, set := range picker.sets {
		for i := len(set.entries) - 1; i >= 0; i-- {
			old := set.entries[i]
			next, exists := desired[old.InstanceID]
			if exists && sameTargetPlacement(old, next) {
				continue
			}
			old.AppID = snapshot.AppID
			if !exists || old.WakeID != next.WakeID || old.NodeID != next.NodeID {
				b.quarantineTargetLocked(old, now)
			}
			set.remove(old.InstanceID)
			removed++
		}
	}
	for _, candidate := range snapshot.Targets {
		if target, exists := desired[candidate.InstanceID]; exists {
			b.recordTargetLocked(snapshot.AppID, target)
		}
	}
	picker.placementGeneration++
	return removed
}

type placementBatchItem struct {
	app        string
	picker     *appPicker
	generation uint64
}

func retainPlacementBatch(batch *[]placementBatchItem, item placementBatchItem) {
	*batch = append(*batch, item)
	sort.Slice(*batch, func(i, j int) bool { return (*batch)[i].app < (*batch)[j].app })
	if len(*batch) > api.TrafficPlacementAppBatchSize {
		*batch = (*batch)[:api.TrafficPlacementAppBatchSize]
	}
}

// Both retained slices are bounded; the cursor rotates across known picker apps
// regardless of request activity, readiness configuration or current routability.
func (b *PGBackend) nextPlacementBatch() []placementBatchItem {
	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	after, before := []placementBatchItem{}, []placementBatchItem{}
	for app, picker := range b.appsPicker {
		item := placementBatchItem{app: app, picker: picker, generation: picker.placementGeneration}
		if app > b.placementCursor {
			retainPlacementBatch(&after, item)
		} else {
			retainPlacementBatch(&before, item)
		}
	}
	batch := append(after, before[:min(len(before), api.TrafficPlacementAppBatchSize-len(after))]...)
	if len(batch) > 0 {
		b.placementCursor = batch[len(batch)-1].app
	}
	return batch
}
