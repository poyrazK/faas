// adr: 531
package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrTargetPlacementChanged = errors.New("gateway: placement changed during hydration")

type placementHydrationFence struct{ changed bool }

// Only active reads of an absent picker retain a fence. An admission, eviction,
// or weight update cannot create/delete that picker and hide the intervening change.
func (b *PGBackend) markPlacementHydrationChangedLocked(app string) {
	if fence := b.placementHydrationFences[app]; fence != nil {
		fence.changed = true
	}
}

func (b *PGBackend) hydrateCurrentTargets(ctx context.Context, app string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := b.liveTargetHydration.DoChan("placements\x00"+app, func() (any, error) {
		return nil, b.hydrateCurrentTargetsOnce(ctx, app)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case done := <-result:
		return done.Err
	}
}

func (b *PGBackend) hydrateCurrentTargetsOnce(ctx context.Context, app string) error {
	b.tgtMu.Lock()
	picker := b.appsPicker[app]
	var generation uint64
	var fence *placementHydrationFence
	if picker != nil {
		generation = picker.placementGeneration
	} else {
		fence = &placementHydrationFence{}
		if b.placementHydrationFences == nil {
			b.placementHydrationFences = make(map[string]*placementHydrationFence)
		}
		b.placementHydrationFences[app] = fence
	}
	b.tgtMu.Unlock()
	defer func() {
		b.tgtMu.Lock()
		if fence != nil && b.placementHydrationFences[app] == fence {
			delete(b.placementHydrationFences, app)
		}
		b.tgtMu.Unlock()
	}()

	snapshots, placementErr, readinessErr := b.loadPlacementSnapshots(ctx, []string{app})
	if err := ctx.Err(); err != nil {
		return err // A caller's cancellation does not withdraw another request's cache.
	}
	snapshot := snapshots[app]
	if placementErr == nil && !validPlacementSnapshot(app, snapshot) {
		placementErr = fmt.Errorf("gateway: incomplete current placement for app %s", app)
	}
	b.tgtMu.Lock()
	defer b.tgtMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current := b.appsPicker[app]
	if current != picker || (picker != nil && picker.placementGeneration != generation) || (fence != nil && fence.changed) {
		return ErrTargetPlacementChanged
	}
	if placementErr != nil {
		if picker != nil {
			markPlacementUnavailable(picker)
		}
		return fmt.Errorf("gateway: hydrate current targets for app %s: %w", app, placementErr)
	}
	if picker == nil {
		picker = &appPicker{sets: make(map[string]*targetSet)}
		b.appsPicker[app] = picker
	}
	b.applyPlacementSnapshotLocked(picker, snapshot, time.Now())
	return readinessErr
}

func markPlacementUnavailable(picker *appPicker) {
	for _, set := range picker.sets {
		for i := range set.entries {
			set.entries[i].PlacementUnavailable = true
		}
	}
}

func (b *PGBackend) cachedTargetPlacement(app, instance string) (Target, bool) {
	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	if picker := b.appsPicker[app]; picker != nil {
		for _, set := range picker.sets {
			for _, target := range set.entries {
				if target.InstanceID == instance {
					target.ReadinessGates = cloneReadinessGates(target.ReadinessGates)
					return target, true
				}
			}
		}
	}
	return Target{}, false
}

// Both periodic repair and request hydration share one placement/readiness
// deadline. Only complete placement permits removals; readiness failures leave
// residents present and unready so scheduler capacity is never inferred away.
func (b *PGBackend) loadPlacementSnapshots(ctx context.Context, apps []string) (map[string]TargetPlacementSnapshot, error, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficPlacementReadTimeout)
	defer cancel()
	snapshots, err := b.placementLoader(readCtx, apps)
	if err != nil {
		return nil, err, nil
	}
	loaded := make(map[string]TargetPlacementSnapshot, len(apps))
	var readinessErr error
	for _, app := range apps {
		snapshot := snapshots[app]
		snapshot.readiness = nil
		if validPlacementSnapshot(app, snapshot) && b.readinessLoader != nil {
			targets := make([]Target, 0, len(snapshot.Targets))
			for _, candidate := range snapshot.Targets {
				targets = append(targets, candidate.Target)
			}
			states, loadErr := b.readinessLoader(readCtx, targets)
			if loadErr != nil {
				readinessErr = errors.Join(readinessErr, fmt.Errorf("placement readiness app %s: %w", app, loadErr))
			} else {
				snapshot.readiness = states
			}
		}
		loaded[app] = snapshot
	}
	return loaded, readCtx.Err(), readinessErr
}
