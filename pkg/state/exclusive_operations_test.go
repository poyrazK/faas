// adr: 387
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

func TestExclusiveOperationStoreLifecycle(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			var base Store
			var ownerStore ExclusiveWorkStore
			var pool *pgxpool.Pool
			if backend == "postgres" {
				pool = pgtest.OpenMigrated(t)
				if err := db.MigrateUp(ctx, pool); err != nil {
					t.Fatalf("MigrateUp: %v", err)
				}
				store := NewPgStore(pool)
				base, ownerStore = store, store
			} else {
				store := NewMemStoreWithExclusiveClock(func() time.Time { return now })
				base, ownerStore = store, store
			}

			account, err := base.CreateAccount(ctx, uuid.NewString()+"@exclusive-state.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := base.CreateApp(ctx, App{
				AccountID: account.ID, Slug: "exclusive-state-" + uuid.NewString()[:8],
				Type: AppTypeApp, Runtime: "node22", RAMMB: 256,
			})
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := base.CreateDeployment(ctx, Deployment{
				AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:exclusive-state-test", Status: DeployLive,
			})
			if err != nil {
				t.Fatal(err)
			}
			nodeID := "exclusive-state-node"
			if pool != nil {
				node, err := base.(interface {
					ComputeNodeByName(context.Context, string) (ComputeNode, error)
				}).ComputeNodeByName(ctx, DefaultLocalNodeName)
				if err != nil {
					t.Fatal(err)
				}
				nodeID = node.ID
			}
			instance, err := base.CreateInstance(ctx, app.ID, deployment.ID, string(StateRunning), 256, nodeID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			incarnation := ExclusiveIncarnation(instance)

			policy := exclusivework.Policy{
				Name: "state-lifecycle", Scope: "account", MemberAppIDs: []string{app.ID},
				Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60,
				MaxAttempts: 2, RetryAfterSeconds: 2,
			}
			if _, err := ownerStore.UpsertExclusiveWorkPolicy(ctx, account.ID, policy); err != nil {
				t.Fatal(err)
			}
			if _, err := ownerStore.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{Name: "Bad_Name"}); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid policy error = %v, want ErrInvalidArgument", err)
			}
			policies, err := ownerStore.ListExclusiveWorkPolicies(ctx, account.ID)
			if err != nil || len(policies) != 1 || policies[0].Policy.Name != policy.Name {
				t.Fatalf("policies = %+v, err = %v", policies, err)
			}

			admit := func(key, idempotencyKey, request string) (ExclusiveOperation, error) {
				keyJSON, _ := json.Marshal(key)
				op, _, err := ownerStore.AdmitExclusiveOperation(ctx, ExclusiveAdmission{
					AccountID: account.ID, AppID: app.ID, PolicyName: policy.Name,
					Key:     keyJSON,
					Request: json.RawMessage(request), IdempotencyKey: idempotencyKey,
				})
				return op, err
			}
			first, err := admit("lane-1", "first-request", `{"kind":"sync","version":1}`)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := admit("lane-1", "first-request", `{"version":1,"kind":"sync"}`)
			if err != nil || !replay.Replayed || replay.ID != first.ID {
				t.Fatalf("idempotent replay = %+v, err = %v", replay, err)
			}
			if _, err := admit("lane-1", "first-request", `{"kind":"sync","version":2}`); !errors.Is(err, exclusivework.ErrIdentityConflict) {
				t.Fatalf("changed idempotent request error = %v, want identity conflict", err)
			}
			if _, _, err := ownerStore.AdmitExclusiveOperation(ctx, ExclusiveAdmission{
				AccountID: account.ID, AppID: app.ID, PolicyName: policy.Name,
				Key: json.RawMessage(`"customer:acme:crm-sync"`), Request: json.RawMessage(`[]`),
			}); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid request error = %v, want ErrInvalidArgument", err)
			}

			firstOwner, err := ownerStore.ClaimExclusiveOperation(ctx, account.ID, first.ID, incarnation)
			if err != nil {
				t.Fatal(err)
			}
			if validated, err := ownerStore.ValidateExclusiveOperation(ctx, firstOwner); err != nil || validated.ID != first.ID {
				t.Fatalf("validate owner = %+v, err = %v", validated, err)
			}
			firstOwner, err = ownerStore.RenewExclusiveOperation(ctx, firstOwner)
			if err != nil {
				t.Fatal(err)
			}
			if err := ownerStore.CommitExclusiveOperation(ctx, firstOwner, json.RawMessage(`{"synced":true}`), []exclusivework.Effect{{
				Name: "sync-completed", Payload: json.RawMessage(`{"customer":"acme"}`),
			}}); err != nil {
				t.Fatal(err)
			}
			completed, err := ownerStore.ExclusiveOperationByID(ctx, account.ID, first.ID)
			var result map[string]any
			if err == nil {
				err = json.Unmarshal(completed.Result, &result)
			}
			if err != nil || completed.State != "completed" || result["synced"] != true {
				t.Fatalf("completed operation = %+v, err = %v", completed, err)
			}

			second, err := admit("lane-1", "second-request", `{"kind":"sync","version":2}`)
			if err != nil {
				t.Fatal(err)
			}
			secondOwner, err := ownerStore.ClaimExclusiveOperation(ctx, account.ID, second.ID, incarnation)
			if err != nil {
				t.Fatal(err)
			}
			if secondOwner.Generation <= firstOwner.Generation {
				t.Fatalf("second generation = %d, first generation = %d", secondOwner.Generation, firstOwner.Generation)
			}
			longReason := strings.Repeat("🙂", api.MaxExclusiveErrorBytes/4+1)
			if err := ownerStore.RetryExclusiveOperation(ctx, secondOwner, longReason); err != nil {
				t.Fatal(err)
			}
			retried, err := ownerStore.ExclusiveOperationByID(ctx, account.ID, second.ID)
			if err != nil || len(retried.LastError) > api.MaxExclusiveErrorBytes || !utf8.ValidString(retried.LastError) {
				t.Fatalf("retry error text bytes=%d valid=%v err=%v", len(retried.LastError), utf8.ValidString(retried.LastError), err)
			}
			if _, err := ownerStore.RenewExclusiveOperation(ctx, secondOwner); !errors.Is(err, exclusivework.ErrStaleOwner) {
				t.Fatalf("renew after retry = %v, want stale owner", err)
			}
			if _, err := ownerStore.ClaimExclusiveOperation(ctx, account.ID, second.ID, incarnation); !errors.Is(err, exclusivework.ErrBusy) {
				t.Fatalf("claim before retry due time = %v, want busy", err)
			}
			if pool == nil {
				now = now.Add(3 * time.Second)
			} else if _, err := pool.Exec(ctx, `update exclusive_work_operations set due_at=clock_timestamp()-interval '1 second' where id=$1`, second.ID); err != nil {
				t.Fatal(err)
			}
			due, err := ownerStore.ListDueExclusiveOperations(ctx, 100)
			if err != nil || len(due) != 1 || due[0].ID != second.ID {
				t.Fatalf("due operations = %+v, err = %v", due, err)
			}
			retryOwner, err := ownerStore.ClaimExclusiveOperation(ctx, account.ID, second.ID, incarnation)
			if err != nil || retryOwner.Generation <= secondOwner.Generation {
				t.Fatalf("retry owner = %+v, err = %v", retryOwner, err)
			}
			if err := ownerStore.FailExclusiveOperation(ctx, retryOwner, "permanent provider failure"); err != nil {
				t.Fatal(err)
			}

			pending, err := admit("lane-2", "pending-request", `{"kind":"sync","version":3}`)
			if err != nil {
				t.Fatal(err)
			}
			if err := ownerStore.DeferPendingExclusiveOperation(ctx, account.ID, pending.ID, longReason); err != nil {
				t.Fatal(err)
			}
			deferred, err := ownerStore.ExclusiveOperationByID(ctx, account.ID, pending.ID)
			if err != nil || len(deferred.LastError) > api.MaxExclusiveErrorBytes || !utf8.ValidString(deferred.LastError) {
				t.Fatalf("deferred error text bytes=%d valid=%v err=%v", len(deferred.LastError), utf8.ValidString(deferred.LastError), err)
			}
			if err := ownerStore.CancelExclusiveOperation(ctx, account.ID, pending.ID); err != nil {
				t.Fatal(err)
			}
			cancelled, err := ownerStore.ExclusiveOperationByID(ctx, account.ID, pending.ID)
			if err != nil || cancelled.State != "cancelled" {
				t.Fatalf("cancelled operation = %+v, err = %v", cancelled, err)
			}

			failedPending, err := admit("lane-3", "failed-request", `{"kind":"sync","version":4}`)
			if err != nil {
				t.Fatal(err)
			}
			if err := ownerStore.FailPendingExclusiveOperation(ctx, account.ID, failedPending.ID, longReason); err != nil {
				t.Fatal(err)
			}
			failed, err := ownerStore.ExclusiveOperationByID(ctx, account.ID, failedPending.ID)
			if err != nil || failed.State != "failed" || len(failed.LastError) > api.MaxExclusiveErrorBytes || !utf8.ValidString(failed.LastError) {
				t.Fatalf("failed operation error text bytes=%d valid=%v state=%q err=%v", len(failed.LastError), utf8.ValidString(failed.LastError), failed.State, err)
			}
		})
	}
}
