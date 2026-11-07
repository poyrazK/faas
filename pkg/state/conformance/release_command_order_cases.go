// adr: 1448 — amendment 1: release commands run one at a time per app scope,
// oldest first, and none after one whose outcome is unknown until checked.
package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type releaseOrderFixture struct {
	t    *testing.T
	fx   *Fixture
	base time.Time
}

func (r releaseOrderFixture) deploy(scope string) state.Deployment {
	r.t.Helper()
	d, err := r.fx.Store.CreateDeployment(r.fx.Ctx, state.Deployment{AppID: r.fx.App.ID, Kind: state.DeploymentKindImage,
		Scope: scope, ImageDigest: "sha256:" + scope, ReleaseCommand: []string{"bin/migrate"}})
	if err != nil {
		r.t.Fatalf("CreateDeployment: %v", err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err := r.fx.Store.UpdateDeploymentStatus(r.fx.Ctx, d.ID, status, ""); err != nil {
			r.t.Fatalf("UpdateDeploymentStatus(%s): %v", status, err)
		}
	}
	if err := r.fx.Store.SetDeploymentRootfs(r.fx.Ctx, d.ID, "/local/"+d.ID+".ext4", "apps/conformance/"+d.ID+".ext4", 4096); err != nil {
		r.t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	return d
}

func (r releaseOrderFixture) task(d state.Deployment, at time.Duration) state.AppTask {
	r.t.Helper()
	task, err := r.fx.Store.CreateAppTask(r.fx.Ctx, state.CreateAppTaskParams{
		AccountID: r.fx.Account.ID, AppID: r.fx.App.ID, DeploymentID: d.ID,
		Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: r.base.Add(at),
	})
	if err != nil {
		r.t.Fatalf("CreateAppTask(release): %v", err)
	}
	return task
}

func (r releaseOrderFixture) claim(at, lease time.Duration) (state.AppTask, error) {
	return r.fx.Store.ClaimNextAppTask(r.fx.Ctx, "schedd-a", r.base.Add(at), lease)
}

// testReleaseCommandOrder pins that release commands of one app scope run
// one at a time, oldest first, while another scope runs alongside, and that
// a command which ended without an exit code holds the next one until a
// person records a check.
func testReleaseCommandOrder(t *testing.T, fx *Fixture) {
	outcomes, ok := fx.Store.(state.ReleaseOutcomeStore)
	if !ok {
		t.Skip("store has no release outcome checks")
	}
	// Times run from now: a store may start a release command at its own
	// clock when that is later than the worker's.
	r := releaseOrderFixture{t: t, fx: fx, base: time.Now().UTC().Truncate(time.Microsecond)}
	v1, v2, staging := r.deploy("production"), r.deploy("production"), r.deploy("staging")
	first, second, other := r.task(v1, 0), r.task(v2, time.Second), r.task(staging, 2*time.Second)

	got, err := r.claim(3*time.Second, time.Minute)
	if err != nil || got.ID != first.ID {
		t.Fatalf("first claim = %s (err=%v), want the oldest release command %s", got.ID, err, first.ID)
	}
	if got, err = r.claim(3*time.Second, time.Hour); err != nil || got.ID != other.ID {
		t.Fatalf("second claim = %s (err=%v), want the other scope's command %s", got.ID, err, other.ID)
	}
	if got, err = r.claim(3*time.Second, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("third claim = %s (err=%v), want the newer command to wait", got.ID, err)
	}
	if _, err := fx.Store.MarkAppTaskRunning(fx.Ctx, first.ID, *mustClaimToken(t, fx, first.ID), r.base.Add(3*time.Second+500*time.Millisecond)); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	// The worker is lost: the running command fails without an exit code.
	if _, err := fx.Store.SweepExpiredAppTasks(fx.Ctx, r.base.Add(2*time.Minute)); err != nil {
		t.Fatalf("SweepExpiredAppTasks: %v", err)
	}
	if got, err = r.claim(2*time.Minute+time.Second, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim after a lost command = %s (err=%v), want it held until checked", got.ID, err)
	}
	uncertain, err := outcomes.UncertainReleaseTask(fx.Ctx, fx.Account.ID, fx.App.ID, "production", v2.ID)
	if err != nil || uncertain.ID != first.ID {
		t.Fatalf("UncertainReleaseTask = %s (err=%v), want %s", uncertain.ID, err, first.ID)
	}
	if _, err := outcomes.UncertainReleaseTask(fx.Ctx, fx.Account.ID, fx.App.ID, "staging", ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("UncertainReleaseTask(other scope) = %v, want ErrNotFound", err)
	}
	check := state.ReleaseOutcomeCheck{AccountID: fx.Account.ID, CheckedBy: "ops@example.com", Note: "migration applied", CheckedAt: r.base.Add(2*time.Minute + 2*time.Second)}
	check.TaskID = second.ID
	if _, err := outcomes.RecordReleaseOutcomeCheck(fx.Ctx, check); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("check of a queued command = %v, want ErrConflict", err)
	}
	check.TaskID = first.ID
	if recorded, err := outcomes.RecordReleaseOutcomeCheck(fx.Ctx, check); err != nil || recorded.DeploymentID != v1.ID {
		t.Fatalf("RecordReleaseOutcomeCheck = %+v, err=%v", recorded, err)
	}
	if _, err := outcomes.RecordReleaseOutcomeCheck(fx.Ctx, check); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second check = %v, want ErrConflict", err)
	}
	if got, err = r.claim(2*time.Minute+3*time.Second, time.Minute); err != nil || got.ID != second.ID {
		t.Fatalf("claim after the check = %s (err=%v), want %s", got.ID, err, second.ID)
	}
}

func mustClaimToken(t *testing.T, fx *Fixture, taskID string) *string {
	t.Helper()
	task, err := fx.Store.AppTaskByID(fx.Ctx, fx.Account.ID, fx.App.ID, taskID)
	if err != nil || task.LeaseToken == nil {
		t.Fatalf("AppTaskByID(%s) = %+v, err=%v", taskID, task, err)
	}
	return task.LeaseToken
}

// testOutpacedQueuedReleaseCancelled pins that an outpaced release whose
// release command is still queued is superseded with the command cancelled,
// and that one whose command was claimed is left to finish.
func testOutpacedQueuedReleaseCancelled(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.OutpacedReleaseStore)
	if !ok {
		t.Skip("store cannot supersede outpaced releases")
	}
	r := releaseOrderFixture{t: t, fx: fx, base: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	claimed, queued := r.deploy("production"), r.deploy("production")
	claimedTask, queuedTask := r.task(claimed, 0), r.task(queued, time.Second)
	r.deploy("production") // the newer revision
	if got, err := r.claim(2*time.Second, time.Minute); err != nil || got.ID != claimedTask.ID {
		t.Fatalf("claim = %s (err=%v), want %s", got.ID, err, claimedTask.ID)
	}
	if superseded, err := store.SupersedeOutpacedRelease(fx.Ctx, claimed.ID); err != nil || superseded {
		t.Fatalf("SupersedeOutpacedRelease(claimed) = %v, %v; want its command left to finish", superseded, err)
	}
	if superseded, err := store.SupersedeOutpacedRelease(fx.Ctx, queued.ID); err != nil || !superseded {
		t.Fatalf("SupersedeOutpacedRelease(queued) = %v, %v; want superseded", superseded, err)
	}
	row, err := fx.Store.DeploymentByID(fx.Ctx, queued.ID)
	if err != nil || row.Status != state.DeploySuperseded {
		t.Fatalf("outpaced release = %q (err=%v), want superseded", row.Status, err)
	}
	task, err := fx.Store.AppTaskByID(fx.Ctx, fx.Account.ID, fx.App.ID, queuedTask.ID)
	if err != nil || task.Status != state.AppTaskCancelled || task.StartedAt != nil {
		t.Fatalf("outpaced release command = %+v, err=%v; want cancelled before it started", task, err)
	}
}
