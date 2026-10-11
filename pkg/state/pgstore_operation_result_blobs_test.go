// adr: 521
package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func resultBlobUUID(id string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true}
}

func resultBlobTime(now time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: now, Valid: true}
}

func resultBlobParams(accountID, operationID string, size int64, expires time.Time) sqlc.InsertCustomerOperationBlobParams {
	id := uuid.NewString()
	return sqlc.InsertCustomerOperationBlobParams{
		ID: resultBlobUUID(id), AccountID: resultBlobUUID(accountID), OperationID: resultBlobUUID(operationID),
		Generation: 1, ExecutionID: resultBlobUUID(uuid.NewString()), Attempt: 1, ReportID: id,
		Fingerprint: strings.Repeat("a", 64), StorageKey: "operation-results/" + accountID + "/" + operationID + "/" + id,
		SizeBytes: size, ExpiresAt: resultBlobTime(expires),
	}
}

func TestPgOperationResultBlobCleanupFences(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	q := sqlc.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	blob := resultBlobParams(uuid.NewString(), uuid.NewString(), 3, now.Add(time.Minute))
	if err := q.InsertCustomerOperationBlob(ctx, pool, blob); err != nil {
		t.Fatal(err)
	}
	claim := func(token string, at time.Time) (sqlc.CustomerOperationResultBlob, error) {
		return q.ClaimCustomerOperationBlobCleanup(ctx, pool, sqlc.ClaimCustomerOperationBlobCleanupParams{
			LeaseToken: token, Now: resultBlobTime(at), LeaseUntil: resultBlobTime(at.Add(time.Minute)),
		})
	}
	if _, err := claim("early", now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("live staging bytes collected: %v", err)
	}
	if n, err := q.RetainCustomerOperationBlob(ctx, pool, sqlc.RetainCustomerOperationBlobParams{
		ID: blob.ID, Now: blob.ExpiresAt,
	}); err != nil || n != 0 {
		t.Fatalf("expired upload attached: %d, %v", n, err)
	}
	at := now.Add(2 * time.Minute)
	var wg sync.WaitGroup
	owners := make(chan string, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token := fmt.Sprintf("owner-%d", i)
			got, err := claim(token, at)
			if err == nil {
				if got.ID != blob.ID || got.State != "deleting" {
					t.Errorf("wrong cleanup receipt: %+v", got)
				}
				owners <- token
			} else if !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("claim: %v", err)
			}
		}()
	}
	wg.Wait()
	close(owners)
	if len(owners) != 1 {
		t.Fatalf("concurrent cleanup owners: %d", len(owners))
	}
	owner := <-owners
	complete := func(token string, when time.Time) (int64, error) {
		return q.CompleteCustomerOperationBlobCleanup(ctx, pool, sqlc.CompleteCustomerOperationBlobCleanupParams{
			ID: blob.ID, LeaseToken: token, Now: resultBlobTime(when),
		})
	}
	for _, tc := range []struct {
		token string
		when  time.Time
	}{{"foreign", at}, {owner, at.Add(time.Minute)}} {
		if n, err := complete(tc.token, tc.when); err != nil || n != 0 {
			t.Fatalf("unowned or expired cleanup completed: %d, %v", n, err)
		}
	}
	next := at.Add(2 * time.Minute)
	if n, err := q.RetryCustomerOperationBlobCleanup(ctx, pool, sqlc.RetryCustomerOperationBlobCleanupParams{
		ID: blob.ID, LeaseToken: owner, Now: resultBlobTime(at), NextAttemptAt: resultBlobTime(next),
	}); err != nil || n != 1 {
		t.Fatalf("persist cleanup retry: %d, %v", n, err)
	}
	if _, err := claim("before-retry", next.Add(-time.Microsecond)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("retry backoff lost: %v", err)
	}
	if _, err := claim("restart-owner", next); err != nil {
		t.Fatal(err)
	}
	if n, err := complete(owner, next); err != nil || n != 0 {
		t.Fatalf("previous lease completed a reclaimed receipt: %d, %v", n, err)
	}
	if n, err := complete("restart-owner", next); err != nil || n != 1 {
		t.Fatalf("owned cleanup completion: %d, %v", n, err)
	}
	if _, err := q.CustomerOperationBlobByKey(ctx, pool, blob.StorageKey); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("completed cleanup left a receipt: %v", err)
	}
}

func TestPgOperationResultBlobAccountingSurvivesOwnerDeletion(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "result-blob-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, size := range []int64{0, 5} {
		blob := resultBlobParams(acct.ID, uuid.NewString(), size, now.Add(-time.Minute))
		if err := q.InsertCustomerOperationBlob(ctx, pool, blob); err != nil {
			t.Fatal(err)
		}
	}
	checkUsage := func(count, bytes int64) {
		t.Helper()
		got, err := q.CustomerOperationBlobUsage(ctx, pool, resultBlobUUID(acct.ID))
		if err != nil || got.BlobCount != count || got.Bytes != bytes {
			t.Fatalf("reserved storage: %+v, %v; want %d receipts, %d bytes", got, err, count, bytes)
		}
	}
	checkUsage(2, 5)
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountDeletedPending); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	checkUsage(2, 5)
	for i := range 2 {
		blob, err := q.ClaimCustomerOperationBlobCleanup(ctx, pool, sqlc.ClaimCustomerOperationBlobCleanupParams{
			LeaseToken: "deleted-owner", Now: resultBlobTime(now), LeaseUntil: resultBlobTime(now.Add(time.Minute)),
		})
		if err != nil || blob.AccountID != resultBlobUUID(acct.ID) {
			t.Fatalf("owner deletion lost cleanup: %+v, %v", blob, err)
		}
		if i == 0 {
			checkUsage(2, 5)
		}
		if n, err := q.CompleteCustomerOperationBlobCleanup(ctx, pool, sqlc.CompleteCustomerOperationBlobCleanupParams{
			ID: blob.ID, LeaseToken: blob.LeaseToken, Now: resultBlobTime(now),
		}); err != nil || n != 1 {
			t.Fatalf("delete retained receipt: %d, %v", n, err)
		}
	}
	checkUsage(0, 0)
}

func TestPgOperationResultBlobRetentionPins(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		expires     time.Duration
		bound       bool
		protected   bool
	}{
		{"active-past-result-horizon", "running", -time.Hour, true, true},
		{"completed-within-result-horizon", "succeeded", time.Hour, true, true},
		{"completed-expired-result", "succeeded", -time.Hour, true, false},
		{"losing-copy-not-bound", "running", time.Hour, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, pool, ctx := pgStoreWithPool(t)
			account, app, deployment := seedLiveDeploy(t, s, ctx)
			tenant, _, err := s.CreatePlatformTenant(ctx, account, "result-owner", "Result owner", 10)
			if err != nil {
				t.Fatal(err)
			}
			inv, err := s.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/exports"})
			if err != nil {
				t.Fatal(err)
			}
			q := sqlc.New()
			definition := uuid.NewString()
			if _, err := q.InsertCustomerOperationDefinition(ctx, pool, sqlc.InsertCustomerOperationDefinitionParams{
				ID: resultBlobUUID(definition), AccountID: resultBlobUUID(account), AppID: resultBlobUUID(app),
				DeploymentID: resultBlobUUID(deployment), Scope: "prod", Name: "export", Revision: strings.Repeat("b", 64), Spec: []byte(`{"name":"export"}`),
			}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			operation := uuid.NewString()
			blob := resultBlobParams(account, operation, 3, now.Add(time.Minute))
			blob.ExecutionID = resultBlobUUID(inv.ID)
			keys := map[string]string{}
			if tc.bound {
				keys["export.csv"] = blob.StorageKey
			}
			record, err := json.Marshal(map[string]any{
				"id": operation, "account_id": account, "app_id": app, "platform_tenant_id": tenant.ID,
				"definition_id": definition, "current_invocation_id": inv.ID, "state": tc.state, "artifact_storage_keys": keys,
				"generation": 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if err := q.InsertCustomerOperation(ctx, tx, sqlc.InsertCustomerOperationParams{
				ID: resultBlobUUID(operation), AccountID: resultBlobUUID(account), AppID: resultBlobUUID(app), TenantID: resultBlobUUID(tenant.ID),
				DefinitionID: resultBlobUUID(definition), InvocationID: resultBlobUUID(inv.ID), State: tc.state, Record: record,
				ExpiresAt: resultBlobTime(now.Add(tc.expires)), CreatedAt: resultBlobTime(now),
			}); err != nil {
				t.Fatal(err)
			}
			if err := q.SetCustomerOperationExecutionIdentity(ctx, tx, sqlc.SetCustomerOperationExecutionIdentityParams{
				OperationID: resultBlobUUID(operation), InvocationID: resultBlobUUID(inv.ID),
			}); err != nil {
				t.Fatal(err)
			}
			if err := q.InsertCustomerOperationExecution(ctx, tx, sqlc.InsertCustomerOperationExecutionParams{
				OperationID: resultBlobUUID(operation), InvocationID: resultBlobUUID(inv.ID), Generation: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := q.InsertCustomerOperationBlob(ctx, pool, blob); err != nil {
				t.Fatal(err)
			}
			if n, err := q.RetainCustomerOperationBlob(ctx, pool, sqlc.RetainCustomerOperationBlobParams{ID: blob.ID, Now: resultBlobTime(now)}); err != nil || n != 1 {
				t.Fatalf("attach staged copy: %d, %v", n, err)
			}
			got, err := q.ClaimCustomerOperationBlobCleanup(ctx, pool, sqlc.ClaimCustomerOperationBlobCleanupParams{
				LeaseToken: "retention", Now: resultBlobTime(now.Add(2 * time.Minute)), LeaseUntil: resultBlobTime(now.Add(3 * time.Minute)),
			})
			if tc.protected {
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("collected a bound retained result: %+v, %v", got, err)
				}
			} else if err != nil || got.ID != blob.ID {
				t.Fatalf("orphaned or expired copy is not collectible: %+v, %v", got, err)
			}
		})
	}
}
