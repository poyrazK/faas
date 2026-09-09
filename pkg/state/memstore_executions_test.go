package state

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func executionTestParams(t *testing.T, accountID string, admittedAt time.Time, timeoutMS int, payload string) CreateExecutionParams {
	t.Helper()
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22,
		Source:  "export default async function main(input) { return input }",
		Input:   []byte(`{"hello":"world"}`),
	}
	if timeoutMS != 0 {
		request.Limits = &api.ExecutionLimitRequest{TimeoutMS: timeoutMS}
	}
	resolved, problem := request.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatalf("resolve request: %v", problem)
	}
	return CreateExecutionParams{
		AccountID: accountID, Request: resolved,
		SourceBytes: len(request.Source), InputBytes: len(request.Input),
		AdmittedAt: admittedAt, DeadlineAt: admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload: []byte(payload), PayloadKID: "execution-test-key",
	}
}

func executionTestAccount(t *testing.T, store *MemStore, suffix string) Account {
	t.Helper()
	account, err := store.CreateAccount(context.Background(), "execution-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return account
}

func TestMemStoreExecutionAdmissionIsAtomic(t *testing.T) {
	store := NewMemStore()
	account := executionTestAccount(t, store, "atomic")
	if err := store.UpdateAccountPlan(context.Background(), account.ID, api.PlanHobby); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	base := time.Now().UTC().Add(time.Second)

	const callers = 12
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base.Add(time.Duration(i)*time.Microsecond), 0, "sealed"))
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	created, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrExecutionQuotaExceeded):
			var quota *ExecutionQuotaError
			if !errors.As(err, &quota) || quota.Limit != 1 || quota.Observed != 2 {
				t.Fatalf("quota error = %#v", err)
			}
			rejected++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if created != 1 || rejected != callers-1 {
		t.Fatalf("created=%d rejected=%d, want 1/%d", created, rejected, callers-1)
	}
}

func TestMemStoreExecutionClaimCompletionAndPayloadErasure(t *testing.T) {
	store := NewMemStore()
	account := executionTestAccount(t, store, "lifecycle")
	base := time.Now().UTC().Add(time.Second)
	created, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base, 0, "sealed-source-and-input"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	read, err := store.ExecutionByID(context.Background(), account.ID, created.ID)
	if err != nil {
		t.Fatalf("ExecutionByID: %v", err)
	}
	if read.Result != nil || read.Status != api.ExecutionStatusQueued {
		t.Fatalf("customer projection leaked or changed payload: %#v", read)
	}
	if _, err := store.ExecutionByID(context.Background(), "another-account", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account read error = %v, want ErrNotFound", err)
	}

	claim, err := store.ClaimExecution(context.Background(), "schedd-a", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if string(claim.SealedPayload) != "sealed-source-and-input" || claim.PayloadKID != "execution-test-key" {
		t.Fatalf("claim payload = %q kid=%q", claim.SealedPayload, claim.PayloadKID)
	}
	claim.SealedPayload[0] = 'X'
	if _, err := store.ClaimExecution(context.Background(), "schedd-b", base.Add(2*time.Millisecond), time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim error = %v, want ErrNotFound", err)
	}
	running, err := store.MarkExecutionRunning(context.Background(), created.ID, *claim.LeaseToken, base.Add(3*time.Millisecond))
	if err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	if running.Status != api.ExecutionStatusRunning || running.StartedAt == nil {
		t.Fatalf("running row = %#v", running)
	}

	exitCode := 0
	completed, err := store.CompleteExecution(context.Background(), CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`{"hello":"world"}`), Stdout: "ok\n", ExitCode: &exitCode,
		Usage:      api.ExecutionUsage{WallTimeMS: 3, CPUTimeMS: 2, PeakMemoryMB: 12},
		FinishedAt: base.Add(4 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	if !completed.Status.Terminal() || completed.LeaseToken != nil || completed.FinishedAt == nil {
		t.Fatalf("completed row = %#v", completed)
	}
	if _, ok := store.executionPayloads[created.ID]; ok {
		t.Fatal("terminal completion retained encrypted payload")
	}
	if _, err := store.CompleteExecution(context.Background(), CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusFailed,
		FinishedAt: base.Add(5 * time.Millisecond),
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("stale terminal rewrite error = %v, want ErrExecutionLeaseLost", err)
	}
}

func TestMemStoreExecutionCancellationWaitsForTeardownAfterClaim(t *testing.T) {
	store := NewMemStore()
	account := executionTestAccount(t, store, "cancel")
	base := time.Now().UTC().Add(time.Second)

	queued, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base, 0, "queued-secret"))
	if err != nil {
		t.Fatalf("create queued: %v", err)
	}
	cancelled, err := store.RequestExecutionCancellation(context.Background(), account.ID, queued.ID, base.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	if cancelled.Status != api.ExecutionStatusCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("queued cancellation = %#v", cancelled)
	}
	if _, ok := store.executionPayloads[queued.ID]; ok {
		t.Fatal("queued cancellation retained payload")
	}

	active, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base.Add(2*time.Millisecond), 0, "active-secret"))
	if err != nil {
		t.Fatalf("create active: %v", err)
	}
	claim, err := store.ClaimExecution(context.Background(), "schedd-a", base.Add(3*time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("claim active: %v", err)
	}
	if _, err := store.MarkExecutionRunning(context.Background(), active.ID, *claim.LeaseToken, base.Add(4*time.Millisecond)); err != nil {
		t.Fatalf("mark active running: %v", err)
	}
	requested, err := store.RequestExecutionCancellation(context.Background(), account.ID, active.ID, base.Add(5*time.Millisecond))
	if err != nil {
		t.Fatalf("request running cancellation: %v", err)
	}
	if requested.Status != api.ExecutionStatusRunning || requested.CancelRequested == nil || requested.FinishedAt != nil {
		t.Fatalf("running cancellation request = %#v", requested)
	}
	if _, ok := store.executionPayloads[active.ID]; !ok {
		t.Fatal("running cancellation erased payload before teardown acknowledgement")
	}
	if _, err := store.CompleteExecution(context.Background(), CompleteExecutionParams{
		ID: active.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`{"ignored":true}`), FinishedAt: base.Add(5500 * time.Microsecond),
	}); !errors.Is(err, ErrExecutionInvalidTerminal) {
		t.Fatalf("success after cancellation request error = %v, want ErrExecutionInvalidTerminal", err)
	}
	completed, err := store.CompleteExecution(context.Background(), CompleteExecutionParams{
		ID: active.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusCancelled,
		FinishedAt: base.Add(6 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("complete cancellation: %v", err)
	}
	if completed.Status != api.ExecutionStatusCancelled {
		t.Fatalf("completed status = %q", completed.Status)
	}
	if _, ok := store.executionPayloads[active.ID]; ok {
		t.Fatal("acknowledged cancellation retained payload")
	}
}

func TestMemStoreExecutionAdmissionUsesStoredPlanAndCapsLeaseAtDeadline(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	free, err := store.CreateAccount(ctx, "execution-free@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("create free account: %v", err)
	}
	base := time.Now().UTC().Add(time.Second)
	if _, err := store.CreateExecution(ctx, executionTestParams(t, free.ID, base, 0, "free")); !errors.Is(err, ErrExecutionsNotAllowed) {
		t.Fatalf("free admission error = %v, want ErrExecutionsNotAllowed", err)
	}

	hobby, err := store.CreateAccount(ctx, "execution-hobby@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create hobby account: %v", err)
	}
	oversized := executionTestParams(t, hobby.ID, base, 0, "oversized")
	oversized.Request.Limits.MaxOutputBytes = 4 << 20
	if _, err := store.CreateExecution(ctx, oversized); !errors.Is(err, ErrExecutionInvalid) {
		t.Fatalf("cross-plan envelope error = %v, want ErrExecutionInvalid", err)
	}

	created, err := store.CreateExecution(ctx, executionTestParams(t, hobby.ID, base, 100, "deadline"))
	if err != nil {
		t.Fatalf("create deadline execution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd", base.Add(10*time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("claim deadline execution: %v", err)
	}
	if claim.LeaseExpiresAt == nil || !claim.LeaseExpiresAt.Equal(created.DeadlineAt) {
		t.Fatalf("lease expiry = %v, want deadline %v", claim.LeaseExpiresAt, created.DeadlineAt)
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, created.DeadlineAt); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("mark running at deadline error = %v, want ErrExecutionLeaseLost", err)
	}
	sweep, err := store.SweepExecutions(ctx, created.DeadlineAt, 10)
	if err != nil {
		t.Fatalf("sweep deadline execution: %v", err)
	}
	if sweep.FinishedRestores != 1 || sweep.PayloadsDeleted != 1 {
		t.Fatalf("deadline sweep = %#v", sweep)
	}
}

func TestMemStoreExecutionSweepRecoveryNeverReplaysRunningCode(t *testing.T) {
	store := NewMemStore()
	account := executionTestAccount(t, store, "sweep")
	base := time.Now().UTC().Add(time.Second)

	expired, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base, 100, "expired"))
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	restore, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base.Add(time.Millisecond), 5000, "restore"))
	if err != nil {
		t.Fatalf("create restore: %v", err)
	}
	running, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base.Add(2*time.Millisecond), 5000, "running"))
	if err != nil {
		t.Fatalf("create running: %v", err)
	}

	restoreClaim, err := store.ClaimExecution(context.Background(), "schedd-a", base.Add(150*time.Millisecond), 50*time.Millisecond)
	if err != nil || restoreClaim.ID != restore.ID {
		t.Fatalf("restore claim = %s, %v", restoreClaim.ID, err)
	}
	runningClaim, err := store.ClaimExecution(context.Background(), "schedd-b", base.Add(151*time.Millisecond), 50*time.Millisecond)
	if err != nil || runningClaim.ID != running.ID {
		t.Fatalf("running claim = %s, %v", runningClaim.ID, err)
	}
	if _, err := store.MarkExecutionRunning(context.Background(), running.ID, *runningClaim.LeaseToken, base.Add(152*time.Millisecond)); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	sweep, err := store.SweepExecutions(context.Background(), base.Add(300*time.Millisecond), 10)
	if err != nil {
		t.Fatalf("SweepExecutions: %v", err)
	}
	if sweep.ExpiredQueued != 1 || sweep.RequeuedRestores != 1 || sweep.FinishedRuns != 1 || sweep.PayloadsDeleted != 2 {
		t.Fatalf("sweep result = %#v", sweep)
	}
	expiredRow, _ := store.ExecutionByID(context.Background(), account.ID, expired.ID)
	restoreRow, _ := store.ExecutionByID(context.Background(), account.ID, restore.ID)
	runningRow, _ := store.ExecutionByID(context.Background(), account.ID, running.ID)
	if expiredRow.Status != api.ExecutionStatusTimedOut || restoreRow.Status != api.ExecutionStatusQueued ||
		runningRow.Status != api.ExecutionStatusFailed || runningRow.FailureCode == nil || *runningRow.FailureCode != "lease_expired" {
		t.Fatalf("unexpected recovery states: expired=%q restore=%q running=%#v", expiredRow.Status, restoreRow.Status, runningRow)
	}
	if _, ok := store.executionPayloads[restore.ID]; !ok {
		t.Fatal("safe restore requeue lost its payload")
	}
	if _, err := store.CompleteExecution(context.Background(), CompleteExecutionParams{
		ID: running.ID, LeaseToken: *runningClaim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		FinishedAt: base.Add(301 * time.Millisecond),
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("expired running lease completion error = %v", err)
	}
}
