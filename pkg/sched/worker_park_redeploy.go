package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// liftLivenessParkForNewWorkerRelease reactivates a worker app parked for
// liveness exhaustion once a deployment created after the park is live.
//
// A worker is never started by a request, and the worker reconciler admits
// nothing while the app is parked, so a fixed release deployed after a crash
// loop went "live" without running: the app still read "parked: liveness
// failed" and `app restart` refused it until someone ran `gregale wake`
// (agent eval 2026-11-23, voting-app worker). The new release is the
// customer's answer to the park, so it gets its own attempt; if it crash-loops
// too, the liveness window parks it again. Admin, lifecycle and security
// parks, and a customer park on top of the liveness park, are left alone. It reports whether the app was reactivated.
func (e *Engine) liftLivenessParkForNewWorkerRelease(ctx context.Context, app state.App, live []state.Deployment) (bool, error) {
	if app.Status != state.AppEvictedCold || len(live) == 0 {
		return false, nil
	}
	// A request wakes any other app, which clears the park itself.
	if instanceModeForApp(app) != string(state.InstanceModeWorker) && app.WorkloadClass != state.WorkloadClassWorker {
		return false, nil
	}
	parked, err := e.store.LatestParkedDeploymentForApp(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sched: load parked deployment for app %s: %w", app.ID, err)
	}
	if !newerReleaseSupersedesLivenessPark(parked, live) {
		return false, nil
	}
	// The liveness stamp outlives later parks: a customer who ran `app park`
	// on the crash-looped app wants it parked, whatever they deploy next. A
	// customer park records a park transition; a liveness park does not. A
	// store that cannot tell keeps the park.
	reader, canRead := e.store.(state.AppParkTransitionReader)
	if !canRead {
		return false, nil
	}
	transitionID, err := reader.CurrentAppParkTransitionID(ctx, app.ID)
	if err != nil {
		return false, fmt.Errorf("sched: load park transition for app %s: %w", app.ID, err)
	}
	if transitionID != "" {
		return false, nil
	}
	ok, err := compareAndSetAppStatus(ctx, e.store, app.ID, state.AppEvictedCold, state.AppActive)
	if err != nil {
		return false, fmt.Errorf("sched: reactivate worker app %s after redeploy: %w", app.ID, err)
	}
	if ok {
		e.log.Info("sched: new worker release lifts liveness park",
			"app", app.ID, "parked_deployment", parked.ID)
	}
	return ok, nil
}

// newerReleaseSupersedesLivenessPark reports whether parked is a liveness
// park and some live deployment other than it was created after the park.
func newerReleaseSupersedesLivenessPark(parked state.Deployment, live []state.Deployment) bool {
	if state.ParkReason(parked.ParkedReason) != state.ParkReasonLivenessExhausted || parked.ParkedAt == nil {
		return false
	}
	for _, dep := range live {
		if dep.ID != parked.ID && dep.Status == state.DeployLive && dep.CreatedAt.After(*parked.ParkedAt) {
			return true
		}
	}
	return false
}
