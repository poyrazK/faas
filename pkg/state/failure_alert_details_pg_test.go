package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-045 amendment 1: a job_failures rule can count one job and a cron
// failed_invocations rule one cron; the checks keep each filter on its
// metric, and the detail queries name what the counts counted.
func TestPgAlertFailureFiltersAndDetails(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "alert-filter-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "alert-filter-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	// Rules: each filter only on its own metric.
	checkViolation := func(name string, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.Is(err, state.ErrInvalidArgument) && (!errors.As(err, &pgErr) || pgErr.Code != "23514") {
			t.Fatalf("%s: err = %v, want refusal", name, err)
		}
	}
	jobRule := pgSampleAlertRule(account.ID, app.ID)
	jobRule.Metric, jobRule.JobName = state.AlertMetricJobFailures, "nightly"
	created, err := store.CreateAlertRule(ctx, jobRule)
	if err != nil || created.JobName != "nightly" {
		t.Fatalf("job rule = %+v, %v", created, err)
	}
	cron, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/bill", true)
	if err != nil {
		t.Fatal(err)
	}
	cronRule := pgSampleAlertRule(account.ID, app.ID)
	cronRule.Metric, cronRule.FailureSource, cronRule.CronID = state.AlertMetricFailedInvocs, state.AlertFailureCron, cron.ID
	createdCron, err := store.CreateAlertRule(ctx, cronRule)
	if err != nil || createdCron.CronID != cron.ID {
		t.Fatalf("cron rule = %+v, %v", createdCron, err)
	}
	wrongJob := pgSampleAlertRule(account.ID, app.ID)
	wrongJob.JobName = "nightly"
	_, err = store.CreateAlertRule(ctx, wrongJob)
	checkViolation("job filter on error rate", err)
	_, err = pool.Exec(ctx, `update alert_rules set failure_source = 'queue' where id = $1::uuid`, createdCron.ID)
	checkViolation("cron filter on queue source", err)
	_, err = pool.Exec(ctx, `update alert_rules set job_name = $2 where id = $1::uuid`, created.ID, strings.Repeat("j", 129))
	checkViolation("long job name", err)
	// Update changes and clears a filter.
	other, empty := "weekly", ""
	updated, err := store.UpdateAlertRule(ctx, created.ID, state.UpdateAlertRuleParams{JobName: &other})
	if err != nil || updated.JobName != "weekly" {
		t.Fatalf("change job filter = %+v, %v", updated, err)
	}
	updated, err = store.UpdateAlertRule(ctx, created.ID, state.UpdateAlertRuleParams{JobName: &empty})
	if err != nil || updated.JobName != "" {
		t.Fatalf("clear job filter = %+v, %v", updated, err)
	}
	updated, err = store.UpdateAlertRule(ctx, createdCron.ID, state.UpdateAlertRuleParams{Name: &other})
	if err != nil || updated.CronID != cron.ID {
		t.Fatalf("unrelated update kept cron filter = %+v, %v", updated, err)
	}

	// Cron invocations: two failures of this cron, one of another cron,
	// one success, and one before the window.
	otherCron, err := store.CreateCron(ctx, app.ID, "0 * * * *", "/report", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range []struct {
		cron, state, lastErr string
		ago                  time.Duration
	}{
		{cron.ID, "failed", "timeout", time.Minute},
		{cron.ID, "dead_letter", "boom", 2 * time.Minute},
		{otherCron.ID, "failed", "x", time.Minute},
		{cron.ID, "completed", "", time.Minute},
		{cron.ID, "failed", "old", 3 * time.Hour},
	} {
		at := now.Add(-inv.ago)
		if _, err := pool.Exec(ctx, `insert into invocations (account_id, app_id, source, path, state, due_at, created_at, completed_at, deployment_scope, cron_id, attempts, last_error)
			values ($1::uuid, $2::uuid, 'cron', '/bill', $3, $4, $4, $4, '', $5::uuid, 2, $6)`, account.ID, app.ID, inv.state, at, inv.cron, inv.lastErr); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.CountFailedCronInvocations(ctx, account.ID, app.ID, "", cron.ID, now.Add(-time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("cron failures = %d, %v; want 2", n, err)
	}
	invs, err := store.ListFailedInvocations(ctx, account.ID, app.ID, "", state.AlertFailureCron, cron.ID, now.Add(-time.Hour), 5)
	if err != nil || len(invs) != 2 || invs[0].LastError != "timeout" || invs[0].CronID != cron.ID || invs[0].Attempts != 2 || invs[1].LastError != "boom" {
		t.Fatalf("cron failure details = %+v, %v", invs, err)
	}
	if all, err := store.ListFailedInvocations(ctx, account.ID, app.ID, "", state.AlertFailureAny, "", now.Add(-time.Hour), 1); err != nil || len(all) != 1 {
		t.Fatalf("limited details = %+v, %v", all, err)
	}

	// Boot failures: newest first, with deployment and node.
	for i, ago := range []time.Duration{2 * time.Minute, time.Minute} {
		data, _ := json.Marshal(map[string]any{"app_id": app.ID, "reason": "vmm_boot_failed", "deployment_id": "dep-" + string(rune('1'+i)), "node_id": "n1", "method": "cold_boot"})
		if err := store.AppendEventAt(ctx, "schedd", "wake.boot_failed", nil, data, now.Add(-ago)); err != nil {
			t.Fatal(err)
		}
	}
	boots, err := store.ListAppBootFailures(ctx, app.ID, now.Add(-time.Hour), now, 5)
	if err != nil || len(boots) != 2 || boots[0].DeploymentID != "dep-2" || boots[0].NodeID != "n1" || boots[0].Method != "cold_boot" {
		t.Fatalf("boot failure details = %+v, %v", boots, err)
	}

	// Jobs: a failed run of nightly with a task error, a failed run of
	// weekly; the filter counts and lists only nightly's.
	nightly, err := store.JobCreate(ctx, account.ID, "nightly", "batch", "registry.example/job:1", []string{"run"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	weekly, err := store.JobCreate(ctx, account.ID, "weekly", "batch", "registry.example/job:1", []string{"run"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range []state.Job{nightly, weekly} {
		run, _, err := store.JobRunCreate(ctx, job.ID, account.ID, "manual", nil, nil, nil, nil, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update job_tasks set status = 'failed', finished_at = now(), error_message = 'exit 3: missing DATABASE_URL' where run_id = $1::uuid and task_index = 1`, run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update job_runs set aggregate_status = 'failed', tasks_failed = 1, started_at = $2, finished_at = $2 where id = $1::uuid`, run.ID, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := store.CountAccountFailedJobRuns(ctx, account.ID, "nightly", now.Add(-time.Hour), now); err != nil || n != 1 {
		t.Fatalf("nightly failures = %d, %v; want 1", n, err)
	}
	if n, err := store.CountAccountFailedJobRuns(ctx, account.ID, "", now.Add(-time.Hour), now); err != nil || n != 2 {
		t.Fatalf("all job failures = %d, %v; want 2", n, err)
	}
	runs, err := store.ListAccountFailedJobRuns(ctx, account.ID, "nightly", now.Add(-time.Hour), now, 5)
	if err != nil || len(runs) != 1 || runs[0].JobName != "nightly" || runs[0].Tasks != 2 || runs[0].TasksFailed != 1 || runs[0].FirstError != "exit 3: missing DATABASE_URL" {
		t.Fatalf("nightly failure details = %+v, %v", runs, err)
	}
}
