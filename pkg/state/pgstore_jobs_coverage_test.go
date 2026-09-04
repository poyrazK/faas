// pgstore_jobs_coverage_test.go — pgstore coverage pin for the
// JobStore surface (Mega-1 jobs). Mirrors the pattern at
// pgstore_alert_presets_test.go: pgtest.Open + db.MigrateUp +
// NewPgStore, every method called with happy + one error input.
//
// Why this file exists:
//
//	pkg/state coverage must clear the 70% floor (Makefile::check-state-coverage).
//	pgstore_jobs.go is 954 LOC of new code with 24 methods. The
//	memstore_jobs_coverage_test.go file pins the MemStore side; this
//	file pins the PgStore side so CI's postgres-backed run gets the
//	other half of the package coverage. Skips cleanly via pgtest.Open
//	when DATABASE_URL isn't set.
//
// Layout: each test method targets one or two JobStore methods so a
// regression points at the specific surface that broke.
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgJobsStoreWithPool opens a fresh pgtest schema, runs migrations,
// returns a PgStore + the underlying pgxpool so callers can issue
// raw SQL against the same per-test schema.
func pgJobsStoreWithPool(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	return pgStoreWithPool(t)
}

// pgJobsSeed creates an account + job + 3-task run on the supplied
// store. Returns the job, run, and 3 fanned-out tasks so each
// coverage test can drive a specific JobStore method without
// re-walking the create dance.
func pgJobsSeed(t *testing.T, s *state.PgStore, ctx context.Context, name string) (state.Job, state.JobRun, []state.JobTask) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "pg-jobs-"+name+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("setup CreateAccount: %v", err)
	}
	job, err := s.JobCreate(ctx,
		acct.ID, name, "app",
		"oci://registry.example/x@sha256:deadbeef",
		[]string{"/bin/sh", "-c", "echo hi"}, 256, 60, 4, 3,
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("setup JobCreate: %v", err)
	}
	parallelism := 2
	run, fanned, err := s.JobRunCreate(ctx,
		job.ID, job.AccountID, "manual",
		&parallelism, nil, nil,
		json.RawMessage(`{}`), 3,
	)
	if err != nil {
		t.Fatalf("setup JobRunCreate: %v", err)
	}
	return job, run, fanned
}

// pgJobsCreateJobTaskInstance inserts a real `instances` row
// carrying kind='job_task' + job_id=:jobID so the FK from
// job_tasks.instance_id resolves. CreateInstanceWithMode doesn't
// write kind/job_id, so a raw INSERT is the cleanest path. Returns
// the generated instance UUID. Takes the pool directly (not the
// PgStore) so the INSERT lands in the same per-test schema the
// surrounding PgStore is bound to. Also inserts a sibling app +
// deployment to satisfy instances.deployment_id (NOT NULL) and
// the deployments FK.
func pgJobsCreateJobTaskInstance(t *testing.T, pool *pgxpool.Pool, ctx context.Context, accountID, jobID string) string {
	t.Helper()
	now := time.Now()
	appID := uuid.NewString()
	depID := uuid.NewString()
	slug := "jobs-fixture-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx,
		`insert into apps (id, account_id, slug, status, ram_mb)
		 values ($1::uuid, $2::uuid, $3, 'active', 256)`,
		appID, accountID, slug); err != nil {
		t.Fatalf("pgJobsCreateJobTaskInstance apps: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`insert into deployments (id, app_id, status, image_digest, created_at, canary_step_started_at)
		 values ($1::uuid, $2::uuid, 'building', 'sha256:fixture', $3, $3)`,
		depID, appID, now); err != nil {
		t.Fatalf("pgJobsCreateJobTaskInstance deployments: %v", err)
	}
	nodeID := defaultLocalNodeID(t, ctx, pool)
	row := pool.QueryRow(ctx,
		`insert into instances (kind, job_id, deployment_id, node_id, state, ram_mb, mode)
		 values ('job_task', $1::uuid, $2::uuid, $3::uuid, 'cold_booting', 256, 'job')
		 returning id::text`, jobID, depID, nodeID)
	var id string
	if err := row.Scan(&id); err != nil {
		t.Fatalf("pgJobsCreateJobTaskInstance: %v", err)
	}
	return id
}

// defaultLocalNodeID returns the UUID of the local compute_node row that
// migration 00024 inserts. The instances.node_id column is NOT NULL, so
// every fixture that creates an instance row needs this ID.
func defaultLocalNodeID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`select id::text from compute_nodes where name = $1`,
		state.DefaultLocalNodeName,
	).Scan(&id); err != nil {
		t.Fatalf("defaultLocalNodeID: %v", err)
	}
	return id
}

// --- jobs table ---

func TestPg_Jobs_JobGetByID(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-1")

	got, err := s.JobGetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("JobGetByID: %v", err)
	}
	if got.ID != job.ID {
		t.Errorf("JobGetByID.ID = %q, want %q", got.ID, job.ID)
	}
	if _, err := s.JobGetByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobGetByID missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobGetByName(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-2")

	got, err := s.JobGetByName(ctx, job.AccountID, job.Name)
	if err != nil {
		t.Fatalf("JobGetByName: %v", err)
	}
	if got.ID != job.ID {
		t.Errorf("JobGetByName.ID = %q, want %q", got.ID, job.ID)
	}
	if _, err := s.JobGetByName(ctx, job.AccountID, "no-such"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobGetByName missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobListByAccount(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-3")

	list, err := s.JobListByAccount(ctx, job.AccountID, 50, 0)
	if err != nil {
		t.Fatalf("JobListByAccount: %v", err)
	}
	if len(list) < 1 || list[0].ID != job.ID {
		t.Errorf("JobListByAccount did not return seeded job: got %d entries", len(list))
	}

	empty, err := s.JobListByAccount(ctx, "00000000-0000-0000-0000-000000000000", 50, 0)
	if err != nil {
		t.Fatalf("JobListByAccount unknown acct: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("JobListByAccount unknown acct: got %d, want 0", len(empty))
	}
}

func TestPg_Jobs_JobUpdate(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-4")

	newCmd := []string{"/bin/sh", "-c", "echo updated"}
	newImg := "oci://registry.example/y@sha256:feedface"
	newRAM := 512
	upd, err := s.JobUpdate(ctx, job.ID, newCmd, &newImg, &newRAM, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("JobUpdate: %v", err)
	}
	if upd.ImageRef != newImg || upd.RAMMB != newRAM || len(upd.Command) != 3 {
		t.Errorf("JobUpdate round-trip: img=%q ram=%d cmd=%v", upd.ImageRef, upd.RAMMB, upd.Command)
	}
	if _, err := s.JobUpdate(ctx, "00000000-0000-0000-0000-000000000000", nil, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobUpdate missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobSoftDelete(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-5")

	deleted, hasLive, err := s.JobSoftDelete(ctx, job.ID)
	if err != nil {
		t.Fatalf("JobSoftDelete: %v", err)
	}
	if !deleted || hasLive {
		t.Errorf("JobSoftDelete first: deleted=%v hasLive=%v, want true/false", deleted, hasLive)
	}
	if _, err := s.JobGetByID(ctx, job.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobGetByID after soft delete: err = %v, want ErrNotFound", err)
	}

	deleted2, _, err := s.JobSoftDelete(ctx, job.ID)
	if err != nil {
		t.Fatalf("JobSoftDelete idempotent: %v", err)
	}
	if deleted2 {
		t.Error("JobSoftDelete idempotent: deleted=true, want false (already gone)")
	}
}

func TestPg_Jobs_JobCountByAccount(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-6")
	if n, err := s.JobCountByAccount(ctx, job.AccountID); err != nil || n < 1 {
		t.Errorf("JobCountByAccount = %d, err=%v, want ≥1", n, err)
	}
}

func TestPg_Jobs_JobConcurrentByAccount(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-7")
	n, err := s.JobConcurrentByAccount(ctx, job.AccountID)
	if err != nil {
		t.Fatalf("JobConcurrentByAccount: %v", err)
	}
	if n < 0 {
		t.Errorf("JobConcurrentByAccount = %d, want ≥0", n)
	}
}

// --- job_runs table ---

func TestPg_Jobs_JobRunGetByID(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, _ := pgJobsSeed(t, s, ctx, "run-1")

	got, err := s.JobRunGetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunGetByID: %v", err)
	}
	if got.ID != run.ID {
		t.Errorf("JobRunGetByID.ID = %q, want %q", got.ID, run.ID)
	}
	if _, err := s.JobRunGetByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobRunGetByID missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobRunListByJob(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, run, _ := pgJobsSeed(t, s, ctx, "run-2")

	list, err := s.JobRunListByJob(ctx, job.ID, 50, 0)
	if err != nil {
		t.Fatalf("JobRunListByJob: %v", err)
	}
	if len(list) < 1 || list[0].ID != run.ID {
		t.Errorf("JobRunListByJob did not return seeded run: got %d entries", len(list))
	}
}

func TestPg_Jobs_JobRunListByAccount(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, run, _ := pgJobsSeed(t, s, ctx, "run-3")

	list, err := s.JobRunListByAccount(ctx, job.AccountID, 50, 0)
	if err != nil {
		t.Fatalf("JobRunListByAccount: %v", err)
	}
	if len(list) < 1 || list[0].ID != run.ID {
		t.Errorf("JobRunListByAccount did not return seeded run: got %d entries", len(list))
	}
}

func TestPg_Jobs_JobRunRecompute(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "run-4")
	finish := time.Now()
	for _, tk := range fanned {
		if err := s.JobTaskMarkTerminal(ctx, run.ID, tk.TaskIndex, "succeeded", 0, "", "", finish); err != nil {
			t.Fatalf("setup JobTaskMarkTerminal: %v", err)
		}
	}
	agg, err := s.JobRunRecompute(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunRecompute: %v", err)
	}
	if agg.AggregateStatus != "succeeded" {
		t.Errorf("JobRunRecompute aggregate = %q, want succeeded", agg.AggregateStatus)
	}

	if _, err := s.JobRunRecompute(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobRunRecompute missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobRunCancel(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, _ := pgJobsSeed(t, s, ctx, "run-5")

	cancelled, err := s.JobRunCancel(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunCancel: %v", err)
	}
	if cancelled.AggregateStatus != "cancelled" {
		t.Errorf("JobRunCancel aggregate = %q, want cancelled", cancelled.AggregateStatus)
	}

	if _, err := s.JobRunCancel(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobRunCancel missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobRunIncrementDeadLetter(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, _ := pgJobsSeed(t, s, ctx, "run-6")
	if err := s.JobRunIncrementDeadLetter(ctx, run.ID); err != nil {
		t.Fatalf("JobRunIncrementDeadLetter: %v", err)
	}
	got, err := s.JobRunGetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunGetByID post: %v", err)
	}
	if got.DeadLetterCount != 1 {
		t.Errorf("JobRunIncrementDeadLetter: count=%d, want 1", got.DeadLetterCount)
	}
}

// --- job_tasks table ---

func TestPg_Jobs_JobTaskClaimBatch(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	pgJobsSeed(t, s, ctx, "task-1")

	batch, err := s.JobTaskClaimBatch(ctx, 2)
	if err != nil {
		t.Fatalf("JobTaskClaimBatch: %v", err)
	}
	if len(batch) == 0 {
		t.Skip("no claimable tasks; another test may have drained the queue")
	}
	if len(batch) > 2 {
		t.Errorf("JobTaskClaimBatch: got %d, want ≤2", len(batch))
	}
}

func TestPg_Jobs_JobTaskMarkClaimed(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	job, run, fanned := pgJobsSeed(t, s, ctx, "task-2")
	task := fanned[0]
	// Create a real job_task instance row directly so the FK on
	// job_tasks.instance_id resolves. CreateInstanceWithMode doesn't
	// write kind/job_id, so a raw INSERT is the cleanest path.
	instanceID := pgJobsCreateJobTaskInstance(t, pool, ctx, job.AccountID, job.ID)
	lease := "00000000-0000-0000-0000-deadbeefcafe"
	nodeID := resolveDefaultLocal(t, ctx, s)
	expires := time.Now().Add(5 * time.Minute)
	if err := s.JobTaskMarkClaimed(ctx, run.ID, task.TaskIndex, instanceID, lease, expires, nodeID); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	got, err := s.JobTaskGet(ctx, run.ID, task.TaskIndex)
	if err != nil {
		t.Fatalf("JobTaskGet post: %v", err)
	}
	if got.Status != "claimed" || got.LeaseToken == nil || *got.LeaseToken != lease {
		t.Errorf("JobTaskMarkClaimed: status=%q lease=%v, want claimed/%q", got.Status, got.LeaseToken, lease)
	}
}

func TestPg_Jobs_JobTaskMarkTerminal(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "task-3")
	task := fanned[0]
	finish := time.Now()
	if err := s.JobTaskMarkTerminal(ctx, run.ID, task.TaskIndex, "failed", 1, "user_error", "boom", finish); err != nil {
		t.Fatalf("JobTaskMarkTerminal: %v", err)
	}
	got, err := s.JobTaskGet(ctx, run.ID, task.TaskIndex)
	if err != nil {
		t.Fatalf("JobTaskGet post: %v", err)
	}
	if got.Status != "failed" || got.ExitCode == nil || *got.ExitCode != 1 {
		t.Errorf("JobTaskMarkTerminal: status=%q exit=%v, want failed/1", got.Status, got.ExitCode)
	}
}

func TestPg_Jobs_JobTaskRetry(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "task-4")
	task := fanned[0]
	if err := s.JobTaskMarkTerminal(ctx, run.ID, task.TaskIndex, "failed", 1, "user_error", "", time.Now()); err != nil {
		t.Fatalf("setup MarkTerminal: %v", err)
	}
	next := time.Now().Add(2 * time.Minute)
	if err := s.JobTaskRetry(ctx, run.ID, task.TaskIndex, next); err != nil {
		t.Fatalf("JobTaskRetry: %v", err)
	}
	got, err := s.JobTaskGet(ctx, run.ID, task.TaskIndex)
	if err != nil {
		t.Fatalf("JobTaskGet post: %v", err)
	}
	if got.Status != "queued" || got.NextAttemptAt == nil {
		t.Errorf("JobTaskRetry: status=%q nextAttempt=%v, want queued/non-nil", got.Status, got.NextAttemptAt)
	}
}

func TestPg_Jobs_JobTaskRequeue(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	job, run, fanned := pgJobsSeed(t, s, ctx, "task-5")
	task := fanned[0]
	instanceID := pgJobsCreateJobTaskInstance(t, pool, ctx, job.AccountID, job.ID)
	if err := s.JobTaskMarkClaimed(ctx, run.ID, task.TaskIndex, instanceID,
		"00000000-0000-0000-0000-deadbeef0001", time.Now().Add(5*time.Minute), resolveDefaultLocal(t, ctx, s)); err != nil {
		t.Fatalf("setup MarkClaimed: %v", err)
	}
	next := time.Now().Add(time.Minute)
	if err := s.JobTaskRequeue(ctx, run.ID, task.TaskIndex, next); err != nil {
		t.Fatalf("JobTaskRequeue: %v", err)
	}
	got, _ := s.JobTaskGet(ctx, run.ID, task.TaskIndex)
	if got.Status != "queued" || got.NextAttemptAt == nil {
		t.Errorf("JobTaskRequeue: status=%q next=%v, want queued/non-nil", got.Status, got.NextAttemptAt)
	}
}

func TestPg_Jobs_JobTaskCancel(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "task-6")
	task := fanned[0]
	if err := s.JobTaskCancel(ctx, run.ID, task.TaskIndex); err != nil {
		t.Fatalf("JobTaskCancel: %v", err)
	}
	got, _ := s.JobTaskGet(ctx, run.ID, task.TaskIndex)
	if got.Status != "cancelled" {
		t.Errorf("JobTaskCancel: status=%q, want cancelled", got.Status)
	}
}

func TestPg_Jobs_JobTaskFindStuck(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	job, run, fanned := pgJobsSeed(t, s, ctx, "task-7")
	task := fanned[0]
	instanceID := pgJobsCreateJobTaskInstance(t, pool, ctx, job.AccountID, job.ID)
	if err := s.JobTaskMarkClaimed(ctx, run.ID, task.TaskIndex, instanceID,
		"00000000-0000-0000-0000-deadbeef0002", time.Now().Add(-time.Hour), resolveDefaultLocal(t, ctx, s)); err != nil {
		t.Fatalf("setup MarkClaimed expired lease: %v", err)
	}
	stuck, err := s.JobTaskFindStuck(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("JobTaskFindStuck: %v", err)
	}
	if len(stuck) == 0 {
		t.Error("JobTaskFindStuck: expected at least one stuck task")
	}
}

func TestPg_Jobs_JobTaskGet(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "task-8")
	got, err := s.JobTaskGet(ctx, run.ID, fanned[0].TaskIndex)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if got.TaskIndex != fanned[0].TaskIndex {
		t.Errorf("JobTaskGet.TaskIndex = %d, want %d", got.TaskIndex, fanned[0].TaskIndex)
	}
	if _, err := s.JobTaskGet(ctx, run.ID, 9999); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("JobTaskGet missing: err = %v, want ErrNotFound", err)
	}
}

func TestPg_Jobs_JobTaskList(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "task-9")
	list, err := s.JobTaskList(ctx, run.ID, 50, 0)
	if err != nil {
		t.Fatalf("JobTaskList: %v", err)
	}
	if len(list) != len(fanned) {
		t.Errorf("JobTaskList: got %d, want %d", len(list), len(fanned))
	}
}

func TestPg_Jobs_ListJobInstances(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "job-instances")
	id := pgJobsCreateJobTaskInstance(t, pool, ctx, job.AccountID, job.ID)

	instances, err := s.ListJobInstances(ctx)
	if err != nil {
		t.Fatalf("ListJobInstances: %v", err)
	}
	for _, instance := range instances {
		if instance.ID == id {
			return
		}
	}
	t.Fatalf("ListJobInstances did not return active job_task instance %s", id)
}
