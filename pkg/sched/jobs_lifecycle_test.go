// adr: 099

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type recordingJobVMM struct {
	spec  JobVmmSpec
	err   error
	calls int
}

type workloadIntentByJobTestStore struct {
	state.Store
	intent state.EnvironmentWorkloadIntent
}

func (s workloadIntentByJobTestStore) EnvironmentWorkloadIntentByJob(_ context.Context, accountID, jobID string) (state.EnvironmentWorkloadIntent, error) {
	if s.intent.AccountID != accountID || s.intent.JobID != jobID {
		return state.EnvironmentWorkloadIntent{}, state.ErrNotFound
	}
	return s.intent, nil
}

func (s workloadIntentByJobTestStore) ProjectEnvironmentByID(ctx context.Context, id string) (state.ProjectEnvironment, error) {
	reader, ok := s.Store.(interface {
		ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
	})
	if !ok {
		return state.ProjectEnvironment{}, state.ErrNotFound
	}
	return reader.ProjectEnvironmentByID(ctx, id)
}

func (s workloadIntentByJobTestStore) AppEnvironmentSecretReferences(ctx context.Context, accountID, appID, scope string) (map[string]string, error) {
	reader, ok := s.Store.(state.AppEnvironmentSecretReferenceReader)
	if !ok {
		return nil, state.ErrNotFound
	}
	return reader.AppEnvironmentSecretReferences(ctx, accountID, appID, scope)
}

func (s workloadIntentByJobTestStore) RuntimeConfigInputsFresh(ctx context.Context, appID string, inputs state.RuntimeConfigInputs) (bool, error) {
	reader, ok := s.Store.(interface {
		RuntimeConfigInputsFresh(context.Context, string, state.RuntimeConfigInputs) (bool, error)
	})
	if !ok {
		return false, state.ErrNotFound
	}
	return reader.RuntimeConfigInputsFresh(ctx, appID, inputs)
}

func (s workloadIntentByJobTestStore) AppRuntimeConfigChangedAtInScope(ctx context.Context, appID, scope string) (time.Time, bool, error) {
	reader, ok := s.Store.(state.EnvironmentRuntimeFreshnessStore)
	if !ok {
		return time.Time{}, false, state.ErrNotFound
	}
	return reader.AppRuntimeConfigChangedAtInScope(ctx, appID, scope)
}

type heldStartRecordingJobVMM struct {
	store    state.Store
	spec     JobVmmSpec
	released bool
}

func (v *heldStartRecordingJobVMM) JobColdBoot(_ context.Context, spec JobVmmSpec) (JobVmmResult, error) {
	v.spec = spec
	return JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, Netns: "fc-job-held", HostIP: "10.100.0.26",
		GuestUID: 20026, StartHeld: spec.StartHeld}, nil
}

func (v *heldStartRecordingJobVMM) ReleaseJobStart(ctx context.Context, spec JobStartSpec) error {
	instance, err := v.store.InstanceByID(ctx, spec.InstanceID)
	if err != nil || !v.spec.StartHeld || spec.InstanceID != v.spec.InstanceID || spec.NodeID != v.spec.NodeID ||
		instance.State != string(state.StateRunning) || instance.Netns == "" || instance.HostIP == "" || instance.GuestUID == 0 {
		return errors.Join(err, errors.New("start gate released before runtime identity was published"))
	}
	v.released = true
	return nil
}

type missingRuntimeIdentityJobVMM struct{ instanceID string }

func (v *missingRuntimeIdentityJobVMM) JobColdBoot(_ context.Context, spec JobVmmSpec) (JobVmmResult, error) {
	v.instanceID = spec.InstanceID
	return JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID}, nil
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

func (s instanceBeforeClaimStore) JobListPendingImageMaterialization(ctx context.Context, limit int) ([]state.Job, error) {
	return s.Store.(state.JobImageMaterializationStore).JobListPendingImageMaterialization(ctx, limit)
}

func (s instanceBeforeClaimStore) JobSetImageMaterialization(ctx context.Context, id, sourceRef, status, resolvedDigest, storageKey, failure string) (state.Job, error) {
	return s.Store.(state.JobImageMaterializationStore).JobSetImageMaterialization(ctx, id, sourceRef, status, resolvedDigest, storageKey, failure)
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
	return JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, Netns: "fc-job-test", HostIP: "10.100.0.25", GuestUID: 20025}, nil
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
	if _, ok := store.(state.JobImageMaterializationStore); ok {
		if _, err := store.(state.JobImageMaterializationStore).JobSetImageMaterialization(ctx, job.ID, job.ImageRef,
			"ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
			t.Fatalf("JobSetImageMaterialization: %v", err)
		}
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, runEnv, 1)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	return acct, job, run
}

func seedGitOpsBoundScheduledJobRun(t *testing.T, store *state.MemStore) (state.Account, state.JobRun, state.EnvironmentWorkloadIntent) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "gitops-bound-job@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	image := "registry.example/daily-report@" + digest
	job, err := store.JobCreate(ctx, account.ID, "daily-report", "batch", image, nil, 256, 60, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	status, cron, timezone := "active", "15 * * * *", "UTC"
	job, err = store.JobUpdateWithSchedule(ctx, job.ID, nil, nil, nil, nil, nil, nil,
		json.RawMessage(`{}`), &status, &cron, &timezone)
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.JobSetImageMaterialization(ctx, job.ID, image, "ready", digest, "jobs/daily-report.ext4", "")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, account.ID, "scheduled", nil, nil, nil, json.RawMessage(`{}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "gitops-bound-job"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "daily-report-config", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	intent := state.EnvironmentWorkloadIntent{
		AccountID: account.ID, AppID: app.ID, EnvironmentID: environment.ID, JobID: job.ID,
		Source:   &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
		Schedule: &api.EnvironmentJobSchedule{Cron: cron, Timezone: timezone},
		ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{
			"database": {Workload: "database", EnvKey: "DATABASE_URL", TargetAppID: uuid.NewString()},
		},
	}
	return account, run, intent
}

func TestEngineWakeJobCallsVMMWithCompleteSpec(t *testing.T) {
	store := state.NewMemStore()
	acct, job, run := seedJobRun(t, store, json.RawMessage(`{"JOB":"job-value","SHARED":"job"}`), json.RawMessage(`{"RUN":"run-value","SHARED":"run","GREGALE_TASK_INDEX":"spoofed"}`))
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
	if vmm.spec.ImageRef != "jobs/"+job.ID+".ext4" || vmm.spec.RAMMB != 256 || vmm.spec.TaskTimeoutSec != 30 {
		t.Fatalf("VMM spec execution fields = %+v", vmm.spec)
	}
	if vmm.spec.Env["JOB"] != "job-value" || vmm.spec.Env["RUN"] != "run-value" || vmm.spec.Env["SHARED"] != "run" {
		t.Fatalf("VMM env = %#v, want merged job+run with run precedence", vmm.spec.Env)
	}
	if vmm.spec.Env["GREGALE_RUN_ID"] != run.ID || vmm.spec.Env["GREGALE_TASK_INDEX"] != "0" || vmm.spec.Env["GREGALE_TASK_ATTEMPT"] != "1" || vmm.spec.Env["GREGALE_TASK_COUNT"] != "1" {
		t.Fatalf("VMM env identity = %#v, want trusted run/task identity", vmm.spec.Env)
	}
	if result.Method != "cold_boot" || result.NodeID != vmm.spec.NodeID {
		t.Fatalf("result = %+v, want cold_boot and returned node", result)
	}
	instance, err := store.InstanceByID(context.Background(), result.InstanceID)
	if err != nil || instance.State != string(state.StateRunning) || instance.Netns != "fc-job-test" ||
		instance.HostIP != "10.100.0.25" || instance.GuestUID != 20025 {
		t.Fatalf("published job runtime identity = %+v, %v", instance, err)
	}
}

func TestEngineWakeJobRequiresHeldStartReleaseBeforeBoot(t *testing.T) {
	base := state.NewMemStore()
	account, run, intent := seedGitOpsBoundScheduledJobRun(t, base)
	intentStore := workloadIntentByJobTestStore{Store: base, intent: intent}
	vmm := &recordingJobVMM{}
	e := newEngine(t, intentStore, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	if _, err := e.WakeJob(context.Background(), account.ID, run.ID, 0); err == nil || !strings.Contains(err.Error(), "start-gate release capability") {
		t.Fatalf("WakeJob error = %v, want missing start-gate capability", err)
	}
	if vmm.calls != 0 {
		t.Fatalf("cold-booted a service-bound Job without a gate-release RPC: calls=%d spec=%+v", vmm.calls, vmm.spec)
	}
}

func TestEngineWakeJobPublishesRuntimeIdentityBeforeReleasingServiceBindings(t *testing.T) {
	base := state.NewMemStore()
	account, run, intent := seedGitOpsBoundScheduledJobRun(t, base)
	intentStore := workloadIntentByJobTestStore{Store: base, intent: intent}
	vmm := &heldStartRecordingJobVMM{store: base}
	e := newEngine(t, intentStore, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	result, err := e.WakeJob(context.Background(), account.ID, run.ID, 0)
	if err != nil {
		t.Fatalf("WakeJob: %v", err)
	}
	wantURL := "http://database.svc.gregale:" + strconv.Itoa(api.ServiceBindingPort)
	if !vmm.spec.StartHeld || vmm.spec.Env["DATABASE_URL"] != wantURL || !vmm.released || result.InstanceID != vmm.spec.InstanceID {
		t.Fatalf("service-bound Job did not publish identity and release its exact held start: spec=%+v released=%t result=%+v",
			vmm.spec, vmm.released, result)
	}
}

func TestEngineWakeJobDeliversOnlyReviewedScopedSecrets(t *testing.T) {
	base := state.NewMemStore()
	account, run, intent := seedGitOpsBoundScheduledJobRun(t, base)
	if err := base.UpsertAppSecretInScope(context.Background(), account.ID, intent.AppID, "staging", "TOKEN_SOURCE", []byte("sealed-token")); err != nil {
		t.Fatal(err)
	}
	if err := base.UpsertAppSecretInScope(context.Background(), account.ID, intent.AppID, "staging", "UNRELATED", []byte("sealed-unrelated")); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{store: base, log: testLog()}
	empty, err := engine.resolveSealedEnvDeliveryForRoleWithEmptyAll(context.Background(), account.ID,
		intent.AppID, "staging", nil, false, false, false)
	if err != nil || empty.AllSecrets || len(empty.Entries) != 0 {
		t.Fatalf("explicit empty Job secret intent expanded to scoped secrets: delivery=%+v err=%v", empty, err)
	}
	if err := base.PutAppEnvironmentSecretReference(context.Background(), account.ID, intent.AppID, "staging", "API_TOKEN", "secret:TOKEN_SOURCE"); err != nil {
		t.Fatal(err)
	}
	intentStore := workloadIntentByJobTestStore{Store: base, intent: intent}
	vmm := &heldStartRecordingJobVMM{store: base}
	e := newEngine(t, intentStore, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	if _, err := e.WakeJob(context.Background(), account.ID, run.ID, 0); err != nil {
		t.Fatalf("WakeJob: %v", err)
	}
	if len(vmm.spec.SealedEnvEntries) != 1 || vmm.spec.SealedEnvEntries[0].Key != "API_TOKEN" ||
		vmm.spec.SealedEnvEntries[0].SourceKey != "TOKEN_SOURCE" || string(vmm.spec.SealedEnvEntries[0].Ciphertext) != "sealed-token" {
		t.Fatalf("Job received unexpected sealed environment: %+v", vmm.spec.SealedEnvEntries)
	}
	secrets, err := base.ListAppSecretsInScope(context.Background(), account.ID, intent.AppID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if secret.Key == "TOKEN_SOURCE" && secret.DeliveryStatus != state.SecretDeliveryDelivered {
			t.Fatalf("delivered secret status = %s, want delivered", secret.DeliveryStatus)
		}
		if secret.Key == "UNRELATED" && secret.DeliveryStatus == state.SecretDeliveryDelivered {
			t.Fatal("unreferenced secret was marked delivered")
		}
	}
}

func TestEngineWakeJobRejectsMissingRuntimeIdentity(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	vmm := &missingRuntimeIdentityJobVMM{}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err == nil {
		t.Fatal("WakeJob accepted a successful boot without runtime identity")
	}
	if vmm.instanceID == "" {
		t.Fatal("JobColdBoot was not called")
	}
	instance, err := store.InstanceByID(context.Background(), vmm.instanceID)
	if err != nil || instance.State != string(state.StateFailed) || instance.Netns != "" || instance.HostIP != "" || instance.GuestUID != 0 {
		t.Fatalf("failed job instance after identity rejection = %+v, %v", instance, err)
	}
}

func TestEngineWakeJobUsesRunCommandAndPolicySnapshot(t *testing.T) {
	store := state.NewMemStore()
	acct, job, _ := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	args := []string{"--dataset", "one"}
	run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual",
		nil, nil, nil, nil, 1, state.JobRunOptions{
			CommandArgs: &args,
			Inputs:      []state.JobInput{{ID: "partition-a", Ref: "s3://customer-data/partition-a"}},
		})
	if err != nil {
		t.Fatal(err)
	}
	if run.RetryMax == nil || *run.RetryMax != job.RetryMax || run.TaskTimeoutS == nil || *run.TaskTimeoutS != job.TaskTimeoutS {
		t.Fatalf("run policy = retry %v timeout %v, want job defaults", run.RetryMax, run.TaskTimeoutS)
	}
	vmm := &recordingJobVMM{}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	if _, err := e.WakeJob(context.Background(), acct.ID, run.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := vmm.spec.Command; len(got) != 3 || got[0] != "/app/handler" || got[1] != "--dataset" || got[2] != "one" {
		t.Fatalf("VMM command = %v, want snapshot with run arguments", got)
	}
	if vmm.spec.Env["GREGALE_INPUT_ID"] != "partition-a" || vmm.spec.Env["GREGALE_INPUT_REF"] != "s3://customer-data/partition-a" {
		t.Fatalf("VMM input env = %v", vmm.spec.Env)
	}
}

func TestDispatchJobsTickExpiresUnstartedFlexibleTasks(t *testing.T) {
	store := state.NewMemStore()
	acct, job, standard := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	if _, err := store.JobRunCancel(context.Background(), standard.ID); err != nil {
		t.Fatal(err)
	}
	eligible := time.Now().UTC().Add(-2 * time.Hour)
	latest := eligible.Add(time.Hour)
	run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, nil, nil, nil, 2,
		state.JobRunOptions{ExecutionClass: "flexible", EligibleAt: &eligible, LatestStartAt: &latest})
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.DispatchJobsTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	settled, err := store.JobRunGetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.AggregateStatus != "cancelled" || settled.TasksCancelled != 2 {
		t.Fatalf("expired run = %+v", settled)
	}
	for index := 0; index < 2; index++ {
		task, err := store.JobTaskGet(context.Background(), run.ID, index)
		if err != nil || task.Status != "cancelled" || task.ErrorMessage == nil || *task.ErrorMessage != "flexible start window expired before admission" {
			t.Fatalf("expired task %d = %+v, err %v", index, task, err)
		}
	}
}

func TestFlexibleTasksWaitForEligibilityAndFollowStandardTasks(t *testing.T) {
	store := state.NewMemStore()
	acct, job, standard := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	eligible := time.Now().UTC().Add(time.Hour)
	latest := eligible.Add(time.Hour)
	flexible, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, nil, nil, nil, 1,
		state.JobRunOptions{ExecutionClass: "flexible", EligibleAt: &eligible, LatestStartAt: &latest})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.JobTaskClaimBatch(context.Background(), 10)
	if err != nil || len(claimed) != 1 || claimed[0].RunID != standard.ID {
		t.Fatalf("future flexible claim candidates = %+v, err %v", claimed, err)
	}
	eligibleNow := time.Now().UTC().Add(-time.Minute)
	latestNow := time.Now().UTC().Add(time.Hour)
	ready, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, nil, nil, nil, 1,
		state.JobRunOptions{ExecutionClass: "flexible", EligibleAt: &eligibleNow, LatestStartAt: &latestNow})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = store.JobTaskClaimBatch(context.Background(), 10)
	if err != nil || len(claimed) != 2 || claimed[0].RunID != standard.ID || claimed[1].RunID != ready.ID || flexible.ID == ready.ID {
		t.Fatalf("eligible claim order = %+v, err %v", claimed, err)
	}
}

func TestFailFastCancelsOnlyUnstartedTasksAfterRetryExhaustion(t *testing.T) {
	store := state.NewMemStore()
	acct, job, _ := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	zero := 0
	run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, &zero, nil, nil, 3,
		state.JobRunOptions{FailurePolicy: "fail_fast"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.JobTaskMarkTerminal(context.Background(), run.ID, 0, "failed", 1, "user_error", "bad input", time.Now()); err != nil {
		t.Fatal(err)
	}
	settled, err := store.JobRunRecompute(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.AggregateStatus != "failed" || settled.TasksFailed != 1 || settled.TasksCancelled != 2 {
		t.Fatalf("fail-fast run = %+v", settled)
	}
	for _, index := range []int{1, 2} {
		task, err := store.JobTaskGet(context.Background(), run.ID, index)
		if err != nil || task.Status != "cancelled" || task.ErrorMessage == nil || *task.ErrorMessage != "fail_fast after permanent task failure" {
			t.Fatalf("unstarted task %d = %+v, err %v", index, task, err)
		}
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
	output := json.RawMessage(`{"version":1,"artifacts":[{"name":"result","uri":"s3://outputs/result.json","size_bytes":2,"sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	if err := e.HandleJobExit(context.Background(), acct.ID, run.ID, 0, 0, "succeeded", leaseToken, output); err != nil {
		t.Fatalf("HandleJobExit: %v", err)
	}
	task, err := store.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if task.Status != "succeeded" || task.LogContent != "beta-job\nwarning\n" || task.LogTruncated || string(task.OutputManifest) != string(output) {
		t.Fatalf("terminal task = %+v, want persisted complete output", task)
	}
}

// adr: 385 — classified outcomes settle or retry only the matching partition.
func TestHandleJobExitClassifiesStructuredOutcomeAndRetainsAttempt(t *testing.T) {
	for _, tc := range []struct {
		name, outcomeCode, action, wantStatus string
	}{
		{name: "permanent record rejection", outcomeCode: "invalid_record", action: "fail_partition", wantStatus: "failed"},
		{name: "retryable upstream error", outcomeCode: "upstream_unavailable", action: "retry", wantStatus: "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, job, _ := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
			retryMax := 1
			rules := &workpolicy.FailureRules{
				Version:          workpolicy.Version,
				Rules:            []workpolicy.FailureRule{{OutcomeCodes: []string{tc.outcomeCode}, Action: tc.action}},
				UnmatchedFailure: "retry", UncertainOutcome: "hold",
			}
			run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, &retryMax, nil, nil, 1,
				state.JobRunOptions{FailureRules: rules})
			if err != nil {
				t.Fatalf("JobRunCreate: %v", err)
			}
			const instanceID = "job-outcome-instance"
			const leaseToken = "job-outcome-lease"
			if err := store.JobTaskMarkClaimed(context.Background(), run.ID, 0, instanceID, leaseToken, time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
				t.Fatalf("JobTaskMarkClaimed: %v", err)
			}
			e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			manifest := json.RawMessage(`{"version":1,"artifacts":[],"outcome_code":"` + tc.outcomeCode + `"}`)
			if err := e.HandleJobExit(context.Background(), acct.ID, run.ID, 0, 0, "succeeded", leaseToken, manifest); err != nil {
				t.Fatalf("HandleJobExit: %v", err)
			}
			task, err := store.JobTaskGet(context.Background(), run.ID, 0)
			if err != nil || task.Status != tc.wantStatus {
				t.Fatalf("task = %+v, err %v; want status %s", task, err, tc.wantStatus)
			}
			attempts, err := store.JobTaskAttemptList(context.Background(), run.ID, 0, 10, 0)
			if err != nil || len(attempts) != 1 || attempts[0].OutcomeCode != tc.outcomeCode || attempts[0].WorkDecision == nil || attempts[0].WorkDecision.Action != tc.action {
				t.Fatalf("attempts = %+v, err %v; want the confirmed structured result retained", attempts, err)
			}
		})
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

func TestEngineWakeJobBootFailureSchedulesBoundedRetryAndReleases(t *testing.T) {
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
	if task.Attempt != 2 || task.NextAttemptAt == nil || !task.NextAttemptAt.After(time.Now()) || task.ErrorClass == nil || *task.ErrorClass != "infra" || task.ErrorMessage == nil {
		t.Fatalf("boot failure did not consume a retry with backoff/error: %+v", task)
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

func TestDispatchJobsTickKeepsBootFailureBackoffAndTerminatesAfterBudget(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	vmm := &recordingJobVMM{err: errors.New("vmmd unavailable")}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	ctx := context.Background()
	if err := e.DispatchJobsTick(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || first.Attempt != 2 || first.Status != "queued" || first.NextAttemptAt == nil {
		t.Fatalf("first dispatch task=%+v err=%v", first, err)
	}
	if err := e.DispatchJobsTick(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ := store.JobTaskGet(ctx, run.ID, 0)
	if vmm.calls != 1 || !second.NextAttemptAt.Equal(*first.NextAttemptAt) {
		t.Fatalf("backoff bypassed: calls=%d first=%+v second=%+v", vmm.calls, first, second)
	}
	if _, err := e.WakeJob(ctx, acct.ID, run.ID, 0); !errors.Is(err, ErrJobRetryBackoff) {
		t.Fatalf("direct WakeJob during backoff err=%v", err)
	}
	if err := store.JobTaskRequeue(ctx, run.ID, 0, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := e.DispatchJobsTick(ctx); err != nil {
		t.Fatal(err)
	}
	terminal, err := store.JobTaskGet(ctx, run.ID, 0)
	if vmm.spec.Env["GREGALE_TASK_ATTEMPT"] != "2" {
		t.Fatalf("retried VMM attempt identity = %q, want 2", vmm.spec.Env["GREGALE_TASK_ATTEMPT"])
	}
	if err != nil || terminal.Status != "failed" || terminal.Attempt != 2 || terminal.ErrorMessage == nil || terminal.FinishedAt == nil || vmm.calls != 2 {
		t.Fatalf("exhausted task=%+v calls=%d err=%v", terminal, vmm.calls, err)
	}
	result, err := store.JobRunGetByID(ctx, run.ID)
	if err != nil || result.AggregateStatus != "dead_letter" {
		t.Fatalf("exhausted run=%+v err=%v", result, err)
	}
	if err := e.DispatchJobsTick(ctx); err != nil {
		t.Fatal(err)
	}
	if vmm.calls != 2 {
		t.Fatalf("terminal task booted again: calls=%d", vmm.calls)
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

func TestLateJobExitAfterBootFailureCannotSettleQueuedRetry(t *testing.T) {
	store := state.NewMemStore()
	acct, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	waiter := &blockingJobExitWaiter{started: make(chan struct{}), release: make(chan struct{})}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).
		WithJobVmmClient(&recordingJobVMM{}).
		WithJobExitWaiter(waiter)
	ctx := context.Background()
	result, err := e.WakeJob(ctx, acct.ID, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-waiter.started:
	case <-time.After(time.Second):
		t.Fatal("job exit waiter did not start")
	}
	retried, err := store.JobTaskFailBoot(ctx, run.ID, 0, result.InstanceID, string(result.LeaseToken), 1, time.Now().Add(time.Minute), "uncertain boot")
	if err != nil || !retried {
		t.Fatalf("record uncertain boot: retried=%v err=%v", retried, err)
	}
	if err := e.jobLeaser.Release(ctx, result.LeaseToken, e.ownerNodeID); err != nil {
		t.Fatal(err)
	}
	close(waiter.release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ins, err := store.InstanceByID(ctx, result.InstanceID)
		if err == nil && ins.State == string(state.StateStopped) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	task, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || task.Status != "queued" || task.Attempt != 2 || task.ErrorMessage == nil || *task.ErrorMessage != "uncertain boot" {
		t.Fatalf("late exit overwrote retry task=%+v err=%v", task, err)
	}
	ins, err := store.InstanceByID(ctx, result.InstanceID)
	if err != nil || ins.State != string(state.StateStopped) {
		t.Fatalf("stale VM not cleaned up: instance=%+v err=%v", ins, err)
	}
}

// TestHandleJobExitRecordsDeadLetterWhenRetryBudgetIsSpent — on
// production-us a task whose retries were exhausted still showed
// retryable/retry on its final attempt, so `jobs attempts` promised a retry
// that never came. The final attempt now records dead_letter /
// retry_budget_exhausted, and the run's dead-letter count still rises.
func TestHandleJobExitRecordsDeadLetterWhenRetryBudgetIsSpent(t *testing.T) {
	store := state.NewMemStore()
	acct, job, _ := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	retryMax := 0
	rules := &workpolicy.FailureRules{
		Version:          workpolicy.Version,
		Rules:            []workpolicy.FailureRule{{OutcomeCodes: []string{"upstream_unavailable"}, Action: "retry"}},
		UnmatchedFailure: "retry", UncertainOutcome: "hold",
	}
	run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, &retryMax, nil, nil, 1,
		state.JobRunOptions{FailureRules: rules})
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	const instanceID, leaseToken = "job-dlq-instance", "job-dlq-lease"
	if err := store.JobTaskMarkClaimed(context.Background(), run.ID, 0, instanceID, leaseToken, time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	manifest := json.RawMessage(`{"version":1,"artifacts":[],"outcome_code":"upstream_unavailable"}`)
	if err := e.HandleJobExit(context.Background(), acct.ID, run.ID, 0, 0, "succeeded", leaseToken, manifest); err != nil {
		t.Fatalf("HandleJobExit: %v", err)
	}
	task, err := store.JobTaskGet(context.Background(), run.ID, 0)
	if err != nil || task.Status != "failed" {
		t.Fatalf("task = %+v, err %v; want failed (no retry left)", task, err)
	}
	attempts, err := store.JobTaskAttemptList(context.Background(), run.ID, 0, 10, 0)
	if err != nil || len(attempts) != 1 || attempts[0].WorkDecision == nil ||
		attempts[0].WorkDecision.Action != "dead_letter" || attempts[0].WorkDecision.Reason != "retry_budget_exhausted" ||
		attempts[0].WorkDecision.Classification == "" {
		t.Fatalf("final attempt decision = %+v, err %v; want <classification>/dead_letter retry_budget_exhausted", attempts, err)
	}
	got, err := store.JobRunGetByID(context.Background(), run.ID)
	if err != nil || got.DeadLetterCount != 1 {
		t.Fatalf("run dead letters = %d, err %v; want 1", got.DeadLetterCount, err)
	}
}
