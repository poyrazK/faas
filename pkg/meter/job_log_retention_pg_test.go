//go:build !no_pg

// adr: 20261102183152

package meter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// jobLogTestRun seeds a finished run of tasks one-task-per-index. Task 0
// fails once and succeeds on its retry, so the attempt journal holds two of
// its attempts; every other task succeeds first time. The task rows walk the
// same queued -> terminal transitions schedd writes, so the journal trigger
// copies each attempt's log as it does in production.
func jobLogTestRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string, tasks int, finishedAgo time.Duration) string {
	t.Helper()
	jobID, runID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs (id, account_id, kind, name, image_ref, ram_mb, task_timeout_s, max_parallelism, retry_max, command)
VALUES ($1, $2, 'batch', $3, 'registry.example/job@sha256:00', 256, 60, 1, 2, ARRAY['run'])`,
		jobID, accountID, "logs-"+jobID[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO job_runs (id, job_id, account_id, trigger_kind, tasks, parallelism, aggregate_status, tasks_succeeded, started_at, finished_at, created_at)
VALUES ($1, $2, $3, 'scheduled', $4, 1, 'succeeded', $4, now() - $5::bigint * interval '1 second' - interval '1 minute',
 now() - $5::bigint * interval '1 second', now() - $5::bigint * interval '1 second' - interval '2 minutes')`,
		runID, jobID, accountID, tasks, int64(finishedAgo.Seconds())); err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		if _, err := pool.Exec(ctx, `INSERT INTO job_tasks (run_id, task_index) VALUES ($1, $2)`, runID, i); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			finishJobLogTestTask(t, ctx, pool, runID, i, "failed", 1, "attempt 1 failed\n", finishedAgo+time.Minute)
			if _, err := pool.Exec(ctx, `UPDATE job_tasks SET status = 'queued', attempt = attempt + 1, finished_at = NULL,
 exit_code = NULL, error_class = NULL, log_content = '', log_truncated = false WHERE run_id = $1 AND task_index = 0`, runID); err != nil {
				t.Fatal(err)
			}
		}
		finishJobLogTestTask(t, ctx, pool, runID, i, "succeeded", 0, fmt.Sprintf("task %d output\n", i), finishedAgo)
	}
	return runID
}

func finishJobLogTestTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID string, index int, status string, exit int, log string, ago time.Duration) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE job_tasks SET status = $3, exit_code = $4, log_content = $5, log_truncated = $6,
 started_at = now() - $7::bigint * interval '1 second' - interval '1 second', finished_at = now() - $7::bigint * interval '1 second'
 WHERE run_id = $1 AND task_index = $2`, runID, index, status, exit, log, status == "failed", int64(ago.Seconds())); err != nil {
		t.Fatal(err)
	}
}

type jobLogTaskRow struct {
	status    string
	exitCode  int
	log       string
	truncated bool
}

func jobLogTestTasks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID string) []jobLogTaskRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT status, coalesce(exit_code, -1), log_content, log_truncated FROM job_tasks WHERE run_id = $1 ORDER BY task_index`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []jobLogTaskRow
	for rows.Next() {
		var r jobLogTaskRow
		if err := rows.Scan(&r.status, &r.exitCode, &r.log, &r.truncated); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func jobLogTestAttempts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID string) (total, withLogs int) {
	t.Helper()
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE log_content <> '') FROM job_task_attempts WHERE run_id = $1`,
		runID).Scan(&total, &withLogs); err != nil {
		t.Fatal(err)
	}
	return total, withLogs
}

func jobLogTestAccount(t *testing.T, ctx context.Context, store *state.PgStore, plan api.Plan) string {
	t.Helper()
	account, err := store.CreateAccount(ctx, fmt.Sprintf("job-logs-%s-%s@example.test", plan, uuid.NewString()[:8]), plan)
	if err != nil {
		t.Fatal(err)
	}
	return account.ID
}

// adr: 20261102183152
func TestJobLogRetentionClearsOutputAndKeepsRunHistory(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account := jobLogTestAccount(t, ctx, store, api.PlanPro)
	expired := jobLogTestRun(t, ctx, pool, account, 3, 31*24*time.Hour)
	retained := jobLogTestRun(t, ctx, pool, account, 1, 29*24*time.Hour)
	attemptsBefore, logsBefore := jobLogTestAttempts(t, ctx, pool, expired)
	if attemptsBefore != 4 || logsBefore != 4 {
		t.Fatalf("seeded attempt journal = %d rows, %d with logs; want 4 and 4", attemptsBefore, logsBefore)
	}

	cleared, err := RetentionOnceJobLogs(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if cleared != (JobLogsCleared{Runs: 1, Tasks: 3, Attempts: 4}) {
		t.Fatalf("cleared = %+v, want the expired run's 3 tasks and 4 attempts", cleared)
	}
	for i, task := range jobLogTestTasks(t, ctx, pool, expired) {
		if task.status != "succeeded" || task.exitCode != 0 || task.log != "" || !task.truncated {
			t.Fatalf("expired task %d = %+v, want its outcome kept and its output gone", i, task)
		}
	}
	attemptsAfter, logsAfter := jobLogTestAttempts(t, ctx, pool, expired)
	if attemptsAfter != attemptsBefore || logsAfter != 0 {
		t.Fatalf("attempt journal = %d rows, %d with logs; want %d rows and no logs (clearing must not journal)", attemptsAfter, logsAfter, attemptsBefore)
	}
	var status string
	var exitCode int
	if err := pool.QueryRow(ctx, `SELECT status, exit_code FROM job_task_attempts WHERE run_id = $1 AND task_index = 0 AND attempt = 1`,
		expired).Scan(&status, &exitCode); err != nil || status != "failed" || exitCode != 1 {
		t.Fatalf("first attempt = %s/%d (%v), want failed/1 kept", status, exitCode, err)
	}
	var aggregate string
	var marked bool
	if err := pool.QueryRow(ctx, `SELECT aggregate_status, logs_cleared_finished_at = finished_at FROM job_runs WHERE id = $1`,
		expired).Scan(&aggregate, &marked); err != nil || aggregate != "succeeded" || !marked {
		t.Fatalf("expired run = %s marked=%v (%v), want succeeded and marked cleared", aggregate, marked, err)
	}
	if got := jobLogTestTasks(t, ctx, pool, retained); len(got) != 1 || got[0].log != "task 0 output\n" {
		t.Fatalf("retained run lost its output: %+v", got)
	}

	if again, err := RetentionOnceJobLogs(ctx, store); err != nil || again != (JobLogsCleared{}) {
		t.Fatalf("second pass = %+v, %v; want nothing left to clear", again, err)
	}
}

// adr: 20261102183152
func TestJobLogRetentionFollowsAccountPlan(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	type runCase struct {
		plan api.Plan
		age  time.Duration
		keep bool
	}
	day := 24 * time.Hour
	cases := []runCase{
		{api.PlanFree, 12 * time.Hour, true},
		{api.PlanFree, 2 * day, false},
		{api.PlanHobby, 6 * day, true},
		{api.PlanHobby, 8 * day, false},
		{api.PlanPro, 29 * day, true},
		{api.PlanPro, 31 * day, false},
		{api.PlanScale, 89 * day, true},
		{api.PlanScale, 91 * day, false},
	}
	accounts := map[api.Plan]string{}
	runs := make([]string, len(cases))
	for i, tc := range cases {
		if accounts[tc.plan] == "" {
			accounts[tc.plan] = jobLogTestAccount(t, ctx, store, tc.plan)
		}
		if want := tc.plan.LogArchiveRetentionDaysMax(); (time.Duration(want)*day > tc.age) != tc.keep {
			t.Fatalf("case %d contradicts %s's %d-day log retention", i, tc.plan, want)
		}
		runs[i] = jobLogTestRun(t, ctx, pool, accounts[tc.plan], 1, tc.age)
	}
	// A run still in flight keeps the output of a task that finished long ago.
	inFlight := jobLogTestRun(t, ctx, pool, accounts[api.PlanFree], 2, 40*day)
	if _, err := pool.Exec(ctx, `UPDATE job_runs SET aggregate_status = 'running', finished_at = NULL, tasks_succeeded = 1 WHERE id = $1`, inFlight); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE job_tasks SET status = 'queued', attempt = attempt + 1, finished_at = NULL, exit_code = NULL, log_content = '' WHERE run_id = $1 AND task_index = 1`, inFlight); err != nil {
		t.Fatal(err)
	}

	cleared, err := RetentionOnceJobLogs(ctx, store)
	if err != nil || cleared.Runs != 4 {
		t.Fatalf("cleared = %+v, %v; want the 4 runs past their plan's window", cleared, err)
	}
	for i, tc := range cases {
		got := jobLogTestTasks(t, ctx, pool, runs[i])
		if kept := got[0].log != ""; kept != tc.keep {
			t.Errorf("%s run finished %s ago: output kept=%v, want %v", tc.plan, tc.age, kept, tc.keep)
		}
	}
	if got := jobLogTestTasks(t, ctx, pool, inFlight); got[0].log != "task 0 output\n" {
		t.Fatalf("unfinished run lost a finished task's output: %+v", got)
	}
}

// adr: 20261102183152
func TestJobLogRetentionClearsARetriedRunAgain(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account := jobLogTestAccount(t, ctx, store, api.PlanHobby)
	run := jobLogTestRun(t, ctx, pool, account, 1, 10*24*time.Hour)
	if cleared, err := RetentionOnceJobLogs(ctx, store); err != nil || cleared.Runs != 1 {
		t.Fatalf("first pass = %+v, %v", cleared, err)
	}
	// A retry reopens the run; while it runs nothing is due.
	if _, err := pool.Exec(ctx, `UPDATE job_runs SET aggregate_status = 'running', finished_at = NULL, tasks_succeeded = 0 WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE job_tasks SET status = 'queued', attempt = attempt + 1, finished_at = NULL, exit_code = NULL, log_content = '', log_truncated = false WHERE run_id = $1`, run); err != nil {
		t.Fatal(err)
	}
	if cleared, err := RetentionOnceJobLogs(ctx, store); err != nil || cleared != (JobLogsCleared{}) {
		t.Fatalf("pass over a reopened run = %+v, %v; want nothing", cleared, err)
	}
	finishJobLogTestTask(t, ctx, pool, run, 0, "succeeded", 0, "retried output\n", 8*24*time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE job_runs SET aggregate_status = 'succeeded', finished_at = now() - interval '8 days', tasks_succeeded = 1 WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
	if total, withLogs := jobLogTestAttempts(t, ctx, pool, run); total != 3 || withLogs != 1 {
		t.Fatalf("journal after the retry = %d rows, %d with logs; want 3 and 1", total, withLogs)
	}
	cleared, err := RetentionOnceJobLogs(ctx, store)
	if err != nil || cleared != (JobLogsCleared{Runs: 1, Tasks: 1, Attempts: 1}) {
		t.Fatalf("pass over the finished retry = %+v, %v; want its new output cleared", cleared, err)
	}
	if _, withLogs := jobLogTestAttempts(t, ctx, pool, run); withLogs != 0 {
		t.Fatalf("retry attempt kept its output")
	}
}

// adr: 20261102183152
func TestJobLogRetentionBatchesWholeRunsByTaskBudget(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account := jobLogTestAccount(t, ctx, store, api.PlanFree)
	for i := range 3 {
		jobLogTestRun(t, ctx, pool, account, 2, time.Duration(48+i)*time.Hour)
	}
	windows, floor := JobLogRetentionWindows()
	batch := state.JobLogRetentionBatch{Windows: windows, FloorDays: floor, MaxRuns: 10, MaxTasks: 3}
	// Runs come oldest first. The second run starts below the budget, so it
	// is taken whole; the third would start past it.
	got, err := store.ClearExpiredJobLogs(ctx, batch)
	if err != nil || got.Runs != 2 || got.Tasks != 4 || !got.More {
		t.Fatalf("first batch = %+v, %v; want two whole runs and more to come", got, err)
	}
	got, err = store.ClearExpiredJobLogs(ctx, batch)
	if err != nil || got.Runs != 1 || got.Tasks != 2 || got.More {
		t.Fatalf("second batch = %+v, %v; want the last run and nothing more", got, err)
	}
	batch.MaxTasks = 1
	got, err = store.ClearExpiredJobLogs(ctx, batch)
	if err != nil || got != (state.JobLogRetentionResult{}) {
		t.Fatalf("batch after the sweep = %+v, %v; want nothing", got, err)
	}
}
