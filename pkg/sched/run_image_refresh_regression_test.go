// adr: 1161

package sched_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunRegressionImageRefreshPreservesQueuedRun(t *testing.T) {
	store := state.NewMemStore()
	account, run := seedImageRun(t, store, "", "")
	job, err := store.JobGetByID(t.Context(), run.JobID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.JobEnsureImageRun(t.Context(), state.ImageRunEnsure{
		AccountID: account.ID, Name: job.Name, ImageRef: job.ImageRef,
		RAMMB: 256, TaskTimeoutSec: 60, PullAlways: true, MaxCached: 3,
	})
	if !errors.Is(err, state.ErrImageRunBusy) {
		t.Fatalf("refresh error=%v, want retryable busy", err)
	}
	vmm := &imageProcessJobVMM{}
	exits := &recordingJobExitWaiter{specs: make(chan sched.JobExitSpec, 1)}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm).WithJobExitWaiter(exits)
	if _, err := engine.WakeJob(t.Context(), account.ID, run.ID, 0); err != nil {
		t.Fatal(err)
	}
	if vmm.spec.ImageRef != run.ImageStorageKeySnapshot {
		t.Fatalf("boot used %q, want queued image %q", vmm.spec.ImageRef, run.ImageStorageKeySnapshot)
	}
	waitJobTaskSettled(t, store, run.ID)
}
