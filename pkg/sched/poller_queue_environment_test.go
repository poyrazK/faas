// adr: 375
package sched

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedQueuePollerEnvironment(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-environment@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queue-environment"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-environment-worker",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	return store, pool, app, dep
}

func enqueueStagePollerDelayedTask(t *testing.T, store *state.PgStore, app state.App, dep state.Deployment) state.Invocation {
	t.Helper()
	headers, _ := json.Marshal(map[string]string{api.RevisionHeader: dep.ID})
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{AppID: app.ID, AccountID: app.AccountID,
		Source: state.InvocationDelayedTask, Headers: headers, DueAt: time.Now().Add(-time.Second), Payload: []byte(`{"stage":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestQueuePollerCannotClaimOrAcknowledgeStageWork(t *testing.T) {
	for _, mode := range []string{"delayed_task", "named_queue", "legacy_queue"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store, pool, app, dep := seedQueuePollerEnvironment(t)
			pending := enqueueStagePollerDelayedTask(t, store, app, dep)
			running := enqueueStagePollerDelayedTask(t, store, app, dep)
			if _, err := store.ClaimInvocationWithCap(ctx, running.ID, "", 600, 2); err != nil {
				t.Fatal(err)
			}
			source, queueName := "delayed_task", ""
			if mode != "delayed_task" {
				source = "queue"
				if mode == "named_queue" {
					queueName = "orders"
					if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: app.AccountID, AppID: app.ID,
						Name: queueName, QueueName: queueName, Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
						t.Fatal(err)
					}
				}
				// Simulate retained/legacy stage queue rows as well as the currently
				// supported delayed tasks. Every production callback must fence them.
				if _, err := pool.Exec(ctx, `UPDATE invocations SET source='queue',queue_name=$2 WHERE id::text=ANY($1::text[])`,
					[]string{pending.ID, running.ID}, queueName); err != nil {
					t.Fatal(err)
				}
			}
			trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", "orders", true, []byte(`{"mode":"`+source+`"}`),
				source, 10, 20, 3, 8192, "commit", api.MustLimitsFor(api.PlanPro))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "legacy_queue" {
				trigger.Slug = "" // exercise the retained unnamed queue adapter
			}
			pollerSource, err := newQueuePoller(pool, trigger, nil)
			if err != nil {
				t.Fatal(err)
			}
			poller := pollerSource.(*queuePoller)
			production, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: app.AccountID,
				Source: state.InvocationSource(source), QueueName: queueName, DueAt: time.Now().Add(-time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			result := poller.Poll(ctx, trigger)
			if result.Error != nil || len(result.Records) != 1 || result.Records[0].ItemIdentifier != production.ID {
				t.Fatalf("production poller claimed/throttled on stage work: %+v", result)
			}
			if got, err := store.InvocationByID(ctx, pending.ID); err != nil || got.State != state.InvocationPending || got.Attempts != 0 {
				t.Fatalf("poll mutated stage pending task: %+v, %v", got, err)
			}
			before, err := store.InvocationByID(ctx, running.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"ack", "retry", "dead_letter", "partial_release"} {
				poller.itemsInFlight[running.ID] = before.Attempts
				switch action {
				case "ack":
					err = poller.Ack(ctx, trigger, []string{running.ID})
				case "retry":
					err = poller.Nack(ctx, trigger, []string{running.ID}, "temporary")
				case "dead_letter":
					err = poller.Nack(ctx, trigger, []string{running.ID}, triggerReasonPoisonRecord)
				case "partial_release":
					err = poller.releaseNamedClaims(ctx, map[string]int{running.ID: before.Attempts}, trigger)
				}
				if err != nil {
					t.Fatalf("%s: %v", action, err)
				}
				if after, err := store.InvocationByID(ctx, running.ID); err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("production %s mutated stage lease/quota/state: %+v, %v", action, after, err)
				}
				if _, current, err := store.GetAccountAsyncQuota(ctx, app.AccountID); err != nil || current != 1 {
					t.Fatalf("production %s changed stage capacity: %d, %v", action, current, err)
				}
			}
		})
	}
}

func TestNamedQueuePollerSkipsStageBacklogBeforeCandidateLimit(t *testing.T) {
	ctx := t.Context()
	store, pool, app, dep := seedQueuePollerEnvironment(t)
	stage := enqueueStagePollerDelayedTask(t, store, app, dep)
	if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: app.AccountID, AppID: app.ID,
		Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	// More stage rows than the poller's scan budget must not hide production.
	if _, err := pool.Exec(ctx, `INSERT INTO invocations(app_id,account_id,environment_id,source,queue_name,state,headers,due_at,created_at)
		SELECT app_id,account_id,environment_id,'queue','orders','pending',headers,now()-interval '1 minute',now()-interval '1 minute'
		FROM invocations CROSS JOIN generate_series(1,1100) WHERE id=$1`, stage.ID); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", "orders", true, []byte(`{"mode":"queue"}`),
		"queue", 1, 20, 3, 8192, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: app.AccountID, AppID: app.ID,
		Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	poller, err := newQueuePoller(pool, trigger, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result := poller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 1 || result.Records[0].ItemIdentifier != production.ID {
		t.Fatalf("stage backlog hid production candidates: %+v", result)
	}
}
