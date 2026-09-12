// adr: 099

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordingJobVMM struct {
	spec  JobVmmSpec
	err   error
	calls int
}

type blockingJobExitWaiter struct {
	started chan struct{}
	release chan struct{}
}

func (w *blockingJobExitWaiter) WaitJobExit(_ context.Context, spec JobExitSpec) (JobExitResult, error) {
	close(w.started)
	<-w.release
	return JobExitResult{ExitCode: 0, ErrorClass: "succeeded", LeaseToken: spec.LeaseToken}, nil
}

func (v *recordingJobVMM) JobColdBoot(_ context.Context, spec JobVmmSpec) (JobVmmResult, error) {
	v.calls++
	v.spec = spec
	if v.err != nil {
		return JobVmmResult{}, v.err
	}
	return JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID}, nil
}

func seedJobRun(t *testing.T, store state.Store, jobEnv, runEnv json.RawMessage) (state.Account, state.Job, state.JobRun) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "job-lifecycle@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "lifecycle", "function", "registry.example/fn@sha256:abc", []string{"/app/handler"}, 256, 30, 2, 1, jobEnv)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, runEnv, 1)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	return acct, job, run
}

func TestEngineWakeJobCallsVMMWithCompleteSpec(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{"JOB":"job-value","SHARED":"job"}`), json.RawMessage(`{"RUN":"run-value","SHARED":"run"}`))
	vmm := &recordingJobVMM{}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)

	result, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0)
	if err != nil {
		t.Fatalf("WakeJob: %v", err)
	}
	if vmm.calls != 1 {
		t.Fatalf("JobColdBoot calls = %d, want 1", vmm.calls)
	}
	if vmm.spec.InstanceID != result.InstanceID || vmm.spec.RunID != run.ID || vmm.spec.AccountID != acct.ID {
		t.Fatalf("VMM spec identity = %+v, result=%+v", vmm.spec, result)
	}
	if vmm.spec.ImageRef != "registry.example/fn@sha256:abc" || vmm.spec.RAMMB != 256 || vmm.spec.TaskTimeoutSec != 30 {
		t.Fatalf("VMM spec execution fields = %+v", vmm.spec)
	}
	if vmm.spec.Env["JOB"] != "job-value" || vmm.spec.Env["RUN"] != "run-value" || vmm.spec.Env["SHARED"] != "run" {
		t.Fatalf("VMM env = %#v, want merged job+run with run precedence", vmm.spec.Env)
	}
	if result.Method != "cold_boot" || result.NodeID != vmm.spec.NodeID {
		t.Fatalf("result = %+v, want cold_boot and returned node", result)
	}
}

func TestEngineWakeJobFleetSchedulerChoosesActiveComputeNode(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	defaultLocal, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(default-local): %v", err)
	}
	if err := store.SetComputeNodeActive(ctx, defaultLocal.ID, false); err != nil {
		t.Fatalf("SetComputeNodeActive(default-local): %v", err)
	}
	remote, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "fleet-compute-1",
		TargetURL:          "tcp://10.0.0.42:50051",
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 4096,
		VCPUBudget:         4,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode(remote): %v", err)
	}
	vmm := &recordingJobVMM{}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)

	result, err := e.WakeJob(ctx, acct.ID, run.ID, 0)
	if err != nil {
		t.Fatalf("WakeJob: %v", err)
	}
	if result.NodeID != remote.ID || vmm.spec.NodeID != remote.ID {
		t.Fatalf("job routed to result=%q spec=%q, want active fleet node %q", result.NodeID, vmm.spec.NodeID, remote.ID)
	}
	if got := e.Ledger().ResidentRAMForNode(remote.ID); got == 0 {
		t.Fatal("remote node has no job RAM reservation")
	}
}

func TestEngineWakeJobFailureRequeuesAndReleases(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	lease := NewMemLeaser(nil)
	vmm := &recordingJobVMM{err: errors.New("vmmd unavailable")}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(lease)).WithJobVmmClient(vmm)

	if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err == nil {
		t.Fatal("WakeJob returned nil, want vmmd error")
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Fatalf("resident RAM = %d, want 0 after failed job boot", got)
	}
	if got := lease.Size(); got != 0 {
		t.Fatalf("active leases = %d, want 0 after failed job boot", got)
	}
	task, err := store.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if task.Status != "queued" || task.InstanceID != nil || task.LeaseToken != nil {
		t.Fatalf("task after rollback = %+v, want queued without execution lease", task)
	}
	instances, err := store.ListJobInstances(context.Background())
	if err != nil {
		t.Fatalf("ListJobInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("live job instances = %+v, want none after failed boot", instances)
	}
	failed, err := store.InstanceByID(context.Background(), vmm.spec.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID after rollback: %v", err)
	}
	if failed.State != string(state.StateFailed) {
		t.Fatalf("failed job instance = %+v, want terminal failed state", failed)
	}
}

func TestEngineWakeJobSupervisesExitAndCleansUp(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	lease := NewMemLeaser(nil)
	waiter := &blockingJobExitWaiter{started: make(chan struct{}), release: make(chan struct{})}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(lease)).
		WithJobVmmClient(&recordingJobVMM{}).
		WithJobExitWaiter(waiter)

	result, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0)
	if err != nil {
		t.Fatalf("WakeJob: %v", err)
	}
	ins, err := store.InstanceByID(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if ins.State != string(state.StateRunning) {
		t.Fatalf("job instance state = %q, want running", ins.State)
	}
	select {
	case <-waiter.started:
	case <-time.After(time.Second):
		t.Fatal("job exit waiter did not start")
	}
	close(waiter.release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ins, err = store.InstanceByID(context.Background(), result.InstanceID)
		if err == nil && ins.State == string(state.StateStopped) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ins.State != string(state.StateStopped) {
		t.Fatalf("job instance after exit = %q, want stopped", ins.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Fatalf("resident RAM after exit = %d, want 0", got)
	}
	if got := lease.Size(); got != 0 {
		t.Fatalf("active leases after exit = %d, want 0", got)
	}
	task, err := store.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if task.Status != "succeeded" {
		t.Fatalf("task after exit = %q, want succeeded", task.Status)
	}
}
