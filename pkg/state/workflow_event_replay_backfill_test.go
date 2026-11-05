package state_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgWorkflowReplayBackfillPreservesLargeNumbers(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@workflow-replay.example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "replay-numeric-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	createWorkflowBackfillDeployment(t, store, app, `[{"name":"paid","trigger":{"type":"event","source":"billing.stripe","event_type":"invoice.paid","filter":{"data":{"amount":{"$eq":9007199254740993}}}},"steps":[{"name":"main","path":"/paid"}]}]`)
	job, err := store.CreateWorkflowEventReplayBackfill(ctx, account.ID, state.WorkflowEventReplayBackfillQuery{AppID: app.ID, WorkflowEventReplayBackfillRequest: api.WorkflowEventReplayBackfillRequest{WorkflowName: "paid", From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err := pool.QueryRow(ctx, `SELECT recipient::text FROM event_replay_jobs WHERE id=$1`, job.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, "9007199254740993") || strings.Contains(snapshot, "9007199254740992") {
		t.Fatalf("large number changed in replay snapshot: %s", snapshot)
	}
}

func TestPgWorkflowReplayPersistsPauseBeforeItemWriteFailure(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@workflow-pause.example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "replay-pause-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	createWorkflowBackfillDeployment(t, store, app, `[]`)
	publishWorkflowBackfillEvent(t, store, pool, app, "before-policy", 150, true)
	createWorkflowBackfillDeployment(t, store, app, `[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid"}]}]`)
	_, err = store.CreateWorkflowEventReplayBackfill(ctx, account.ID, state.WorkflowEventReplayBackfillQuery{AppID: app.ID, WorkflowEventReplayBackfillRequest: api.WorkflowEventReplayBackfillRequest{WorkflowName: "paid", From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := store.SetAutomationFailurePolicy(ctx, app.ID, "paid", api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 1, MinCompletedRuns: 1, WindowSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "paid", DefinitionSnapshot: json.RawMessage(`{"name":"paid","steps":[{"name":"main","path":"/paid"}]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_replay_item_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected replay item failure'; END $$;
		CREATE TRIGGER reject_replay_item_test BEFORE INSERT ON event_replay_job_items FOR EACH ROW EXECUTE FUNCTION reject_replay_item_test()`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ProcessNextEventReplayBackfill(ctx, time.Now().UTC()); err != nil || !worked {
		t.Fatalf("latch pass: worked=%t err=%v", worked, err)
	}
	if _, err := store.ProcessNextEventReplayBackfill(ctx, time.Now().UTC()); err == nil {
		t.Fatal("expected injected item-write failure")
	}
	policy, err := store.GetAutomationFailurePolicy(ctx, app.ID, "paid")
	if err != nil || !policy.Paused || policy.Generation != 1 {
		t.Fatalf("failure pause lost: %+v %v", policy, err)
	}
}

func TestPgWorkflowReplayRetryBoundsExpiredItemCleanup(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@workflow-expired.example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "replay-expired-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	createWorkflowBackfillDeployment(t, store, app, `[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid"}]}]`)
	job, err := store.CreateWorkflowEventReplayBackfill(ctx, account.ID, state.WorkflowEventReplayBackfillQuery{AppID: app.ID, WorkflowEventReplayBackfillRequest: api.WorkflowEventReplayBackfillRequest{WorkflowName: "paid", From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_replay_job_items(job_id,outbox_id,accepted_at,event_source,event_id,event_type,schema_version,state,attempts,failure_code,last_error,retryable)
		SELECT $1,900000000+n,clock_timestamp()+n*interval '1 microsecond','billing.us','expired-'||n,'invoice.paid','','failed',1,'target_unavailable','temporary',true FROM generate_series(1,4) n`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE event_replay_jobs SET state='completed_with_failures',scan_complete=true,completed_at=clock_timestamp() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	result, err := store.RetryFailedEventReplayBackfill(ctx, account.ID, job.ID, 2)
	if err != nil || result.RemainingRetryableCount != 2 {
		t.Fatalf("bounded cleanup: %+v %v", result, err)
	}
}

func TestPgWorkflowReplayRetriesAdvancePastUnavailableItem(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@workflow-retry-fair.example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "retry-fair-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	createWorkflowBackfillDeployment(t, store, app, `[]`)
	event := publishWorkflowBackfillEvent(t, store, pool, app, "still-retained", 150, true)
	createWorkflowBackfillDeployment(t, store, app, `[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid"}]}]`)
	job, err := store.CreateWorkflowEventReplayBackfill(ctx, account.ID, state.WorkflowEventReplayBackfillQuery{AppID: app.ID, WorkflowEventReplayBackfillRequest: api.WorkflowEventReplayBackfillRequest{WorkflowName: "paid", From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO event_replay_job_items(job_id,outbox_id,accepted_at,event_source,event_id,event_type,schema_version,state,attempts,failure_code,last_error,retryable)
		SELECT $1,id,created_at,source,event_id,event_type,coalesce(schema_version,''),'failed',1,'target_unavailable','temporary',true FROM event_fanout_outbox WHERE id=$2`, job.ID, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO event_replay_job_items(job_id,outbox_id,accepted_at,event_source,event_id,event_type,schema_version,state,attempts,failure_code,last_error,retryable)
		VALUES ($1,900000001,clock_timestamp()+interval '1 second','billing.us','expired','invoice.paid','','failed',1,'target_unavailable','temporary',true)`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE event_replay_jobs SET state='completed_with_failures',scan_complete=true,completed_at=clock_timestamp() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE apps SET maintenance_mode=true WHERE id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RetryFailedEventReplayBackfill(ctx, account.ID, job.ID, 1); err != nil {
		t.Fatal(err)
	}
	result, err := store.RetryFailedEventReplayBackfill(ctx, account.ID, job.ID, 1)
	if err != nil || result.RemainingRetryableCount != 1 {
		t.Fatalf("expired item starved: %+v %v", result, err)
	}
}

func TestPgWorkflowEventReplayBackfillPinsDefinitionAndDeduplicatesAcrossJobs(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@workflow-backfill.example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workflow-backfill-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:workflow-backfill-base",
		Status: state.DeployPending, Scope: "default", Workflows: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, baseline.ID); err != nil {
		t.Fatal(err)
	}

	old := publishWorkflowBackfillEvent(t, store, pool, app, "old-to-backfill", 150, true)
	firstDefinition := `[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","filter":{"data":{"amount":{"$gt":100}}}},"steps":[{"name":"main","path":"/paid"}]}]`
	firstDeployment := createWorkflowBackfillDeployment(t, store, app, firstDefinition)
	captured := publishWorkflowBackfillEvent(t, store, pool, app, "already-captured", 175, true)
	unknown := publishWorkflowBackfillEvent(t, store, pool, app, "legacy-unknown", 180, true)
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET recipient_snapshot=NULL WHERE id=$1", unknown.ID); err != nil {
		t.Fatal(err)
	}
	filtered := publishWorkflowBackfillEvent(t, store, pool, app, "filter-mismatch", 10, true)
	unsettled := publishWorkflowBackfillEvent(t, store, pool, app, "still-pending", 190, false)
	var originalSnapshot []byte
	if err := pool.QueryRow(ctx, "SELECT recipient_snapshot FROM event_fanout_outbox WHERE id=$1", old.ID).Scan(&originalSnapshot); err != nil {
		t.Fatal(err)
	}
	var unsettledState string
	if err := pool.QueryRow(ctx, "SELECT state FROM event_fanout_outbox WHERE id=$1", unsettled.ID).Scan(&unsettledState); err != nil || unsettledState == "delivered" {
		t.Fatalf("unsettled state=%q err=%v", unsettledState, err)
	}
	if captured.ID == old.ID || filtered.ID == unknown.ID {
		t.Fatal("test event IDs were not unique")
	}

	from := time.Now().UTC().Add(-time.Hour)
	until := time.Now().UTC().Add(time.Minute)
	job, err := store.CreateWorkflowEventReplayBackfill(ctx, account.ID, state.WorkflowEventReplayBackfillQuery{
		AppID: app.ID, WorkflowEventReplayBackfillRequest: api.WorkflowEventReplayBackfillRequest{
			WorkflowName: "paid", From: from, Until: until,
		},
	})
	if err != nil || job.ConsumerKind != "workflow" || job.WorkflowName != "paid" || job.WorkflowRevision == "" || job.SubscriptionID != "" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	// The job owns its definition. A later live deployment changes the path,
	// but must not change which workflow body historical admission executes.
	secondDefinition := strings.Replace(firstDefinition, `"/paid"`, `"/replacement"`, 1)
	secondDeployment := createWorkflowBackfillDeployment(t, store, app, secondDefinition)
	if secondDeployment.ID == firstDeployment.ID {
		t.Fatal("replacement deployment did not advance")
	}
	job = runWorkflowBackfillToCompletion(t, store, account.ID, job.ID)
	if job.State != "completed" || !job.ScanComplete || job.Progress.Enqueued != 1 || job.Progress.Matched != 4 ||
		job.Progress.Filtered != 1 || job.Progress.SkippedCaptured != 1 || job.Progress.SkippedUnknown != 1 || job.Progress.SkippedUnsettled != 1 {
		t.Fatalf("job progress=%+v", job)
	}
	items, err := store.ListEventReplayBackfillItems(ctx, account.ID, job.ID, api.EventReplayBackfillItemsQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]api.EventReplayBackfillItem{}
	for _, item := range items.Items {
		states[item.EventID] = item
	}
	if got := states["old-to-backfill"]; got.State != "enqueued" || got.WorkflowRunID == "" {
		t.Fatalf("backfilled item=%+v", got)
	}
	if got := states["already-captured"]; got.State != "skipped_captured" {
		t.Fatalf("captured item=%+v", got)
	}
	if got := states["legacy-unknown"]; got.State != "skipped_unknown" {
		t.Fatalf("unknown item=%+v", got)
	}
	if got := states["still-pending"]; got.State != "skipped_unsettled" {
		t.Fatalf("unsettled item=%+v", got)
	}
	if got := states["filter-mismatch"]; got.State != "filtered" {
		t.Fatalf("filtered item=%+v", got)
	}
	run, err := store.GetWorkflowRun(ctx, states["old-to-backfill"].WorkflowRunID)
	if err != nil || !strings.Contains(string(run.DefinitionSnapshot), "/paid") || strings.Contains(string(run.DefinitionSnapshot), "/replacement") {
		t.Fatalf("run did not use the pinned definition: run=%+v err=%v", run, err)
	}
	var afterSnapshot []byte
	if err := pool.QueryRow(ctx, "SELECT recipient_snapshot FROM event_fanout_outbox WHERE id=$1", old.ID).Scan(&afterSnapshot); err != nil {
		t.Fatal(err)
	}
	if string(originalSnapshot) != string(afterSnapshot) {
		t.Fatalf("backfill mutated the original snapshot: before=%s after=%s", originalSnapshot, afterSnapshot)
	}

	second, err := store.CreateWorkflowEventReplayBackfill(ctx, account.ID, state.WorkflowEventReplayBackfillQuery{
		AppID: app.ID, WorkflowEventReplayBackfillRequest: api.WorkflowEventReplayBackfillRequest{
			WorkflowName: "paid", From: from, Until: until,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second = runWorkflowBackfillToCompletion(t, store, account.ID, second.ID)
	secondItems, err := store.ListEventReplayBackfillItems(ctx, account.ID, second.ID, api.EventReplayBackfillItemsQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range secondItems.Items {
		if item.EventID == "old-to-backfill" && item.State != "skipped_existing" {
			t.Fatalf("second job did not deduplicate admission: %+v", item)
		}
	}
	if len(secondItems.Items) == 0 {
		t.Fatal("second backfill returned no outcomes")
	}
}

func createWorkflowBackfillDeployment(t *testing.T, store *state.PgStore, app state.App, definitions string) state.Deployment {
	t.Helper()
	deployment, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:workflow-backfill-" + uuid.NewString(),
		Status: state.DeployPending, Scope: "default", Workflows: json.RawMessage(definitions),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(context.Background(), deployment.ID); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func publishWorkflowBackfillEvent(t *testing.T, store *state.PgStore, pool *pgxpool.Pool, app state.App, id string, amount int, settle bool) state.PublishedEventWork {
	t.Helper()
	publishReplayPreviewEvent(t, store, app.AccountID, id, "billing.us", amount)
	if !settle {
		var work state.PublishedEventWork
		if err := pool.QueryRow(context.Background(), "SELECT id FROM event_fanout_outbox WHERE account_id=$1 AND event_id=$2", app.AccountID, id).Scan(&work.ID); err != nil {
			t.Fatal(err)
		}
		return work
	}
	work, err := store.ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishedEvent(context.Background(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	return *work
}

func runWorkflowBackfillToCompletion(t *testing.T, store *state.PgStore, accountID, jobID string) api.EventReplayBackfillJobResponse {
	t.Helper()
	for range 100 {
		job, err := store.GetEventReplayBackfill(context.Background(), accountID, jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.ScanComplete {
			return job
		}
		if _, err := store.ProcessNextEventReplayBackfill(context.Background(), time.Now().UTC()); err != nil {
			t.Fatalf("process job: %v", err)
		}
	}
	t.Fatalf("backfill %s did not finish scanning", jobID)
	return api.EventReplayBackfillJobResponse{}
}
