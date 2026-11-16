package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// activatePrimeWithoutSnapshot handles a prime whose instance booted healthy
// but whose init snapshot capture failed on the platform side (disk full, a
// vmmd PauseAndSnapshot error). Snapshots are a cache (ADR-005): the
// deployment is cold-bootable, so it goes live without one and its first
// request cold boots, instead of failing a release that works (deploy-path
// review P13). The instance lands in STOPPED, as a failed park capture does.
// A before_checkpoint hook failure is the app's own and still fails the
// deploy; the caller passes only InitSnapshotOutcomeSnapshotFailed here.
func (e *Engine) activatePrimeWithoutSnapshot(ctx context.Context, ins state.Instance, app state.App, startedAt time.Time, captureErr error) error {
	e.log.Warn("sched: prime snapshot capture failed; activating the deployment without a snapshot (first request cold boots)",
		"instance", ins.ID, "app", ins.AppID, "deployment", ins.DeploymentID, "node", ins.NodeID, "err", captureErr)
	payload := deploymentReadyPayload(ins.DeploymentID, executionModeForApp(app), ins.ID, state.SnapshotPrimeCaptureFailedReason)
	expected := ins
	expected.State = string(state.StateSnapshotting)
	if err := e.completePrimeHandoff(ctx, expected, state.StateStopped, db.NotifyDeploymentReady, payload); err != nil {
		return err
	}
	if e.events != nil {
		failedAt := time.Now().UTC()
		e.events.Emit(ctx, events.ParkFailed{
			EmitAt: failedAt, WakeID: ins.WakeID, AppID: ins.AppID, DeploymentID: ins.DeploymentID,
			InstanceID: ins.ID, NodeID: ins.NodeID, StartedAt: startedAt.UTC(), FailedAt: failedAt,
			Reason: state.SnapshotPrimeCaptureFailedReason,
		})
	}
	return e.sendPrimeHandoff(ctx, ins.DeploymentID, db.NotifyDeploymentReady, payload)
}
