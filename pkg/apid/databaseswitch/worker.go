// Package databaseswitch runs rollbacks that restore the database
// (ADR-20261023181931). A switch restores the app's managed PostgreSQL
// database to before the releases it undoes, stages and verifies the app's
// credentials for the copy, fences and drains the app, makes the rollback
// target live, moves the bindings in one transaction and resumes the app.
// Any failure before that transaction aborts the switch and leaves the app
// serving the release it served before, on the source database.
package databaseswitch

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Databases restores and reads managed PostgreSQL databases.
type Databases interface {
	Get(ctx context.Context, accountID, id string) (managedpostgres.Database, error)
	FindByName(ctx context.Context, accountID, name string) (managedpostgres.Database, error)
	Restore(ctx context.Context, r managedpostgres.RestoreDatabaseRequest) (managedpostgres.Database, error)
}

// Cutovers stages, verifies, fences, activates, resumes and cancels the
// switch's cutover.
type Cutovers interface {
	Prepare(ctx context.Context, r managedpostgres.PrepareCutoverRequest) (managedpostgres.Cutover, error)
	Get(ctx context.Context, account, id string) (managedpostgres.Cutover, error)
	Verify(ctx context.Context, account, id string) (managedpostgres.Cutover, error)
	Cancel(ctx context.Context, account, id string) (managedpostgres.Cutover, error)
	Fence(ctx context.Context, account, id string) (managedpostgres.CutoverAdmissionFence, error)
	Activate(ctx context.Context, a managedpostgres.CutoverActivation) (managedpostgres.ActivatedCutover, error)
	Resume(ctx context.Context, account, id string) error
}

// Releases reads and moves the app's live release.
type Releases interface {
	LiveDeploymentForScope(ctx context.Context, appID, scope string) (state.Deployment, error)
	DeploymentByID(ctx context.Context, id string) (state.Deployment, error)
	PrepareDeploymentRollback(ctx context.Context, appID, targetDeploymentID string) (state.Deployment, error)
	MarkDeploymentLive(ctx context.Context, id string) error
}

// Event names a step an Observer records.
type Event string

const (
	EventFenced          Event = "fenced"
	EventReleaseSwitched Event = "release_switched"
	EventActivated       Event = "activated"
	EventCompleted       Event = "completed"
	EventAborting        Event = "aborting"
	EventFailed          Event = "failed"
	EventCancelled       Event = "cancelled"
)

// Worker advances database switches.
type Worker struct {
	Store     state.DatabaseSwitchStore
	Drain     state.DatabaseSwitchDrainStore
	Databases Databases
	Cutovers  Cutovers
	Releases  Releases
	// InvalidateSnapshots marks every snapshot of the app stale, so the first
	// wake after the switch cold-boots with the restored credentials.
	InvalidateSnapshots func(ctx context.Context, appID string) error
	// AppChanged tells the gateway and schedd that the app changed.
	AppChanged func(ctx context.Context, appID string)
	// Observe records audit rows for a step; nil records nothing.
	Observe func(ctx context.Context, sw state.DatabaseSwitch, event Event)
	Log     *slog.Logger
	Now     func() time.Time
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// Run advances due switches every api.DatabaseSwitchCheckInterval until ctx
// ends.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(api.DatabaseSwitchCheckInterval)
	defer ticker.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick claims the due switches and advances each one step.
func (w *Worker) Tick(ctx context.Context) {
	now := w.now()
	due, err := w.Store.ClaimDueDatabaseSwitches(ctx, uuid.NewString(), now, now.Add(api.DatabaseSwitchLease), api.DatabaseSwitchBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.log().Warn("database switch: claim due switches", "err", err)
		}
		return
	}
	for _, sw := range due {
		if ctx.Err() != nil {
			return
		}
		w.Advance(ctx, sw)
	}
}

// Advance runs one step of a leased switch and saves the result.
func (w *Worker) Advance(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	next := w.step(ctx, sw)
	saved, err := w.Store.SaveDatabaseSwitch(ctx, next, w.now())
	if err != nil {
		w.log().Warn("database switch: save progress", "switch", sw.ID, "phase", next.Phase, "err", err)
		return next
	}
	if saved.Phase != sw.Phase {
		w.log().Info("database switch: phase", "switch", saved.ID, "app", saved.AppID, "from", sw.Phase, "to", saved.Phase,
			"failure", saved.FailureCode)
	}
	if w.Observe != nil {
		for _, event := range transitionEvents(sw, saved) {
			w.Observe(ctx, saved, event)
		}
	}
	return saved
}

// transitionEvents lists the steps a save recorded, in order.
func transitionEvents(before, after state.DatabaseSwitch) []Event {
	var events []Event
	if before.FencedAt == nil && after.FencedAt != nil {
		events = append(events, EventFenced)
	}
	if before.ReleaseSwitchedAt == nil && after.ReleaseSwitchedAt != nil {
		events = append(events, EventReleaseSwitched)
	}
	if before.ActivatedAt == nil && after.ActivatedAt != nil {
		events = append(events, EventActivated)
	}
	if before.Phase != after.Phase {
		switch after.Phase {
		case state.DatabaseSwitchCompleted:
			events = append(events, EventCompleted)
		case state.DatabaseSwitchAborting:
			events = append(events, EventAborting)
		case state.DatabaseSwitchFailed:
			events = append(events, EventFailed)
		case state.DatabaseSwitchCancelled:
			events = append(events, EventCancelled)
		}
	}
	return events
}

func (w *Worker) step(ctx context.Context, sw state.DatabaseSwitch) state.DatabaseSwitch {
	if sw.CancelRequestedAt != nil && state.DatabaseSwitchCancellable(sw.Phase) {
		return w.beginAbort(sw, codeCancelled, "Cancelled on request before the switch.")
	}
	switch sw.Phase {
	case state.DatabaseSwitchRestoring:
		return w.restore(ctx, sw)
	case state.DatabaseSwitchStaging:
		return w.stage(ctx, sw)
	case state.DatabaseSwitchVerifying:
		return w.verify(ctx, sw)
	case state.DatabaseSwitchFencing:
		return w.fence(ctx, sw)
	case state.DatabaseSwitchSettling:
		return w.settle(ctx, sw)
	case state.DatabaseSwitchDraining:
		return w.drain(ctx, sw)
	case state.DatabaseSwitchSwitching:
		return w.switchOver(ctx, sw)
	case state.DatabaseSwitchResuming:
		return w.resume(ctx, sw)
	case state.DatabaseSwitchAborting:
		return w.abort(ctx, sw)
	}
	return w.wait(sw)
}

// Failure codes a switch can end with.
const (
	codeCancelled             = "cancelled"
	codeRestoreFailed         = "restore_failed"
	codeRestoreTimeout        = "restore_timeout"
	codeRestoreNameTaken      = "restore_name_taken"
	codeRestoreUnavailable    = "restore_unavailable"
	codeStagingFailed         = "staging_failed"
	codeVerificationFailed    = "verification_failed"
	codeFenceFailed           = "fence_failed"
	codeDrainBlocked          = "drain_blocked"
	codeDrainTimeout          = "drain_timeout"
	codeReleaseChanged        = "release_changed"
	codeReleaseSwitchFailed   = "release_switch_failed"
	codeActivationFailed      = "activation_failed"
	codeProvisioningClosed    = "provisioning_closed"
	codeCutoverCancelled      = "cutover_cancelled"
	maxTransientAttempts      = 30
	maxActivationAttempts     = 10
	transientBackoffCeiling   = time.Minute
	phaseUnfencedTimeoutLimit = api.DatabaseSwitchRestoreTimeout
)

// wait keeps the phase and checks again after the worker cadence.
func (w *Worker) wait(sw state.DatabaseSwitch) state.DatabaseSwitch {
	sw.RetryAt = w.now().Add(api.DatabaseSwitchCheckInterval)
	return sw
}

// retry records a transient error and backs off; after too many attempts
// the switch aborts.
func (w *Worker) retry(sw state.DatabaseSwitch, code string) state.DatabaseSwitch {
	sw.AttemptCount++
	sw.LastErrorCode = code
	if sw.AttemptCount >= maxTransientAttempts {
		return w.beginAbort(sw, code, "Gave up after repeated errors.")
	}
	backoff := api.DatabaseSwitchCheckInterval << min(sw.AttemptCount, 4)
	sw.RetryAt = w.now().Add(min(backoff, transientBackoffCeiling))
	return sw
}

// advanceTo moves the switch to its next phase.
func (w *Worker) advanceTo(sw state.DatabaseSwitch, phase string) state.DatabaseSwitch {
	now := w.now()
	sw.Phase, sw.PhaseStartedAt, sw.RetryAt = phase, now, now
	sw.AttemptCount, sw.LastErrorCode = 0, ""
	return sw
}

// beginAbort starts the compensation that ends the switch failed or
// cancelled.
func (w *Worker) beginAbort(sw state.DatabaseSwitch, code, detail string) state.DatabaseSwitch {
	if sw.ActivatedAt != nil {
		// Past the activation transaction there is no way back; finish.
		sw.LastErrorCode = code
		return w.wait(sw)
	}
	sw = w.advanceTo(sw, state.DatabaseSwitchAborting)
	sw.FailureCode, sw.FailureDetail = code, truncateDetail(detail)
	return sw
}

func (w *Worker) finish(sw state.DatabaseSwitch, phase string) state.DatabaseSwitch {
	sw = w.advanceTo(sw, phase)
	at := w.now()
	sw.FinishedAt = &at
	return sw
}

func truncateDetail(detail string) string {
	if len(detail) > 500 {
		return detail[:500]
	}
	return detail
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout"
	case errors.Is(err, managedpostgres.ErrProvisioningClosed):
		return codeProvisioningClosed
	case errors.Is(err, managedpostgres.ErrConflict), errors.Is(err, state.ErrConflict):
		return "conflict"
	case errors.Is(err, managedpostgres.ErrNotFound), errors.Is(err, state.ErrNotFound):
		return "not_found"
	case errors.Is(err, managedpostgres.ErrInvalid), errors.Is(err, state.ErrInvalidArgument):
		return "invalid"
	}
	return "unavailable"
}
