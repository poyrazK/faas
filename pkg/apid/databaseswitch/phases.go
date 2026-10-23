package databaseswitch

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// restore starts the restore into a new database and waits until the copy is
// ready. A database already restored from the same source to the same point
// under the switch's name is reused.
func (w *Worker) restore(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	if sw.TargetDatabaseID == "" {
		target, err := w.Databases.Restore(ctx, managedpostgres.RestoreDatabaseRequest{
			AccountID: sw.AccountID, SourceDatabaseID: sw.SourceDatabaseID, Name: sw.TargetName, PointInTime: sw.RestorePoint})
		switch {
		case err == nil:
			sw.TargetDatabaseID = target.ID
		case errors.Is(err, managedpostgres.ErrProvisioningClosed):
			return w.beginAbort(sw, codeProvisioningClosed, "Managed PostgreSQL provisioning is not open for this account.")
		case errors.Is(err, managedpostgres.ErrInvalid):
			return w.beginAbort(sw, codeRestoreUnavailable, "The provider refused the restore; the restore point may be outside the database's restore window.")
		case errors.Is(err, managedpostgres.ErrConflict):
			if _, findErr := w.Databases.FindByName(ctx, sw.AccountID, sw.TargetName); findErr == nil {
				return w.beginAbort(sw, codeRestoreNameTaken, fmt.Sprintf("A database named %s exists and is not a restore of the source to %s.",
					sw.TargetName, sw.RestorePoint.Format("2006-01-02T15:04:05Z07:00")))
			}
			return w.retry(sw, "source_not_ready")
		default:
			return w.retry(sw, errorCode(err))
		}
	}
	target, err := w.Databases.Get(ctx, sw.AccountID, sw.TargetDatabaseID)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	switch target.State {
	case managedpostgres.StateReady:
		return w.advanceTo(sw, state.DatabaseSwitchStaging)
	case managedpostgres.StateFailed, managedpostgres.StateDeleting, managedpostgres.StateDeleted:
		return w.beginAbort(sw, codeRestoreFailed, fmt.Sprintf("The restored database is %s.", target.State))
	}
	if w.now().Sub(sw.PhaseStartedAt) > api.DatabaseSwitchRestoreTimeout {
		return w.beginAbort(sw, codeRestoreTimeout, "The restored database did not become ready in time.")
	}
	return w.wait(sw)
}

// stage prepares the cutover from the source to the copy for the app and
// scope. The cutover takes the switch's ID, so a retried request finds it.
func (w *Worker) stage(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	if sw.CutoverID == "" {
		c, err := w.Cutovers.Prepare(ctx, managedpostgres.PrepareCutoverRequest{ID: sw.ID, AccountID: sw.AccountID, AppID: sw.AppID,
			Scope: sw.Scope, SourceDatabaseID: sw.SourceDatabaseID, TargetDatabaseID: sw.TargetDatabaseID})
		switch {
		case err == nil:
			sw.CutoverID = c.ID
		case errors.Is(err, managedpostgres.ErrProvisioningClosed):
			return w.beginAbort(sw, codeProvisioningClosed, "Managed PostgreSQL provisioning is not open for this account.")
		case errors.Is(err, managedpostgres.ErrConflict), errors.Is(err, managedpostgres.ErrInvalid), errors.Is(err, managedpostgres.ErrUnsupported):
			return w.beginAbort(sw, codeStagingFailed, "The app's bindings to the source database could not be staged on the copy: "+errorCode(err)+".")
		default:
			return w.retry(sw, errorCode(err))
		}
	}
	c, err := w.Cutovers.Get(ctx, sw.AccountID, sw.CutoverID)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	switch c.State {
	case managedpostgres.CutoverPrepared:
		if _, err := w.Cutovers.Verify(ctx, sw.AccountID, sw.CutoverID); err != nil {
			return w.retry(sw, errorCode(err))
		}
		return w.advanceTo(sw, state.DatabaseSwitchVerifying)
	case managedpostgres.CutoverVerifying, managedpostgres.CutoverVerified:
		return w.advanceTo(sw, state.DatabaseSwitchVerifying)
	case managedpostgres.CutoverCancelling, managedpostgres.CutoverCancelled:
		return w.beginAbort(sw, codeCutoverCancelled, "The cutover was cancelled.")
	}
	sw.LastErrorCode = c.LastErrorCode
	if w.now().Sub(sw.PhaseStartedAt) > phaseUnfencedTimeoutLimit {
		return w.beginAbort(sw, codeStagingFailed, "The copy's credentials were not staged in time.")
	}
	return w.wait(sw)
}

// verify waits for SQL verification of every staged credential.
func (w *Worker) verify(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	c, err := w.Cutovers.Get(ctx, sw.AccountID, sw.CutoverID)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	switch c.State {
	case managedpostgres.CutoverVerified:
		if c.VerificationFresh(w.now()) {
			return w.advanceTo(sw, state.DatabaseSwitchFencing)
		}
		if _, err := w.Cutovers.Verify(ctx, sw.AccountID, sw.CutoverID); err != nil {
			return w.retry(sw, errorCode(err))
		}
	case managedpostgres.CutoverPrepared:
		if _, err := w.Cutovers.Verify(ctx, sw.AccountID, sw.CutoverID); err != nil {
			return w.retry(sw, errorCode(err))
		}
	case managedpostgres.CutoverCancelling, managedpostgres.CutoverCancelled:
		return w.beginAbort(sw, codeCutoverCancelled, "The cutover was cancelled.")
	}
	sw.LastErrorCode = c.LastErrorCode
	if w.now().Sub(sw.PhaseStartedAt) > phaseUnfencedTimeoutLimit {
		return w.beginAbort(sw, codeVerificationFailed, "The copy's credentials could not be verified in time.")
	}
	return w.wait(sw)
}

// fence installs the app-wide admission fence. From here the app does not
// serve until the switch resumes it or aborts.
func (w *Worker) fence(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	c, err := w.Cutovers.Get(ctx, sw.AccountID, sw.CutoverID)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	if c.State != managedpostgres.CutoverVerified || !c.VerificationFresh(w.now()) {
		if c.State == managedpostgres.CutoverVerified || c.State == managedpostgres.CutoverPrepared {
			if _, err := w.Cutovers.Verify(ctx, sw.AccountID, sw.CutoverID); err != nil {
				return w.retry(sw, errorCode(err))
			}
		}
		return w.advanceTo(sw, state.DatabaseSwitchVerifying)
	}
	f, err := w.Cutovers.Fence(ctx, sw.AccountID, sw.CutoverID)
	if errors.Is(err, managedpostgres.ErrConflict) {
		sw = w.retry(sw, "fence_conflict")
		if sw.AttemptCount >= 3 && sw.Phase == state.DatabaseSwitchFencing {
			return w.beginAbort(sw, codeFenceFailed, "The app could not be fenced: it is not active, or another operation holds it.")
		}
		return sw
	}
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	at := f.FencedAt.UTC()
	sw.FencedAt = &at
	return w.advanceTo(sw, state.DatabaseSwitchSettling)
}

// settle waits until no instance of the fenced app would block a drain:
// undispatched wakes, tasks and migrations finish or fail under the fence.
func (w *Worker) settle(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	preview, err := w.Drain.PreviewDrainBlockers(ctx, sw.AppID)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	sw.Drain = state.DatabaseSwitchDrain{Pending: preview.Supported, Parked: preview.Parked, Blocked: preview.Blocking}
	if preview.Settled() {
		now := w.now()
		sw.DrainRequestedAt = &now
		return w.advanceTo(sw, state.DatabaseSwitchDraining)
	}
	if w.now().Sub(sw.PhaseStartedAt) > api.DatabaseSwitchSettleTimeout {
		if preview.Overflow {
			return w.beginAbort(sw, codeDrainBlocked, "The app has more instances than one drain can capture.")
		}
		return w.beginAbort(sw, codeDrainBlocked, fmt.Sprintf(
			"%d instances could not be drained (frozen, starting, migrating, or a running task).", preview.Blocking))
	}
	return w.wait(sw)
}

// drain waits for schedd to capture the plan and retire every member.
func (w *Worker) drain(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	plan, found, err := w.Drain.DrainPlanForCutover(ctx, sw.CutoverID, sw.AppID, *sw.FencedAt)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	if found {
		sw.Drain = drainCounts(plan)
		if managedpostgres.RollbackDrainComplete(plan) {
			return w.advanceTo(sw, state.DatabaseSwitchSwitching)
		}
		if sw.Drain.Blocked > 0 || drainPlanBlocked(plan) {
			return w.beginAbort(sw, codeDrainBlocked, fmt.Sprintf("The drain cannot retire %d instances.", max(sw.Drain.Blocked, 1)))
		}
	}
	if w.now().Sub(sw.PhaseStartedAt) > api.DatabaseSwitchDrainTimeout {
		return w.beginAbort(sw, codeDrainTimeout, "The app's instances were not retired in time.")
	}
	return w.wait(sw)
}

func drainCounts(plan state.ManagedPostgresDrainPlan) state.DatabaseSwitchDrain {
	var d state.DatabaseSwitchDrain
	for _, m := range plan.Members {
		switch {
		case m.State == "retired":
			d.Retired++
		case m.BlockerCode == "parked_instance_unproven":
			d.Parked++
		case m.State == "pending":
			d.Pending++
		default:
			d.Blocked++
		}
	}
	return d
}

func drainPlanBlocked(plan state.ManagedPostgresDrainPlan) bool {
	for _, b := range plan.Blockers {
		if b != "inventory_empty" {
			return true
		}
	}
	return false
}

// switchOver makes the target the live release, then moves the bindings in
// the activation transaction. Before that commit everything is undone by an
// abort; after it there is no way back.
func (w *Worker) switchOver(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	if sw.ReleaseSwitchedAt == nil {
		return w.switchRelease(ctx, sw)
	}
	c, err := w.Cutovers.Get(ctx, sw.AccountID, sw.CutoverID)
	if err != nil {
		return w.retry(sw, errorCode(err))
	}
	if c.State == managedpostgres.CutoverActivated {
		return w.activated(sw)
	}
	if c.State == managedpostgres.CutoverVerifying || (c.State == managedpostgres.CutoverVerified && !c.VerificationFresh(w.now())) {
		if c.State == managedpostgres.CutoverVerified {
			if _, err := w.Cutovers.Verify(ctx, sw.AccountID, sw.CutoverID); err != nil {
				return w.retry(sw, errorCode(err))
			}
		}
		return w.wait(sw)
	}
	_, err = w.Cutovers.Activate(ctx, managedpostgres.CutoverActivation{AccountID: sw.AccountID, CutoverID: sw.CutoverID,
		FencedAt: *sw.FencedAt, LiveDeploymentID: sw.TargetDeploymentID, Now: w.now()})
	switch code := managedpostgres.ActivationBlockedCode(err); {
	case err == nil:
		return w.activated(sw)
	case code == managedpostgres.ActivationVerificationStale:
		if _, err := w.Cutovers.Verify(ctx, sw.AccountID, sw.CutoverID); err != nil {
			return w.retry(sw, errorCode(err))
		}
		return w.wait(sw)
	case code != "":
		return w.beginAbort(sw, codeActivationFailed, "The bindings could not be switched: "+code+".")
	}
	sw.AttemptCount++
	sw.LastErrorCode = errorCode(err)
	if sw.AttemptCount >= maxActivationAttempts {
		return w.beginAbort(sw, codeActivationFailed, "The bindings could not be switched after repeated errors.")
	}
	sw.RetryAt = w.now().Add(api.DatabaseSwitchCheckInterval)
	return sw
}

func (w *Worker) activated(sw state.DatabaseSwitch) state.DatabaseSwitch {
	at := w.now()
	sw.ActivatedAt = &at
	return w.advanceTo(sw, state.DatabaseSwitchResuming)
}

// switchRelease makes the target live while the app is fenced. It checks
// that the release recorded at request time still serves.
func (w *Worker) switchRelease(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	live, err := w.Releases.LiveDeploymentForScope(ctx, sw.AppID, sw.Scope)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return w.retry(sw, errorCode(err))
	}
	if live.ID != sw.TargetDeploymentID {
		if live.ID != sw.FromDeploymentID {
			return w.beginAbort(sw, codeReleaseChanged, "Another release started serving after the switch was requested.")
		}
		target, err := w.Releases.DeploymentByID(ctx, sw.TargetDeploymentID)
		if err != nil {
			return w.retry(sw, errorCode(err))
		}
		if target.Status == state.DeploySuperseded || (target.Status == state.DeployLive && target.TrafficPercent == 0) {
			if _, err := w.Releases.PrepareDeploymentRollback(ctx, sw.AppID, sw.TargetDeploymentID); err != nil {
				return w.beginAbort(sw, codeReleaseSwitchFailed, "The rollback target could not be prepared: "+errorCode(err)+".")
			}
		}
		if err := w.Releases.MarkDeploymentLive(ctx, sw.TargetDeploymentID); err != nil {
			return w.beginAbort(sw, codeReleaseSwitchFailed, "The rollback target could not be made live: "+errorCode(err)+".")
		}
	}
	at := w.now()
	sw.ReleaseSwitchedAt = &at
	sw.AttemptCount, sw.LastErrorCode = 0, ""
	sw.RetryAt = at
	return sw
}

// resume invalidates the app's snapshots and lifts the fence. Both steps
// are idempotent, so a crash between them repeats them.
func (w *Worker) resume(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	if w.InvalidateSnapshots != nil {
		if err := w.InvalidateSnapshots(ctx, sw.AppID); err != nil {
			return w.retry(sw, errorCode(err))
		}
	}
	if err := w.Cutovers.Resume(ctx, sw.AccountID, sw.CutoverID); err != nil {
		return w.retry(sw, errorCode(err))
	}
	if w.AppChanged != nil {
		w.AppChanged(ctx, sw.AppID)
	}
	sw.LastErrorCode = ""
	return w.finish(sw, state.DatabaseSwitchCompleted)
}

// abort undoes everything done before the activation transaction: it makes
// the previous release live again if the target was switched in, then
// cancels the cutover, whose cancellation revokes the staged credentials and
// lifts the fence and the pins. The restored database is kept.
func (w *Worker) abort(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	if sw.ReleaseSwitchedAt != nil && sw.FromDeploymentID != "" {
		live, err := w.Releases.LiveDeploymentForScope(ctx, sw.AppID, sw.Scope)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return w.retry(sw, errorCode(err))
		}
		if live.ID == sw.TargetDeploymentID {
			if _, err := w.Releases.PrepareDeploymentRollback(ctx, sw.AppID, sw.FromDeploymentID); err != nil &&
				!errors.Is(err, state.ErrRollbackTargetAlreadyLive) {
				return w.retry(sw, errorCode(err))
			}
			if err := w.Releases.MarkDeploymentLive(ctx, sw.FromDeploymentID); err != nil {
				return w.retry(sw, errorCode(err))
			}
		}
	}
	if sw.CutoverID != "" {
		c, err := w.Cutovers.Get(ctx, sw.AccountID, sw.CutoverID)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return w.retry(sw, errorCode(err))
		}
		switch {
		case err != nil:
		case c.State == managedpostgres.CutoverActivated:
			// The activation committed after all: finish the switch.
			return w.activated(sw)
		case c.State != managedpostgres.CutoverCancelled:
			if c.State != managedpostgres.CutoverCancelling {
				if _, err := w.Cutovers.Cancel(ctx, sw.AccountID, sw.CutoverID); err != nil {
					return w.retry(sw, errorCode(err))
				}
			}
			return w.wait(sw)
		}
	}
	if sw.FailureCode == codeCancelled {
		return w.finish(sw, state.DatabaseSwitchCancelled)
	}
	return w.finish(sw, state.DatabaseSwitchFailed)
}
