package ci

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// The database is real and fully migrated. Only GitHub bytes, object storage and
// scheduler/guest receipts are synthetic; these tests never execute commands.
type jobsHarness struct {
	pool                 *pgxpool.Pool
	store                *PostgresStore
	backend              JobsBackend
	blobs                *jobsTestBlobs
	stream               *jobsTestStream
	account, other, node string
	binding              GitHubBinding
	req                  JobsSubmitRequest
	clock                *qualificationClock
}

type jobsTestBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (b *jobsTestBlobs) Put(ctx context.Context, key string, src io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data[key] = bytes.Clone(raw)
	return nil
}
func (b *jobsTestBlobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, ok := b.data[key]
	if !ok {
		return nil, errors.New("missing synthetic blob")
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(raw))), nil
}

type jobsTestStream struct {
	mu                  sync.Mutex
	calls               int
	sha                 string
	body                []byte
	hook                func()
	account, repository string
	installation        int64
}

func (s *jobsTestStream) StreamSourceRef(ctx context.Context, account string, installation int64, repo, sha string, bound int64) (io.ReadCloser, string, bool, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, 0, err
	}
	s.mu.Lock()
	s.calls++
	s.account, s.repository, s.installation = account, repo, installation
	body, resolved, hook := bytes.Clone(s.body), s.sha, s.hook
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if bound != api.CIJobsSourceArchiveMaxBytes {
		return nil, "", false, 0, errors.New("incorrect source bound")
	}
	if resolved == "" {
		resolved = sha
	}
	return io.NopCloser(bytes.NewReader(body)), resolved, false, 0, nil
}

func newJobsHarness(t *testing.T) *jobsHarness {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	clock := &qualificationClock{}
	clock.nanos.Store(time.Now().UTC().UnixNano())
	h := &jobsHarness{pool: pool, account: uuid.NewString(), other: uuid.NewString(), node: uuid.NewString(), clock: clock,
		blobs: &jobsTestBlobs{data: make(map[string][]byte)}, stream: &jobsTestStream{body: []byte("opaque synthetic tar.gz; never extracted by host")}}
	h.restart()
	for _, account := range []string{h.account, h.other} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro')", account, account+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO compute_nodes(id,name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb)
 VALUES ($1,$2,'unix:///nonexistent-ci-test-vmmd.sock',4,8192,8,8192)`, h.node, "ci-test-"+h.node); err != nil {
		t.Fatal(err)
	}
	revision, err := h.store.PutRevision(t.Context(), h.account, "jobs-checks", executionDefinition())
	if err != nil {
		t.Fatal(err)
	}
	h.binding, err = h.store.PutGitHubBinding(t.Context(), h.account, "jobs-source", GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	h.req = JobsSubmitRequest{SubmitRequest: SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: h.binding.ID, Templates: make(map[string]string)}
	for name, job := range revision.Definition.Jobs {
		id := uuid.NewString()
		h.req.Templates[name] = id
		if _, err := pool.Exec(t.Context(), `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key)
 VALUES ($1,$2,'batch',$3,$4,$5,600,1,0,'{}',$6,'ready',$7,$8)`, id, h.account, "ci-"+name, job.Image, api.CIJobsRAMMB, []string{JobsRunnerPath, JobsRunnerProtocol}, strings.Split(job.Image, "@")[1], "jobs/"+id+"/image.ext4"); err != nil {
			t.Fatal(err)
		}
	}
	h.backend = JobsBackend{Config: ConfigFromEnv(func(name string) string {
		switch name {
		case InternalEnv, GitHubEnv, DispatchEnv, TransportEnv:
			return "1"
		case AccountsEnv:
			return h.account
		}
		return ""
	}), Store: h.store, Source: GitHubArchiveSource{Verify: h.verify, Stream: h.stream, Blobs: h.blobs}, Output: BlobJobsOutput{Blobs: h.blobs}}
	return h
}

func (h *jobsHarness) restart() {
	h.store = NewPostgresStore(h.pool)
	h.store.executionClock = h.clock.now
	h.backend.Store = h.store
}
func (h *jobsHarness) verify(_ context.Context, account string, installation, repository int64, name string) error {
	if account != h.account || installation != 12 || repository != 34 || name != "gregale/ci-qualification" {
		return ErrGitHubAccess
	}
	return nil
}
func (h *jobsHarness) admit(t *testing.T, key string) Execution {
	t.Helper()
	row, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, key, h.req)
	if err != nil || replay {
		t.Fatalf("admit: replay=%v err=%v", replay, err)
	}
	return row
}
func (h *jobsHarness) tick(t *testing.T, row Execution) Execution {
	t.Helper()
	next, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func (h *jobsHarness) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *jobsHarness) fakeInstance(t *testing.T, row Execution, name string) string {
	t.Helper()
	var job JobExecution
	for _, candidate := range row.Jobs {
		if candidate.Name == name {
			job = candidate
		}
	}
	instance := uuid.NewString()
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO instances(id,node_id,state,ram_mb,kind,job_id,mode) VALUES ($1,$2,'running',$3,'job_task',$4,'job')`, instance, h.node, api.CIJobsRAMMB, h.req.Templates[name]); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), `UPDATE job_tasks SET status='claimed',instance_id=$2,started_at=clock_timestamp(),lease_token=$3,lease_expires_at=clock_timestamp()+interval '1 minute',last_lease_node=$4 WHERE run_id=$1`, job.Attempt.ID, instance, uuid.NewString(), h.node); err != nil {
		t.Fatal(err)
	}
	return instance
}

func (h *jobsHarness) publish(t *testing.T, row Execution, name, status string, code int, logs []byte, artifact bool, stopped bool) string {
	t.Helper()
	var job JobExecution
	var plan PlannedJob
	for i, candidate := range row.Jobs {
		if candidate.Name == name {
			job, plan = candidate, row.Plan.Jobs[i]
		}
	}
	var instance string
	if err := h.pool.QueryRow(t.Context(), "SELECT coalesce(instance_id::text,'') FROM job_tasks WHERE run_id=$1", job.Attempt.ID).Scan(&instance); err != nil {
		t.Fatal(err)
	}
	if instance == "" {
		instance = h.fakeInstance(t, row, name)
	}
	key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, AttemptID: job.Attempt.ID, JobName: name, Fence: 1}
	prefix := jobsObjectPrefix(key) + "output/"
	result := JobsResultManifest{Version: 1, AccountID: h.account, ExecutionID: row.ID, AttemptID: job.Attempt.ID, CommitSHA: row.CommitSHA, Status: status, ExitCode: &code,
		Logs: SourceArchive{Key: prefix + "logs.bin", Digest: jobsDigest(logs), Bytes: int64(len(logs))}}
	for i, step := range plan.Steps {
		stepCode := 0
		stepStatus := StatusSucceeded
		if status == StatusFailed && i == 0 {
			stepCode, stepStatus = code, StatusFailed
		} else if status == StatusFailed {
			stepStatus = StatusSkipped
		} else if status == StatusCancelled {
			stepStatus = StatusCancelled
		}
		value := StepResult{Name: step.Name, Status: stepStatus}
		if stepStatus == StatusSucceeded || stepStatus == StatusFailed {
			value.ExitCode = &stepCode
		}
		result.Steps = append(result.Steps, value)
	}
	if err := h.blobs.Put(t.Context(), result.Logs.Key, bytes.NewReader(logs)); err != nil {
		t.Fatal(err)
	}
	if artifact {
		raw := []byte("failure or success report bytes")
		ref := JobsArtifact{ArtifactRef: ArtifactRef{Path: "reports/unit.xml", Digest: jobsDigest(raw), Bytes: int64(len(raw))}, Key: prefix + "artifacts/reports/unit.xml"}
		result.Artifacts = append(result.Artifacts, ref)
		if err := h.blobs.Put(t.Context(), ref.Key, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.blobs.Put(t.Context(), prefix+"result.json", bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status=$2,exit_code=$3,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL,last_lease_node=NULL WHERE run_id=$1", job.Attempt.ID, status, code); err != nil {
		t.Fatal(err)
	}
	if stopped {
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", instance); err != nil {
			t.Fatal(err)
		}
	}
	return instance
}

func TestCIJobs(t *testing.T) {
	// Preserve the normal skip behavior on machines without PostgreSQL. The
	// private clone runs all migrations and exercises actual Jobs constraints.
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	t.Run("success_cleanup_output_restart", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "fresh-jobs")
		row = h.tick(t, row)
		if row.Mode != ExecutionJobs || h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 1 {
			t.Fatal("dependent job started early")
		}
		var command []string
		var env []byte
		var retries, ram, timeout int
		if err := h.pool.QueryRow(t.Context(), "SELECT command,effective_env_snapshot,retry_max,ram_mb_snapshot,task_timeout_s FROM job_runs WHERE id=$1", row.Jobs[0].Attempt.ID).Scan(&command, &env, &retries, &ram, &timeout); err != nil {
			t.Fatal(err)
		}
		if command[0] != JobsRunnerPath || command[1] != JobsRunnerProtocol || retries != 0 || ram != api.CIJobsRAMMB || timeout != row.Plan.Jobs[0].TimeoutSeconds || string(env) != "{}" {
			t.Fatal("Jobs limits/runner isolation were lost")
		}
		raw, err := base64.StdEncoding.DecodeString(command[2])
		if err != nil {
			t.Fatal(err)
		}
		var manifest JobsManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.CommitSHA != row.CommitSHA || manifest.AccountID != h.account || manifest.Source.Bytes == 0 {
			t.Fatal("source identity missing")
		}
		logs := bytes.Repeat([]byte("x"), api.CILogPageMaxBytes+13)
		instance := h.publish(t, row, "test", StatusSucceeded, 0, logs, true, false)
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusRunning || len(row.Jobs[0].Attempt.Artifacts) != 0 {
			t.Fatal("completed task certified VM cleanup early")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", instance); err != nil {
			t.Fatal(err)
		}
		h.restart()
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusRunning || len(row.Jobs[0].Attempt.Logs) != api.CILogPageMaxBytes {
			t.Fatal("first terminal log page not retained")
		}
		// The final output is immutable after collection, including across restart.
		if err := h.blobs.Put(t.Context(), jobsObjectPrefix(AttemptKey{AccountID: h.account, ExecutionID: row.ID, AttemptID: row.Jobs[0].Attempt.ID})+"output/result.json", strings.NewReader("corrupted later")); err != nil {
			t.Fatal(err)
		}
		h.restart()
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusSucceeded || !bytes.Equal(row.Jobs[0].Attempt.Logs, logs) || len(row.Jobs[0].Attempt.Artifacts) != 1 {
			t.Fatal("restart lost verified output")
		}
		row = h.tick(t, row)
		if h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 2 {
			t.Fatal("ready dependency not dispatched exactly once")
		}
		h.publish(t, row, "build", StatusSucceeded, 0, nil, false, true)
		row = h.tick(t, row)
		if row.Status != StatusSucceeded {
			t.Fatalf("pipeline did not succeed: %+v", row)
		}
		if h.stream.calls != 2 || h.stream.account != h.account || h.stream.installation != 12 || h.stream.repository != row.Plan.Repository {
			t.Fatal("source was not account scoped or was fetched twice")
		}
	})
	t.Run("failure_artifacts_skip_dependents", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.tick(t, h.admit(t, "failure"))
		h.publish(t, row, "test", StatusFailed, 2, []byte("synthetic test failure"), true, true)
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusFailed || len(row.Jobs[0].Attempt.Artifacts) != 1 {
			t.Fatal("failure artifacts lost")
		}
		row = h.tick(t, row)
		if row.Status != StatusFailed || row.Jobs[1].Status != StatusSkipped || h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 1 {
			t.Fatal("failed dependencies dispatched")
		}
	})
	t.Run("fresh_admission_and_flags", func(t *testing.T) {
		h := newJobsHarness(t)
		blocked, _, err := h.store.Submit(t.Context(), h.account, "old-blocked", h.req.SubmitRequest)
		if err != nil {
			t.Fatal(err)
		}
		qualification, _, err := h.store.SubmitQualification(t.Context(), h.account, "synthetic", h.req.SubmitRequest)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{InternalEnv, GitHubEnv, DispatchEnv, AccountsEnv} {
			cfg := ConfigFromEnv(func(key string) string {
				if key == name {
					return ""
				}
				if key == AccountsEnv {
					return h.account
				}
				return "1"
			})
			if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "disabled", h.req); !errors.Is(err, ErrDispatchDisabled) {
				t.Fatalf("missing %s enabled admission: %v", name, err)
			}
		}
		if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, qualification.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("qualification reached compute: %v", err)
		}
		if h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 0 {
			t.Fatal("old data dispatched")
		}
		row := h.admit(t, "fresh")
		again, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "fresh", h.req)
		if err != nil || !replay || again.ID != row.ID {
			t.Fatalf("idempotency failed: %v", err)
		}
		if _, _, err := h.store.SubmitQualification(t.Context(), h.account, "fresh", h.req.SubmitRequest); !errors.Is(err, ErrConflict) {
			t.Fatal("Jobs identity replayed as qualification")
		}
		old, err := h.store.Run(t.Context(), h.account, blocked.ID)
		if err != nil || old.Status != StatusBlocked {
			t.Fatal("blocked receipt changed")
		}
	})
	t.Run("cancel_before_start_and_late_start", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "cancel-race")
		lease, err := h.store.claimExecution(t.Context(), h.account, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		attempt := lease.Execution.Jobs[0].Attempt
		key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, JobName: "test", AttemptID: attempt.ID, Fence: lease.Fence}
		req, _, err := jobsAttemptFor(lease.Execution, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.backend.Cancel(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		if err := h.backend.Start(t.Context(), req); err != nil {
			t.Fatal(err)
		}
		if h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 0 || h.stream.calls != 0 {
			t.Fatal("cancel tombstone allowed start")
		}
		observation, err := h.backend.Observe(t.Context(), ObserveRequest{Key: key, LogLimit: api.CILogPageMaxBytes})
		if err != nil || observation.Status != StatusCancelled {
			t.Fatalf("tombstone not observable: %v", err)
		}
	})
	t.Run("source_inflight_cancel_fences_creation", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "source-race")
		entered, release := make(chan struct{}), make(chan struct{})
		h.stream.hook = func() { close(entered); <-release }
		done := make(chan error, 1)
		go func() {
			_, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID)
			done <- err
		}()
		<-entered
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		h.clock.advance(api.CIExecutionLeaseSeconds + 1)
		row = h.tick(t, row)
		close(release)
		if err := <-done; !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale source start not fenced: %v", err)
		}
		if row.Status != StatusCancelled || h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 0 {
			t.Fatal("cancelled source fetch created a job")
		}
	})
	t.Run("cancel_waits_for_cleanup_with_dispatch_off", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.tick(t, h.admit(t, "live-cancel"))
		instance := h.fakeInstance(t, row, "test")
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		h.backend.Config = Config{}
		row = h.tick(t, row)
		if row.Status != StatusCancelling {
			t.Fatal("cancelled task certified cleanup before stop")
		}
		h.publish(t, row, "test", StatusCancelled, 143, nil, true, true)
		row = h.tick(t, row)
		if row.Status != StatusCancelled || len(row.Jobs[0].Attempt.Artifacts) != 1 {
			t.Fatalf("cancel cleanup/output not finished for %s", instance)
		}
	})
	t.Run("lost_start_ack_and_stale_fence", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "lost-ack")
		lease, err := h.store.claimExecution(t.Context(), h.account, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, JobName: "test", AttemptID: lease.Execution.Jobs[0].Attempt.ID, Fence: lease.Fence}
		req, _, _ := jobsAttemptFor(lease.Execution, key)
		if err := h.backend.Start(t.Context(), req); err != nil {
			t.Fatal(err)
		}
		// Simulate losing the response before the engine commits its tick.
		h.clock.advance(api.CIExecutionLeaseSeconds + 1)
		h.restart()
		row = h.tick(t, row)
		if h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 1 || h.stream.calls != 1 {
			t.Fatal("restart repeated commands/source")
		}
		if err := h.backend.Start(t.Context(), req); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale Start fence accepted: %v", err)
		}
		if err := h.backend.Cancel(t.Context(), key); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("stale Cancel fence accepted")
		}
	})
	t.Run("queued_cancel_needs_no_guest_result", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.tick(t, h.admit(t, "queued-cancel"))
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		h.backend.Config = Config{}
		h.backend.Output = nil
		row = h.tick(t, row)
		if row.Status != StatusCancelled || h.count(t, "SELECT count(*) FROM job_tasks WHERE status='cancelled'") != 1 {
			t.Fatal("queued cancellation required a nonexistent guest result")
		}
	})
	t.Run("timeout_stops_and_retains_artifacts", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.tick(t, h.admit(t, "timeout"))
		h.fakeInstance(t, row, "test")
		h.clock.advance(row.Plan.Jobs[0].TimeoutSeconds + 1)
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusCancelling {
			t.Fatal("timeout reported terminal before cleanup")
		}
		h.publish(t, row, "test", StatusCancelled, 143, nil, true, true)
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusFailed || row.Jobs[0].Reason != ReasonTimeout || len(row.Jobs[0].Attempt.Artifacts) != 1 {
			t.Fatal("timeout outcome or artifacts lost")
		}
	})
	t.Run("changed_source_authority_refuses_dispatch", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "binding-revoked")
		h.stream.hook = func() {
			if err := h.store.DisableGitHubBinding(t.Context(), h.account, h.binding.Name); err != nil {
				t.Error(err)
			}
		}
		_, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID)
		if !errors.Is(err, ErrGitHubAccess) || h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 0 {
			t.Fatalf("revoked binding created work: %v", err)
		}
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		row = h.tick(t, row)
		if row.Status != StatusCancelled {
			t.Fatal("binding revocation blocked cleanup")
		}
	})
	t.Run("changed_template_refuses_dispatch", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "template-changed")
		h.stream.hook = func() {
			if _, err := h.pool.Exec(t.Context(), "UPDATE jobs SET image_storage_key='jobs/changed.ext4' WHERE id=$1", h.req.Templates["test"]); err != nil {
				t.Error(err)
			}
		}
		_, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID)
		if !errors.Is(err, ErrConflict) || h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 0 {
			t.Fatalf("template drift created work: %v", err)
		}
	})
	t.Run("attempt_request_is_immutable_and_scoped", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "scope")
		lease, err := h.store.claimExecution(t.Context(), h.account, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, JobName: "test", AttemptID: lease.Execution.Jobs[0].Attempt.ID, Fence: lease.Fence}
		req, _, _ := jobsAttemptFor(lease.Execution, key)
		changed := clone(req)
		changed.Job.Steps[0].Run = "unexpected command"
		if err := h.backend.Start(t.Context(), changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("mutated command accepted: %v", err)
		}
		changed = clone(req)
		changed.Key.AccountID = h.other
		if err := h.backend.Start(t.Context(), changed); err == nil {
			t.Fatal("foreign account accepted")
		}
		if h.stream.calls != 0 {
			t.Fatal("invalid request fetched source")
		}
	})
	t.Run("admission_refuses_unqualified_templates_and_accounts", func(t *testing.T) {
		h := newJobsHarness(t)
		for _, change := range []string{
			"UPDATE jobs SET env_overrides='{\"SCM_TOKEN\":\"supersecret\"}' WHERE kind='batch'",
			"UPDATE jobs SET env_overrides='{}',command=ARRAY['/bin/sh','-c','unexpected'] WHERE kind='batch'",
			"UPDATE jobs SET command=ARRAY['/usr/local/bin/gregale-ci-runner','v1'],image_materialization_status='pending' WHERE kind='batch'",
			"UPDATE jobs SET image_materialization_status='ready',ram_mb=2048 WHERE kind='batch'",
			"UPDATE jobs SET ram_mb=512,account_id='" + h.other + "' WHERE kind='batch'",
		} {
			if _, err := h.pool.Exec(t.Context(), change); err != nil {
				t.Fatal(err)
			}
			if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "reject", h.req); err == nil {
				t.Fatal("unqualified template admitted")
			}
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE jobs SET account_id=$1", h.account); err != nil {
			t.Fatal(err)
		}
		for _, plan := range []string{"free", "hobby"} {
			if _, err := h.pool.Exec(t.Context(), "UPDATE accounts SET plan=$2 WHERE id=$1", h.account, plan); err != nil {
				t.Fatal(err)
			}
			if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "reject", h.req); err == nil {
				t.Fatal("unsupported plan or timeout admitted")
			}
		}
		if h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
			t.Fatal("failed admissions leaked executions")
		}
	})
	t.Run("concurrent_starts_one_run", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.admit(t, "concurrent")
		lease, err := h.store.claimExecution(t.Context(), h.account, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, JobName: "test", AttemptID: lease.Execution.Jobs[0].Attempt.ID, Fence: lease.Fence}
		req, _, _ := jobsAttemptFor(lease.Execution, key)
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Go(func() { errs <- h.backend.Start(t.Context(), req) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if h.count(t, "SELECT count(*) FROM job_runs WHERE account_id=$1", h.account) != 1 {
			t.Fatal("duplicate job runs after concurrent Start")
		}
	})
	t.Run("bounded_admission_and_rollback_guard", func(t *testing.T) {
		h := newJobsHarness(t)
		for range api.CIMaxJobsExecutionsPerAccount {
			row := h.admit(t, uuid.NewString())
			if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "over-quota", h.req); !errors.Is(err, ErrLimit) {
			t.Fatalf("quota not enforced: %v", err)
		}
		data, err := os.ReadFile("../../migrations/20261016175839296_ci_jobs_adapter.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil {
			t.Fatal("rollback discarded Jobs recovery authority")
		}
		if h.count(t, "SELECT count(*) FROM ci_executions") != api.CIMaxJobsExecutionsPerAccount {
			t.Fatal("failed rollback lost executions")
		}
	})
}
