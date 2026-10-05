//go:build metal

// adr: 594
package e2e_test

import (
	"context"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// This drill stops vmmd on dedicated native workers. Ordinary metal runs skip
// it; an explicit regional gate requires every subtest to execute and pass.
func TestNativeRegionalJobsAcceptance(t *testing.T) {
	if os.Getenv("FAAS_REGION_ACCEPTANCE_CONFIRM") != "native-x86" {
		t.Skip("requires a dedicated native regional fleet; see ADR-594")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("regional acceptance controller must run Linux x86_64")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
	defer cancel()
	f := newRegionalJobAcceptance(t, ctx)
	t.Run("worker_loss_retries_on_same_region_peer", f.workerLoss)
	if t.Failed() {
		return
	}
	t.Run("whole_region_outage_queues_without_remote_boot_or_retry", f.regionOutage)
}

type regionalJobAcceptance struct {
	ctx        context.Context
	store      *state.PgStore
	client     *api.Client
	job        api.JobResponse
	nodes      []state.ComputeNode
	commands   []e2etest.NativeHostCommand
	stopped    map[int]bool
	runs       []api.JobRunResponse
	region     string
	metricsURL string
	repoRoot   string
	remoteBoot regionalJobBootObservation
	peer       int
}

type regionalJobBootObservation struct {
	invocation string
	count      float64
}

func newRegionalJobAcceptance(t *testing.T, ctx context.Context) *regionalJobAcceptance {
	t.Helper()
	required := func(key string) string {
		t.Helper()
		value := os.Getenv(key)
		if value == "" {
			t.Fatalf("%s is required", key)
		}
		return value
	}
	image := required("FAAS_REGION_JOB_IMAGE")
	if !regexp.MustCompile(`@sha256:[a-f0-9]{64}$`).MatchString(image) {
		t.Fatal("FAAS_REGION_JOB_IMAGE must be digest-pinned and provide /bin/sh and sleep")
	}
	metricsURL := required("FAAS_REGION_VMMD_METRICS_URL_B")
	if !regexp.MustCompile(`^http://(127\.0\.0\.1|localhost|\[::1\]):[0-9]+/metrics$`).MatchString(metricsURL) {
		t.Fatal("remote vmmd metrics must use an explicit loopback HTTP /metrics URL")
	}
	repoRoot := required("FAAS_REGION_WORKER_REPO")
	if !regexp.MustCompile(`^/[a-zA-Z0-9_/-]+$`).MatchString(repoRoot) || strings.Contains(repoRoot, "..") {
		t.Fatal("FAAS_REGION_WORKER_REPO must be a plain absolute checkout path")
	}
	pool, err := pgxpool.New(ctx, required("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := &regionalJobAcceptance{ctx: ctx, store: state.NewPgStore(pool),
		client:  api.NewClient(required("FAAS_REGION_API_URL"), required("FAAS_REGION_API_TOKEN")),
		stopped: make(map[int]bool), metricsURL: metricsURL, repoRoot: repoRoot}
	var targets []string
	f.nodes, targets = regionalAcceptanceWorkers(t, ctx, f.store, required)
	f.region = *f.nodes[0].Region
	registry, err := f.store.ComputeRegionByID(ctx, f.region)
	if err != nil || !registry.AcceptingNewApps {
		t.Fatalf("region must accept definitions: %v", err)
	}
	for i, target := range targets {
		run, err := e2etest.NativeAcceptanceSSH(target)
		if err != nil {
			t.Fatal(err)
		}
		f.commands = append(f.commands, run)
		if !f.leakcheck(t, ctx, i) {
			t.FailNow()
		}
	}
	f.remoteBoot = f.remoteBoots(t, ctx)
	f.job, err = f.client.CreateJob(ctx, api.CreateJobRequest{
		Name: "regional-native-" + uuid.NewString()[:12], Kind: "batch", ComputeRegion: f.region,
		ImageRef: image, Command: []string{"/bin/sh", "-c", "sleep 30; echo gregale-regional-job"},
		RAMMB: 128, TaskTimeoutSec: 120, MaxParallelism: 1, RetryMax: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	//nolint:contextcheck // Cleanup must restore workers after the test context is canceled.
	t.Cleanup(func() { f.cleanup(t) })
	if f.job.ComputeRegion != f.region {
		t.Fatal("API lost the selected job region")
	}
	f.wait(t, ctx, "digest-pinned job image materialization", 5*time.Minute, func(ctx context.Context) bool {
		job, err := f.client.GetJob(ctx, f.job.Name)
		if err != nil {
			t.Fatal(err)
		}
		if job.ImageMaterializationStatus == "failed" {
			t.Fatal("job image materialization failed")
		}
		return job.ImageMaterializationStatus == "ready" && job.ImageResolvedDigest == image[strings.LastIndex(image, "@")+1:]
	})
	return f
}

func (f *regionalJobAcceptance) dispatch(t *testing.T) api.JobRunResponse {
	t.Helper()
	retries, parallelism, timeout := 1, 1, 120
	run, err := f.client.CreateJobRun(f.ctx, f.job.Name, api.CreateJobRunRequest{Tasks: 1, Parallelism: &parallelism, RetryMax: &retries, TaskTimeoutSec: &timeout})
	if err != nil {
		t.Fatal(err)
	}
	f.runs = append(f.runs, run)
	if run.JobID != f.job.ID || run.Tasks != 1 || run.ImageResolvedDigestSnapshot == "" {
		t.Fatal("run did not retain the admitted image and job identity")
	}
	return run
}

func (f *regionalJobAcceptance) task(t *testing.T, ctx context.Context, run api.JobRunResponse) api.JobTaskResponse {
	t.Helper()
	tasks, err := f.client.ListJobRunTasks(ctx, f.job.Name, run.ID)
	if err != nil || tasks.Total != 1 || len(tasks.Tasks) != 1 || tasks.Tasks[0].RunID != run.ID || tasks.Tasks[0].TaskIndex != 0 {
		t.Fatalf("expected exactly the admitted task: %+v %v", tasks, err)
	}
	return tasks.Tasks[0]
}

func (f *regionalJobAcceptance) wait(t *testing.T, parent context.Context, description string, within time.Duration, predicate func(context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, within)
	defer cancel()
	for {
		f.placement(t, ctx)
		if predicate(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", description, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func (f *regionalJobAcceptance) placement(t *testing.T, ctx context.Context) []state.JobExecutionInstance {
	t.Helper()
	instances, err := f.store.JobExecutionInstances(ctx, f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		node, err := f.store.ComputeNodeByID(ctx, instance.NodeID)
		if err != nil || instance.JobID != f.job.ID || instance.Kind != "job_task" || !state.ComputeRegionAllowsNode(f.region, node) {
			t.Fatalf("job instance escaped selected region: %+v %v", instance, err)
		}
	}
	return instances
}

func (f *regionalJobAcceptance) service(t *testing.T, ctx context.Context, index int, action string) {
	t.Helper()
	if action == "stop" {
		f.stopped[index] = true
	}
	if _, err := f.commands[index](ctx, "sudo", "-n", "systemctl", action, "faas-vmmd"); err != nil {
		t.Fatalf("%s vmmd on %s: %v", action, f.nodes[index].Name, err)
	}
	if action == "start" {
		delete(f.stopped, index)
	}
}

func (f *regionalJobAcceptance) workerLoss(t *testing.T) {
	run := f.dispatch(t)
	var first state.JobExecutionInstance
	f.wait(t, f.ctx, "initial real VM execution", 3*time.Minute, func(ctx context.Context) bool {
		task := f.task(t, ctx, run)
		if task.Status != "claimed" || task.InstanceID == "" {
			return false
		}
		var err error
		first, err = f.store.JobExecutionInstanceByID(ctx, task.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		return first.State == "running"
	})
	owner := 0
	if first.NodeID == f.nodes[1].ID {
		owner = 1
	}
	if first.NodeID != f.nodes[owner].ID {
		t.Fatal("initial VM is not on a designated regional worker")
	}
	f.peer = 1 - owner
	f.service(t, f.ctx, owner, "stop")
	f.wait(t, f.ctx, "retry after worker loss", 8*time.Minute, func(ctx context.Context) bool {
		current, err := f.client.GetJobRun(ctx, f.job.Name, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.AggregateStatus == "dead_letter" || current.AggregateStatus == "failed" {
			t.Fatal("worker loss exhausted the retry instead of recovering")
		}
		return current.AggregateStatus == "succeeded"
	})
	attempts, err := f.client.ListJobTaskAttempts(f.ctx, f.job.Name, run.ID, 0)
	if err != nil || len(attempts.Attempts) != 2 {
		t.Fatalf("need retained loss and retry outcomes: %+v %v", attempts, err)
	}
	lost, recovered := attempts.Attempts[0], attempts.Attempts[1]
	if lost.Attempt != 1 || lost.Status == "succeeded" || lost.InstanceID != first.ID || recovered.Attempt != 2 || recovered.Status != "succeeded" || recovered.InstanceID == "" || recovered.InstanceID == first.ID {
		t.Fatalf("loss/retry evidence is inconsistent: %+v", attempts.Attempts)
	}
	peer, err := f.store.JobExecutionInstanceByID(f.ctx, recovered.InstanceID)
	if err != nil || peer.NodeID != f.nodes[f.peer].ID {
		t.Fatalf("retry did not execute on the regional peer: %+v %v", peer, err)
	}
	f.unchangedRemoteBoots(t)
	t.Logf("regional job %s run %s: lost instance %s on %s; retry %s on %s", f.job.ID, run.ID, first.ID, first.NodeID, peer.ID, peer.NodeID)
}

func (f *regionalJobAcceptance) regionOutage(t *testing.T) {
	for i := range f.nodes[:2] {
		if !f.stopped[i] {
			f.service(t, f.ctx, i, "stop")
		}
	}
	f.wait(t, f.ctx, "whole region becomes unavailable", 3*time.Minute, func(ctx context.Context) bool {
		catalog, err := f.client.ListComputeRegions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, region := range catalog {
			if region.ID == f.region {
				return region.Availability == "unavailable" && region.EligibleNodes == 0
			}
		}
		return false
	})
	baseline := make(map[string]bool)
	for _, instance := range f.placement(t, f.ctx) {
		baseline[instance.ID] = true
	}
	run := f.dispatch(t)
	initial := f.task(t, f.ctx, run)
	if initial.Status != "queued" || initial.InstanceID != "" {
		t.Fatal("outage run did not start queued")
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		task := f.task(t, f.ctx, run)
		if task.Status != "queued" || task.Attempt != initial.Attempt || task.InstanceID != "" {
			t.Fatalf("outage consumed a retry or claimed a VM: %+v", task)
		}
		for _, instance := range f.placement(t, f.ctx) {
			if !baseline[instance.ID] {
				t.Fatalf("new instance admitted during outage: %+v", instance)
			}
		}
		select {
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		case <-time.After(time.Second):
		}
	}
	f.unchangedRemoteBoots(t)
	f.service(t, f.ctx, f.peer, "start")
	f.wait(t, f.ctx, "queued work resumes in its region", 5*time.Minute, func(ctx context.Context) bool {
		current, err := f.client.GetJobRun(ctx, f.job.Name, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current.AggregateStatus == "succeeded"
	})
	attempts, err := f.client.ListJobTaskAttempts(f.ctx, f.job.Name, run.ID, 0)
	if err != nil || len(attempts.Attempts) != 1 || attempts.Attempts[0].Attempt != initial.Attempt || attempts.Attempts[0].Status != "succeeded" {
		t.Fatalf("outage/resumption changed the retry budget: %+v %v", attempts, err)
	}
	instance, err := f.store.JobExecutionInstanceByID(f.ctx, attempts.Attempts[0].InstanceID)
	if err != nil || instance.NodeID != f.nodes[f.peer].ID {
		t.Fatalf("resumed task escaped region: %+v %v", instance, err)
	}
	f.unchangedRemoteBoots(t)
	t.Logf("regional job %s outage run %s: queued without retry for one minute; resumed on %s; remote boots=%g", f.job.ID, run.ID, instance.NodeID, f.remoteBoot.count)
}

func (f *regionalJobAcceptance) remoteBoots(t *testing.T, ctx context.Context) regionalJobBootObservation {
	t.Helper()
	invocation, err := f.commands[2](ctx, "systemctl", "show", "faas-vmmd", "--property=InvocationID", "--value")
	if err != nil || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(strings.TrimSpace(string(invocation))) {
		t.Fatal("cannot verify remote vmmd invocation")
	}
	metrics, err := f.commands[2](ctx, "curl", "--fail", "--silent", "--show-error", "--max-time", "10", f.metricsURL)
	if err != nil {
		t.Fatal("cannot read remote vmmd boot metrics")
	}
	count, err := e2etest.JobBootOperations(string(metrics))
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.commands[2](ctx, "systemctl", "show", "faas-vmmd", "--property=InvocationID", "--value")
	if err != nil || string(after) != string(invocation) {
		t.Fatal("remote vmmd restarted during metrics observation")
	}
	return regionalJobBootObservation{invocation: strings.TrimSpace(string(invocation)), count: count}
}

func (f *regionalJobAcceptance) unchangedRemoteBoots(t *testing.T) {
	t.Helper()
	observed := f.remoteBoots(t, f.ctx)
	if observed != f.remoteBoot {
		t.Fatalf("foreign vmmd received a job boot or restarted: before=%+v after=%+v", f.remoteBoot, observed)
	}
}

func (f *regionalJobAcceptance) leakcheck(t *testing.T, ctx context.Context, index int) bool {
	t.Helper()
	output, err := f.commands[index](ctx, "sudo", "-n", "bash", f.repoRoot+"/deploy/scripts/leakcheck.sh")
	if err != nil || !strings.Contains(string(output), "leakcheck OK") {
		t.Errorf("worker %s failed leakcheck: %s (%v)", f.nodes[index].Name, output, err)
		return false
	}
	t.Logf("worker %s: %s", f.nodes[index].Name, strings.TrimSpace(string(output)))
	return true
}

func (f *regionalJobAcceptance) cleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for index := range f.stopped {
		if _, err := f.commands[index](ctx, "sudo", "-n", "systemctl", "start", "faas-vmmd"); err != nil {
			t.Errorf("restore vmmd on %s: %v", f.nodes[index].Name, err)
		}
	}
	for _, run := range f.runs {
		current, err := f.client.GetJobRun(ctx, f.job.Name, run.ID)
		if err != nil {
			t.Errorf("read cleanup run: %v", err)
			continue
		}
		if current.AggregateStatus == "queued" || current.AggregateStatus == "running" {
			if _, err := f.client.CancelJobRun(ctx, f.job.Name, run.ID); err != nil {
				t.Errorf("cancel acceptance run: %v", err)
			}
		}
	}
	for {
		instances, err := f.store.JobExecutionInstances(ctx, f.job.ID)
		if err != nil {
			t.Errorf("read cleanup instances: %v", err)
			break
		}
		live := false
		for _, instance := range instances {
			if instance.State == "waking" || instance.State == "cold_booting" || instance.State == "running" {
				live = true
			}
		}
		if !live {
			break
		}
		select {
		case <-ctx.Done():
			t.Errorf("acceptance instances not settled: %v", ctx.Err())
			goto deleteJob
		case <-time.After(time.Second):
		}
	}
deleteJob:
	if _, err := f.client.DeleteJob(ctx, f.job.Name); err != nil {
		t.Errorf("delete acceptance job: %v", err)
	}
	checkCtx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	for index := range f.nodes {
		f.leakcheck(t, checkCtx, index)
	}
	t.Logf("regional Jobs cleanup checked for %s", f.job.ID)
}
