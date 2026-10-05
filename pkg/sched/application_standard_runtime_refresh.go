// adr: 592 — resident processes roll; idle services remain at zero residency.
package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

type applicationStandardRefreshKey struct{}

func (e *Engine) RefreshApplicationStandard(ctx context.Context, r state.ApplicationStandardRuntimeRefreshRequest) (CoordOutcome, error) {
	store, ok := e.store.(state.ApplicationStandardRuntimeRefreshStore)
	if !ok {
		return CoordOutcome{}, fmt.Errorf("sched: standard runtime handoff store unavailable")
	}
	current, err := store.CheckApplicationStandardRuntimeRefresh(ctx, r)
	if err != nil || !current {
		return CoordOutcome{}, err
	}
	app, err := e.store.AppByID(ctx, r.AppID)
	if err != nil {
		return CoordOutcome{}, err
	}
	r.AppID = app.ID // Preserve the store's physical spelling for legacy MemStore rows.
	ctx = context.WithValue(ctx, applicationStandardRefreshKey{}, r)
	out, refreshErr := e.restartApp(ctx, r.AppID, r.WakeID, true)
	current, err = store.CheckApplicationStandardRuntimeRefresh(ctx, r)
	if err != nil || !current {
		return CoordOutcome{}, err
	}
	if refreshErr != nil {
		return out, refreshErr
	}
	// Another wake may have been in flight when restartApp coalesced. Its
	// outcome alone cannot acknowledge this revision's durable handoff.
	if err := e.verifyApplicationStandardRefreshInstances(ctx, r); err != nil {
		return out, err
	}
	return out, nil
}

func (e *Engine) checkApplicationStandardRefresh(ctx context.Context) error {
	r, selected := ctx.Value(applicationStandardRefreshKey{}).(state.ApplicationStandardRuntimeRefreshRequest)
	if !selected {
		return nil
	}
	store, ok := e.store.(state.ApplicationStandardRuntimeRefreshStore)
	if !ok {
		return fmt.Errorf("sched: standard runtime handoff store unavailable")
	}
	current, err := store.CheckApplicationStandardRuntimeRefresh(ctx, r)
	if err != nil {
		return err
	}
	if !current {
		return state.ErrApplicationStandardRuntimeStale
	}
	return nil
}

func (e *Engine) standardRefreshInstanceStale(ctx context.Context, instance state.Instance) bool {
	r, selected := ctx.Value(applicationStandardRefreshKey{}).(state.ApplicationStandardRuntimeRefreshRequest)
	if !selected {
		return false
	}
	store, ok := e.store.(state.InstanceApplicationStandardAdmissionStore)
	if !ok {
		return true
	}
	capture, err := store.GetInstanceApplicationStandardAdmission(ctx, instance.ID)
	return err != nil || capture.DesiredRevision != r.Standard.DesiredRevision || capture.PersistedRevision != r.Standard.DesiredRevision || capture.EffectiveHash != r.Standard.EffectiveHash
}

func (e *Engine) verifyApplicationStandardRefreshInstances(ctx context.Context, r state.ApplicationStandardRuntimeRefreshRequest) error {
	instances, err := e.store.ListInstancesForApp(ctx, r.AppID)
	if err != nil {
		return err
	}
	for _, ins := range instances {
		if ins.State == string(state.StateSnapshotting) || ins.State == string(state.StateMigrating) || runtimeConfigResident(ins) && e.standardRefreshInstanceStale(ctx, ins) {
			return state.ErrApplicationStandardRefreshDeferred
		}
	}
	return e.checkApplicationStandardRefresh(ctx)
}

func applicationStandardRefreshLifecycleBusy(ctx context.Context, instances []state.Instance) bool {
	if _, selected := ctx.Value(applicationStandardRefreshKey{}).(state.ApplicationStandardRuntimeRefreshRequest); !selected {
		return false
	}
	for _, ins := range instances {
		if ins.State == string(state.StateSnapshotting) || ins.State == string(state.StateMigrating) {
			return true
		}
	}
	return false
}
