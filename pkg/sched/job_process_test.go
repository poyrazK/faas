package sched_test

// adr: 1790 — image runs boot with the image process and -u/-w overrides.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// imageProcessJobVMM records the boot spec and echoes the image-process
// contract unless it models a vmmd that predates ADR-1790.
type imageProcessJobVMM struct {
	ignoresImageProcess bool
	spec                sched.JobVmmSpec
	calls               int
}

func (v *imageProcessJobVMM) JobColdBoot(_ context.Context, spec sched.JobVmmSpec) (sched.JobVmmResult, error) {
	v.calls++
	v.spec = spec
	return sched.JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, Netns: "fc-job-image", HostIP: "10.100.0.28",
		GuestUID: 20028, ImageProcess: spec.ImageProcess && !v.ignoresImageProcess}, nil
}

// seedImageRun creates an image-run runner job with a ready image and one
// run carrying user and workdir.
func seedImageRun(t *testing.T, store *state.MemStore, user, workdir string) (state.Account, state.JobRun) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "image-process@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.JobEnsureImageRun(ctx, state.ImageRunEnsure{AccountID: acct.ID, Name: "ir-0123456789abcdef",
		ImageRef: "docker.io/library/alpine:latest", RAMMB: 256, TaskTimeoutSec: 60, MaxCached: 3})
	if err != nil {
		t.Fatalf("JobEnsureImageRun: %v", err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, res.Job.ID, res.Job.ImageRef, "ready",
		"sha256:"+strings.Repeat("d", 64), "jobs/"+res.Job.ID+".ext4", ""); err != nil {
		t.Fatalf("JobSetImageMaterialization: %v", err)
	}
	one, zero, timeout := 1, 0, 60
	args := []string{"id"}
	run, _, err := store.JobRunCreate(ctx, res.Job.ID, acct.ID, "manual", &one, &zero, &timeout, json.RawMessage(`{}`), 1,
		state.JobRunOptions{CommandArgs: &args, ProcessUser: user, ProcessWorkdir: workdir})
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	return acct, run
}

func TestEngineWakeJobPassesImageProcessToVMM(t *testing.T) {
	for _, tc := range []struct {
		name          string
		user, workdir string
	}{
		{name: "image defaults"},
		{name: "overrides", user: "nobody:nogroup", workdir: "/tmp/run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, run := seedImageRun(t, store, tc.user, tc.workdir)
			vmm := &imageProcessJobVMM{}
			exits := &recordingJobExitWaiter{specs: make(chan sched.JobExitSpec, 1)}
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
				WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm).WithJobExitWaiter(exits)
			if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err != nil {
				t.Fatalf("WakeJob: %v", err)
			}
			if !vmm.spec.ImageProcess || vmm.spec.ProcessUser != tc.user || vmm.spec.ProcessWorkdir != tc.workdir {
				t.Fatalf("boot spec = image %t user %q workdir %q", vmm.spec.ImageProcess, vmm.spec.ProcessUser, vmm.spec.ProcessWorkdir)
			}
			select {
			case <-exits.specs:
			case <-time.After(5 * time.Second):
				t.Fatal("exit watch did not start")
			}
			waitJobTaskSettled(t, store, run.ID)
		})
	}
}

func TestEngineWakeJobKeepsOrdinaryJobsOnTheGuestProcess(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := sched.SeedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	vmm := &imageProcessJobVMM{}
	exits := &recordingJobExitWaiter{specs: make(chan sched.JobExitSpec, 1)}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm).WithJobExitWaiter(exits)
	if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err != nil {
		t.Fatalf("WakeJob: %v", err)
	}
	if vmm.spec.ImageProcess || vmm.spec.ProcessUser != "" || vmm.spec.ProcessWorkdir != "" {
		t.Fatalf("ordinary job boot spec = %+v", vmm.spec)
	}
	select {
	case <-exits.specs:
	case <-time.After(5 * time.Second):
		t.Fatal("exit watch did not start")
	}
	waitJobTaskSettled(t, store, run.ID)
}

func TestEngineWakeJobFailsImageRunOnVMMWithoutImageProcess(t *testing.T) {
	store := state.NewMemStore()
	acct, run := seedImageRun(t, store, "", "")
	vmm := &imageProcessJobVMM{ignoresImageProcess: true}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm)
	_, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0)
	if err == nil || !strings.Contains(err.Error(), "image-process mismatch") {
		t.Fatalf("WakeJob error = %v, want image-process mismatch", err)
	}
	assertInteractiveAdmissionFailed(t, store, run.ID, vmm.spec.InstanceID, "job_vmm_image_process_unsupported",
		"cannot run image runs with the image's user and environment yet")
}
