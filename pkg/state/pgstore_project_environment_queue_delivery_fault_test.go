//go:build !no_pg

// adr: 566
package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgEnvironmentQueueDeliveryRollsBackClaimAndFinish(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	f, req := seedQueueDelivery(t.Context(), t, s, state.WorkloadClassWorker, "pull")
	inv := enqueueStageQueue(t.Context(), t, s, f)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM project_environments WHERE id=$1 FOR UPDATE`, f.spec.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = s.ClaimNextProjectEnvironmentQueueDelivery(blocked, req)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claim bypassed environment lock: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_queue_receipt() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END; $$ LANGUAGE plpgsql;
        CREATE TRIGGER fail_queue_receipt BEFORE INSERT ON invocation_environment_queue_receipts FOR EACH ROW EXECUTE FUNCTION fail_queue_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); err == nil {
		t.Fatal("receipt failure did not abort claim")
	}
	unchanged, err := s.InvocationByID(ctx, inv.ID)
	if err != nil || unchanged.State != state.InvocationPending || unchanged.Attempts != 0 || unchanged.QuotaReserved || unchanged.LeaseExpiresAt != nil {
		t.Fatalf("partial claim: %+v %v", unchanged, err)
	}
	if _, _, err := s.GetAccountAsyncQuota(ctx, f.account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed claim retained quota: %v", err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invocation_environment_queue_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("partial receipt: %d %v", receipts, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_queue_receipt ON invocation_environment_queue_receipts`); err != nil {
		t.Fatal(err)
	}
	d, err := s.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var tokenHash string
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM invocation_environment_queue_receipts WHERE invocation_id=$1`, inv.ID).Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(d.Receipt))
	if tokenHash == d.Receipt || tokenHash != hex.EncodeToString(digest[:]) {
		t.Fatal("plaintext or invalid receipt stored")
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_queue_quota_release() RETURNS trigger AS $$ BEGIN
        IF NEW.current_inflight < OLD.current_inflight THEN RAISE EXCEPTION 'injected release failure'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql;
        CREATE TRIGGER fail_queue_quota_release BEFORE UPDATE ON account_async_quota FOR EACH ROW EXECUTE FUNCTION fail_queue_quota_release()`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, d.Receipt, []byte(`{"ok":true}`)); err == nil {
		t.Fatal("quota release failure did not abort completion")
	}
	unchanged, err = s.InvocationByID(ctx, inv.ID)
	if err != nil || unchanged.State != state.InvocationDispatching || !unchanged.QuotaReserved || unchanged.CompletedAt != nil || len(unchanged.Result) != 0 {
		t.Fatalf("partial completion: %+v %v", unchanged, err)
	}
	_, inflight, err := s.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || inflight != 1 {
		t.Fatalf("partial release: %d %v", inflight, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_queue_quota_release ON account_async_quota`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, d.Receipt, nil); err != nil {
		t.Fatalf("rolled back completion consumed receipt: %v", err)
	}
	if n, err := s.DeleteInvocationsByIDs(ctx, []string{inv.ID}); err != nil || n != 1 {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invocation_environment_queue_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("retention orphaned receipt: %d %v", receipts, err)
	}
}

func TestPgEnvironmentQueueDeliveryDamagedOwnersRemainPrivate(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	f, req := seedQueueDelivery(t.Context(), t, s, state.WorkloadClassJob, "pull")
	inv := enqueueStageQueue(t.Context(), t, s, f)
	d, err := s.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM invocation_environment_queue_admissions WHERE invocation_id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invocations SET environment_id=NULL,headers='{}'::jsonb WHERE id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, d.Receipt, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing owner completed: %v", err)
	}
	if err := s.CompleteInvocation(ctx, inv.ID, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generic completion adopted receipt-only ownership: %v", err)
	}
	if err := s.FailInvocation(ctx, inv.ID, "adopted", time.Second, 1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generic failure adopted receipt-only ownership: %v", err)
	}
	if _, err := s.ProductionQueueInvocationByID(ctx, inv.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production read adopted receipt-only ownership: %v", err)
	}
	if rows, err := s.QueuePeek(ctx, f.app.ID, 1, ""); err != nil || len(rows) != 0 {
		t.Fatalf("production peek exposed damaged delivery: %+v %v", rows, err)
	}
	trigger, err := s.CreateTriggerIfUnderQuota(ctx, f.app.ID, "queue", "orders", true, []byte(`{"mode":"queue"}`), "queue", 1, 20, 3, 8192, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	appID := mustPgUUID(t, f.app.ID)
	for _, action := range []func() error{
		func() error {
			return q.ReleaseProductionNamedQueueClaims(ctx, pool, sqlc.ReleaseProductionNamedQueueClaimsParams{AppID: appID, QueueName: "orders", InvocationIds: []string{inv.ID}, Attempts: []int32{1}})
		},
		func() error {
			return q.RetryProductionQueueTriggerInvocations(ctx, pool, sqlc.RetryProductionQueueTriggerInvocationsParams{TriggerID: trigger.ID, AppID: appID, Source: "queue", InvocationIds: []string{inv.ID}, Attempts: []int32{1}, LastError: "adopted"})
		},
		func() error {
			return q.FinishProductionQueueTriggerInvocations(ctx, pool, sqlc.FinishProductionQueueTriggerInvocationsParams{TriggerID: trigger.ID, AppID: appID, Source: "queue", InvocationIds: []string{inv.ID}, Attempts: []int32{1}, RecordState: "succeeded", InvocationState: "completed"})
		},
	} {
		if err := action(); err != nil {
			t.Fatal(err)
		}
		row, err := s.InvocationByID(ctx, inv.ID)
		if err != nil || row.State != state.InvocationDispatching || !row.QuotaReserved {
			t.Fatalf("production action adopted damaged receipt: %+v %v", row, err)
		}
	}
	raw, err := migrations.FS.ReadFile("20261004095153171_environment_queue_delivery_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err := pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback discarded receipt ownership")
	}
	if err := s.CancelInvocation(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invocations SET state='pending',completed_at=NULL WHERE id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimInvocation(ctx, inv.ID, "", 30); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("bare claim adopted receipt-only ownership: %v", err)
	}
	if _, err := s.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 100); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capped claim adopted receipt-only ownership: %v", err)
	}
	if _, err := s.ClaimQueueTriggerInvocation(ctx, inv.ID, trigger.ID.String(), f.app.ID, "orders", 30); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("named production claim adopted receipt: %v", err)
	}
	if rows, err := q.ListProductionNamedQueueCandidates(ctx, pool, sqlc.ListProductionNamedQueueCandidatesParams{TriggerID: trigger.ID, AppID: appID, QueueName: "orders", CandidateLimit: 1}); err != nil || len(rows) != 0 {
		t.Fatalf("production candidate exposed receipt: %v %v", rows, err)
	}
	if rows, err := q.ClaimProductionLegacyQueueInvocations(ctx, pool, sqlc.ClaimProductionLegacyQueueInvocationsParams{TriggerID: trigger.ID, AppID: appID, Source: "queue", QueueName: "orders", BatchLimit: 1}); err != nil || len(rows) != 0 {
		t.Fatalf("legacy production claim exposed receipt: %v %v", rows, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invocations SET source='async_invoke',queue_name='',due_at=now()-interval '1 hour' WHERE id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	production, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() ([]state.Invocation, error){
		func() ([]state.Invocation, error) { return s.ListDueInvocations(ctx, time.Now(), 1) },
		func() ([]state.Invocation, error) {
			return s.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 1)
		},
	} {
		rows, err := read()
		if err != nil || len(rows) != 1 || rows[0].ID != production.ID {
			t.Fatalf("damaged receipt consumed a generic drain page: %+v %v", rows, err)
		}
	}
	unchanged, err := s.InvocationByID(ctx, inv.ID)
	if err != nil || unchanged.State != state.InvocationPending || unchanged.Attempts != 1 || unchanged.QuotaReserved {
		t.Fatalf("rejected legacy claim mutated owned message: %+v %v", unchanged, err)
	}
}

func TestPgEnvironmentQueueDeliveryMigrationRoundTrip(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	f, _ := seedQueueDelivery(t.Context(), t, s, state.WorkloadClassWorker, "pull")
	inv := enqueueStageQueue(t.Context(), t, s, f)
	raw, err := migrations.FS.ReadFile("20261004095153171_environment_queue_delivery_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration down section")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProductionQueueInvocationByID(ctx, inv.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rollback exposed admitted stage message: %v", err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	coverage, err := s.ProjectEnvironmentCloneSchemaCoverage(ctx, f.account.ID, f.project.ID)
	if err != nil || !coverage.Known {
		t.Fatalf("receipt migration omitted schema policy: %+v %v", coverage.Blockers, err)
	}
}
