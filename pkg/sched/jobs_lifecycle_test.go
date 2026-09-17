// adr: 099

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

type jobLogStream struct {
	lines []LogLine
	next  int
}

func (s *jobLogStream) Recv() (LogLine, error) {
	if s.next >= len(s.lines) {
		return LogLine{}, io.EOF
	}
	line := s.lines[s.next]
	s.next++
	return line, nil
}

type jobLogVMM struct {
	*fakeVMM
	lines []LogLine
}

type delayedJobLogVMM struct {
	*fakeVMM
	calls int
}

func (v *delayedJobLogVMM) Logs(_ context.Context, _ string, _ string, sinceSeq int64, _ time.Time, _ bool) (LogStream, error) {
	v.calls++
	if v.calls == 1 {
		return &jobLogStream{lines: []LogLine{{Seq: 1, Stream: "stderr", Line: "guest-init: stage before-job-supervisor"}}}, nil
	}
	if sinceSeq <= 2 {
		return &jobLogStream{lines: []LogLine{{Seq: 2, Stream: "stdout", Line: "beta-job"}}}, nil
	}
	return &jobLogStream{}, nil
}

type routedDestroyJobVMM struct {
	*fakeVMM
	destroyNode     string
	destroyInstance string
}

func (v *routedDestroyJobVMM) Destroy(_ context.Context, nodeID, instanceID string) error {
	v.destroyNode = nodeID
	v.destroyInstance = instanceID
	v.destroys++
	return v.destroyErr
}

func (v *jobLogVMM) Logs(_ context.Context, nodeID, instanceID string, sinceSeq int64, sinceWrittenAt time.Time, follow bool) (LogStream, error) {
	return &jobLogStream{lines: append([]LogLine(nil), v.lines...)}, nil
}

// instanceBeforeClaimStore mirrors PostgreSQL's immediate
// job_tasks_instance_id_fkey. MemStore intentionally does not enforce SQL
// foreign keys, which previously let WakeJob claim a task before inserting
// its instance and hid a production-only dispatch failure.
type instanceBeforeClaimStore struct {
	state.Store
}

func (s instanceBeforeClaimStore) JobTaskMarkClaimed(ctx context.Context, runID string, taskIndex int, instanceID, leaseToken string, leaseExpiresAt time.Time, nodeID string) error {
	if _, err := s.Store.InstanceByID(ctx, instanceID); err != nil {
		return errors.New("job task claimed before referenced instance exists")
	}
	return s.Store.JobTaskMarkClaimed(ctx, runID, taskIndex, instanceID, leaseToken, leaseExpiresAt, nodeID)
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

func TestHandleJobExitPersistsCombinedTaskOutputBeforeCleanup(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	const (
		instanceID = "job-log-instance"
		leaseToken = "6a274117-5aca-4a61-9672-e9e21b691f8b"
	)
	if err := store.JobTaskMarkClaimed(context.Background(), run.ID, 0, instanceID, leaseToken, time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	vmm := &jobLogVMM{fakeVMM: &fakeVMM{}, lines: []LogLine{
		{Seq: 1, Stream: "stdout", Line: "beta-job"},
		{Seq: 2, Stream: "stderr", Line: "warning"},
	}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.HandleJobExit(context.Background(), acct.ID, run.ID, 0, 0, "succeeded", leaseToken); err != nil {
		t.Fatalf("HandleJobExit: %v", err)
	}
	task, err := store.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if task.Status != "succeeded" || task.LogContent != "beta-job\nwarning\n" || task.LogTruncated {
		t.Fatalf("terminal task = %+v, want persisted complete output", task)
	}
}

func TestHandleJobExitSettlesLateSerialOutput(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	const (
		instanceID = "job-late-log-instance"
		leaseToken = "7a274117-5aca-4a61-9672-e9e21b691f8b"
	)
	if err := store.JobTaskMarkClaimed(context.Background(), run.ID, 0, instanceID, leaseToken, time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	vmm := &delayedJobLogVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.HandleJobExit(context.Background(), acct.ID, run.ID, 0, 0, "succeeded", leaseToken); err != nil {
		t.Fatalf("HandleJobExit: %v", err)
	}
	task, err := store.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if task.LogContent != "guest-init: stage before-job-supervisor\nbeta-job\n" || task.LogTruncated {
		t.Fatalf("terminal task = %+v, want late serial output persisted", task)
	}
	if vmm.calls < 2 {
		t.Fatalf("Logs calls = %d, want replay after initial snapshot", vmm.calls)
	}
}

func TestEngineWakeJobFleetSchedulerChoosesActiveComputeNode(t *testing.T) {
	memStore := state.NewMemStore()
	store := instanceBeforeClaimStore{Store: memStore}
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

func TestJobRetryPolicyUsesRunOverrideWithoutExtraAttempt(t *testing.T) {
	zero, one, three := 0, 1, 3
	tests := []struct {
		name        string
		jobDefault  int
		runOverride *int
		wantRetries int
	}{
		{name: "job default zero", jobDefault: 0, wantRetries: 0},
		{name: "job default one", jobDefault: 1, wantRetries: 1},
		{name: "override zero", jobDefault: 1, runOverride: &zero, wantRetries: 0},
		{name: "override one", jobDefault: 0, runOverride: &one, wantRetries: 1},
		{name: "override three", jobDefault: 1, runOverride: &three, wantRetries: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := state.Job{RetryMax: tt.jobDefault}
			run := state.JobRun{RetryMax: tt.runOverride}
			retryMax := effectiveJobRetryMax(job, run)
			attempts := 1
			for jobTaskHasRetryRemaining(attempts, retryMax) {
				attempts++
			}
			if got := attempts - 1; got != tt.wantRetries {
				t.Fatalf("retry count = %d, want %d (total attempts=%d)", got, tt.wantRetries, attempts)
			}
		})
	}
}

func TestHandleJobExitDestroysOnComputeNodeNotLeaseOwner(t *testing.T) {
	store := state.NewMemStore()
	acct, job, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	ctx := context.Background()
	remote, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "job-compute", TargetURL: "tcp://10.0.0.42:50051", VPCPUs: 2,
		MemMB: 2048, MaxConcurrency: 10, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	const (
		instanceID = "88ff3215-cc04-4a16-81f2-4136067e1350"
		leaseToken = "6a274117-5aca-4a61-9672-e9e21b691f8b"
	)
	if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 0,
		string(state.StateRunning), job.RAMMB, remote.ID, instanceID, leaseToken,
		time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
		t.Fatalf("CreateAndClaimJobInstance: %v", err)
	}
	vmm := &routedDestroyJobVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil)))
	if err := e.HandleJobExit(ctx, acct.ID, run.ID, 0, 0, "succeeded", leaseToken); err != nil {
		t.Fatalf("HandleJobExit: %v", err)
	}
	if vmm.destroyNode != remote.ID || vmm.destroyInstance != instanceID {
		t.Fatalf("destroy routed to node=%q instance=%q, want node=%q instance=%q", vmm.destroyNode, vmm.destroyInstance, remote.ID, instanceID)
	}
}

func TestReconcileOrphanedJobInstancesDestroysAndSettles(t *testing.T) {
	store := state.NewMemStore()
	_, job, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	ctx := context.Background()
	const instanceID = "e34081c8-66a9-47f1-975f-0c167c384ba3"
	if _, err := store.CreateJobInstance(ctx, instanceID, job.ID, run.ID, 0,
		string(state.StateRunning), job.RAMMB, state.DefaultLocalNodeName, instanceID); err != nil {
		t.Fatalf("CreateJobInstance: %v", err)
	}
	vmm := &routedDestroyJobVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	got, err := e.ReconcileOrphanedJobInstances(ctx, 64)
	if err != nil {
		t.Fatalf("ReconcileOrphanedJobInstances: %v", err)
	}
	if got != 1 || vmm.destroyInstance != instanceID {
		t.Fatalf("reconciled=%d destroy=%q, want 1/%q", got, vmm.destroyInstance, instanceID)
	}
	ins, err := store.InstanceByID(ctx, instanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if ins.State != string(state.StateStopped) {
		t.Fatalf("orphan state=%q, want stopped", ins.State)
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
