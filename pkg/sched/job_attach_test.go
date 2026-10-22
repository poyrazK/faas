// adr: 1330

package sched_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// interactiveJobVMM records the boot spec and echoes the interactive
// contract unless it models a vmmd that predates ADR-1330.
type interactiveJobVMM struct {
	ignoresInteractive bool
	spec               sched.JobVmmSpec
	calls              int
}

func (v *interactiveJobVMM) JobColdBoot(_ context.Context, spec sched.JobVmmSpec) (sched.JobVmmResult, error) {
	v.calls++
	v.spec = spec
	return sched.JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, Netns: "fc-job-interactive", HostIP: "10.100.0.27",
		GuestUID: 20027, Interactive: spec.Interactive != nil && !v.ignoresInteractive}, nil
}

// recordingJobExitWaiter reports the exit-watch spec and settles the task.
type recordingJobExitWaiter struct {
	specs chan sched.JobExitSpec
}

func (w *recordingJobExitWaiter) WaitJobExit(_ context.Context, spec sched.JobExitSpec) (sched.JobExitResult, error) {
	w.specs <- spec
	return sched.JobExitResult{ExitCode: 0, ErrorClass: "succeeded", LeaseToken: spec.LeaseToken}, nil
}

// failingJobRunAttachStore fails the interactive-session lookup.
type failingJobRunAttachStore struct {
	MemStore *state.MemStore
}

func (failingJobRunAttachStore) JobRunAttachSession(context.Context, string) (state.JobRunAttach, error) {
	return state.JobRunAttach{}, errors.New("attach sessions unavailable")
}

// seedInteractiveJobRun adds an interactive one-task run to seedJobRun's job.
func seedInteractiveJobRun(t *testing.T, store state.Store) (state.Account, state.Job, state.JobRun, state.JobRunAttach) {
	t.Helper()
	acct, job, _ := sched.SeedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	digest := sha256.Sum256([]byte("interactive-token"))
	attach := state.JobRunAttach{TTY: true, TokenSHA256: digest[:], AttachTimeoutSec: 60}
	one, zero := 1, 0
	run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", &one, &zero, nil, nil, 1,
		state.JobRunOptions{Attach: &attach})
	if err != nil {
		t.Fatalf("interactive JobRunCreate: %v", err)
	}
	return acct, job, run, attach
}

func TestEngineWakeJobPassesInteractiveSessionToVMM(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive bool
	}{
		{name: "interactive run", interactive: true},
		{name: "ordinary run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			var acct state.Account
			var job state.Job
			var run state.JobRun
			var attach state.JobRunAttach
			if tc.interactive {
				acct, job, run, attach = seedInteractiveJobRun(t, store)
			} else {
				acct, job, run = sched.SeedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
			}
			vmm := &interactiveJobVMM{}
			exits := &recordingJobExitWaiter{specs: make(chan sched.JobExitSpec, 1)}
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
				WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm).WithJobExitWaiter(exits)

			if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err != nil {
				t.Fatalf("WakeJob: %v", err)
			}
			wantDeadline := fcvm.EffectiveDestroyWait(job.TaskTimeoutS)
			switch got := vmm.spec.Interactive; {
			case !tc.interactive && got != nil:
				t.Fatalf("ordinary run carried an interactive session: %+v", got)
			case tc.interactive && (got == nil || !got.TTY || got.AttachTimeoutSec != attach.AttachTimeoutSec ||
				!bytes.Equal(got.TokenSHA256, attach.TokenSHA256)):
				t.Fatalf("interactive session = %+v, want %+v", got, attach)
			case tc.interactive:
				wantDeadline += time.Duration(attach.AttachTimeoutSec) * time.Second
			}
			select {
			case spec := <-exits.specs:
				if spec.Deadline != wantDeadline {
					t.Fatalf("exit-watch deadline = %s, want %s", spec.Deadline, wantDeadline)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("exit watch did not start")
			}
			waitJobTaskSettled(t, store, run.ID)
		})
	}
}

func TestEngineWakeJobFailsInteractiveRunOnVMMWithoutSessions(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run, _ := seedInteractiveJobRun(t, store)
	vmm := &interactiveJobVMM{ignoresInteractive: true}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm)

	_, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0)
	if err == nil || !strings.Contains(err.Error(), "interactive mismatch") {
		t.Fatalf("WakeJob error = %v, want interactive mismatch", err)
	}
	if vmm.calls != 1 || vmm.spec.Interactive == nil {
		t.Fatalf("vmmd calls = %d spec = %+v, want one interactive boot", vmm.calls, vmm.spec)
	}
	assertInteractiveAdmissionFailed(t, store, run.ID, vmm.spec.InstanceID, "job_vmm_interactive_unsupported",
		"this node cannot run interactive sessions yet")
}

func TestEngineWakeJobFailsInteractiveRunWhenSessionLookupFails(t *testing.T) {
	base := state.NewMemStore()
	acct, _, run, _ := seedInteractiveJobRun(t, base)
	vmm := &interactiveJobVMM{}
	e := sched.XNewEngine(t, statetest.NewMem(failingJobRunAttachStore{MemStore: base}), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithJobLeaser(sched.AdaptJobLeaser(sched.NewMemLeaser(nil))).WithJobVmmClient(vmm)

	if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err == nil || !strings.Contains(err.Error(), "interactive session") {
		t.Fatalf("WakeJob error = %v, want interactive session failure", err)
	}
	if vmm.calls != 0 {
		t.Fatalf("booted a VM without its interactive session: calls=%d spec=%+v", vmm.calls, vmm.spec)
	}
	task, err := base.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil || task.Status != "failed" || !strings.Contains(derefString(task.ErrorMessage), "interactive session could not be prepared") {
		t.Fatalf("task after failed session lookup = %+v, %v", task, err)
	}
}

// assertInteractiveAdmissionFailed checks a rolled-back interactive admission:
// with no retries the task fails with the customer message, and the instance
// fails with the operator reason.
func assertInteractiveAdmissionFailed(t *testing.T, store *state.MemStore, runID, instanceID, reason, message string) {
	t.Helper()
	ctx := context.Background()
	task, err := store.JobTaskGet(ctx, runID, 0)
	if err != nil || task.Status != "failed" || !strings.Contains(derefString(task.ErrorMessage), message) {
		t.Fatalf("task after rollback = %+v, %v; want failed with %q", task, err, message)
	}
	instance, err := store.InstanceByID(ctx, instanceID)
	if err != nil || instance.State != string(state.StateFailed) {
		t.Fatalf("instance after rollback = %+v, %v", instance, err)
	}
	events, err := store.ListEvents(ctx, instanceID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, event := range events {
		if event.Kind == "wake_boot_error" && strings.Contains(string(event.Data), reason) {
			return
		}
	}
	t.Fatalf("events = %+v, want a wake_boot_error carrying %q", events, reason)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// waitJobTaskSettled lets the exit watch finish before the test returns.
func waitJobTaskSettled(t *testing.T, store state.Store, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task, err := store.JobTaskGet(context.Background(), runID, 0); err == nil && task.Status == "succeeded" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job task did not settle after the exit watch")
}
