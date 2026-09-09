//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgExecutionParams(t *testing.T, accountID string, admittedAt time.Time, timeoutMS int, payload string) state.CreateExecutionParams {
	t.Helper()
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimePython313,
		Source:  "def main(input, context):\n    return input",
		Input:   []byte(`{"value":42}`),
	}
	if timeoutMS != 0 {
		request.Limits = &api.ExecutionLimitRequest{TimeoutMS: timeoutMS}
	}
	resolved, problem := request.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatalf("resolve execution: %v", problem)
	}
	return state.CreateExecutionParams{
		AccountID: accountID, Request: resolved,
		SourceBytes: len(request.Source), InputBytes: len(request.Input),
		AdmittedAt: admittedAt, DeadlineAt: admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload: []byte(payload), PayloadKID: "pg-execution-key",
	}
}

func pgExecutionAccount(t *testing.T, store *state.PgStore, ctx context.Context, suffix string) state.Account {
	t.Helper()
	account, err := store.CreateAccount(ctx, "pg-execution-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return account
}

func pgExecutionPayloadCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, executionID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from execution_payloads where execution_id=$1::uuid`, executionID).Scan(&count); err != nil {
		t.Fatalf("count execution payload: %v", err)
	}
	return count
}

func TestPgStoreExecutionLifecycleAndAtomicAdmission(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "lifecycle")
	base := time.Now().UTC().Add(time.Second)
	created, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base, 0, "sealed-pg-payload"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if pgExecutionPayloadCount(t, pool, ctx, created.ID) != 1 {
		t.Fatal("create did not atomically persist payload")
	}
	if _, err := store.ExecutionByID(ctx, "00000000-0000-0000-0000-000000000000", created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read error = %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-pg", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if claim.ID != created.ID || string(claim.SealedPayload) != "sealed-pg-payload" {
		t.Fatalf("claim = %#v", claim)
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	exitCode := 0
	completed, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`{"value":42}`), ExitCode: &exitCode,
		Usage:      api.ExecutionUsage{WallTimeMS: 2, CPUTimeMS: 1, PeakMemoryMB: 10},
		FinishedAt: base.Add(3 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	if completed.Status != api.ExecutionStatusSucceeded || pgExecutionPayloadCount(t, pool, ctx, created.ID) != 0 {
		t.Fatalf("completion = %#v payload_count=%d", completed, pgExecutionPayloadCount(t, pool, ctx, created.ID))
	}
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusFailed,
		FinishedAt: base.Add(4 * time.Millisecond),
	}); !errors.Is(err, state.ErrExecutionLeaseLost) {
		t.Fatalf("stale completion error = %v", err)
	}
	if _, err := pool.Exec(ctx, `update executions set status='failed' where id=$1::uuid`, created.ID); err == nil {
		t.Fatal("database allowed terminal state rewrite")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
			t.Fatalf("terminal rewrite error = %v", err)
		}
	}

	if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	params := pgExecutionParams(t, account.ID, base.Add(time.Second), 0, "atomic")
	const callers = 8
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.CreateExecution(context.Background(), params)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	createdCount, quotaCount := 0, 0
	for err := range results {
		if err == nil {
			createdCount++
		} else if errors.Is(err, state.ErrExecutionQuotaExceeded) {
			quotaCount++
		} else {
			t.Fatalf("parallel admission: %v", err)
		}
	}
	if createdCount != 1 || quotaCount != callers-1 {
		t.Fatalf("parallel admission created=%d quota=%d", createdCount, quotaCount)
	}
}

func TestPgStoreExecutionSweepRecovery(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "sweep")
	base := time.Now().UTC().Add(time.Second)
	expired, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base, 100, "expired"))
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	restore, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base.Add(time.Millisecond), 5000, "restore"))
	if err != nil {
		t.Fatalf("create restore: %v", err)
	}
	running, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base.Add(2*time.Millisecond), 5000, "running"))
	if err != nil {
		t.Fatalf("create running: %v", err)
	}
	restoreClaim, err := store.ClaimExecution(ctx, "schedd-a", base.Add(150*time.Millisecond), 50*time.Millisecond)
	if err != nil || restoreClaim.ID != restore.ID {
		t.Fatalf("restore claim id=%s err=%v", restoreClaim.ID, err)
	}
	runningClaim, err := store.ClaimExecution(ctx, "schedd-b", base.Add(151*time.Millisecond), 50*time.Millisecond)
	if err != nil || runningClaim.ID != running.ID {
		t.Fatalf("running claim id=%s err=%v", runningClaim.ID, err)
	}
	if _, err := store.MarkExecutionRunning(ctx, running.ID, *runningClaim.LeaseToken, base.Add(152*time.Millisecond)); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	sweep, err := store.SweepExecutions(ctx, base.Add(300*time.Millisecond), 10)
	if err != nil {
		t.Fatalf("SweepExecutions: %v", err)
	}
	if sweep.ExpiredQueued != 1 || sweep.RequeuedRestores != 1 || sweep.FinishedRuns != 1 || sweep.PayloadsDeleted != 2 {
		t.Fatalf("sweep = %#v", sweep)
	}
	expiredRow, _ := store.ExecutionByID(ctx, account.ID, expired.ID)
	restoreRow, _ := store.ExecutionByID(ctx, account.ID, restore.ID)
	runningRow, _ := store.ExecutionByID(ctx, account.ID, running.ID)
	if expiredRow.Status != api.ExecutionStatusTimedOut || restoreRow.Status != api.ExecutionStatusQueued ||
		runningRow.Status != api.ExecutionStatusFailed || runningRow.FailureCode == nil || *runningRow.FailureCode != "lease_expired" {
		t.Fatalf("states expired=%q restore=%q running=%#v", expiredRow.Status, restoreRow.Status, runningRow)
	}
	if pgExecutionPayloadCount(t, pool, ctx, expired.ID) != 0 ||
		pgExecutionPayloadCount(t, pool, ctx, running.ID) != 0 ||
		pgExecutionPayloadCount(t, pool, ctx, restore.ID) != 1 {
		t.Fatal("sweep payload retention diverged from lifecycle state")
	}
}
