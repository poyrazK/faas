package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// completeRetentionTestExecution admits, claims, runs, and completes one
// execution admitted at admittedAt.
func completeRetentionTestExecution(t *testing.T, store *MemStore, accountID, workflowID string, admittedAt time.Time) Execution {
	t.Helper()
	ctx := context.Background()
	params := executionTestParams(t, accountID, admittedAt, 0, "sealed")
	if workflowID != "" {
		params.WorkflowID = workflowID
		params.StepLabel = "step"
	}
	created, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-a", admittedAt.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, admittedAt.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	completed, err := store.CompleteExecution(ctx, CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`{"ok":true}`), Stdout: "out\n",
		Usage:      api.ExecutionUsage{WallTimeMS: 40, CPUTimeMS: 30, PeakMemoryMB: 20},
		FinishedAt: admittedAt.Add(3 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	return completed
}

func TestMemStorePurgeExpiredExecutionsKeepsUsageAndFreshReceipts(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	account := executionTestAccount(t, store, "retention")
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	fresh := time.Now().UTC().Add(-time.Hour)

	expired := completeRetentionTestExecution(t, store, account.ID, "", old)
	kept := completeRetentionTestExecution(t, store, account.ID, "", fresh)
	if _, err := store.AppendExecutionEvent(ctx, account.ID, expired.ID, ExecutionEventStatus, executionStatusPayload(api.ExecutionStatusSucceeded), old); err != nil {
		t.Fatalf("AppendExecutionEvent: %v", err)
	}
	usageBefore, err := store.ExecutionUsageByAccount(ctx, account.ID, old)
	if err != nil {
		t.Fatalf("ExecutionUsageByAccount: %v", err)
	}

	cutoff := time.Now().UTC().Add(-api.ExecutionReceiptRetention)
	deleted, err := store.PurgeExpiredExecutions(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("PurgeExpiredExecutions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := store.ExecutionByID(ctx, account.ID, expired.ID); err == nil {
		t.Fatal("expired receipt is still readable")
	}
	if _, ok := store.executionEvents[expired.ID]; ok {
		t.Fatal("expired receipt kept its replay events")
	}
	if _, err := store.ExecutionByID(ctx, account.ID, kept.ID); err != nil {
		t.Fatalf("fresh receipt was purged: %v", err)
	}
	usageAfter, err := store.ExecutionUsageByAccount(ctx, account.ID, old)
	if err != nil {
		t.Fatalf("ExecutionUsageByAccount after purge: %v", err)
	}
	if usageAfter != usageBefore || usageAfter.Runs == 0 {
		t.Fatalf("usage changed by retention: before=%+v after=%+v", usageBefore, usageAfter)
	}
	if again, err := store.PurgeExpiredExecutions(ctx, cutoff, 100); err != nil || again != 0 {
		t.Fatalf("second purge = %d, %v; want 0, nil", again, err)
	}
}

func TestMemStorePurgeExpiredExecutionsGuards(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	account := executionTestAccount(t, store, "retention-guards")
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)

	inWorkflow := completeRetentionTestExecution(t, store, account.ID, "incident-1", old)
	store.executionWorkflowJobs["job-1"] = ExecutionWorkflowJob{
		ID: "job-1", AccountID: account.ID, WorkflowID: "incident-1",
		Status: api.ManagedExecutionWorkflowRunning, CreatedAt: old, UpdatedAt: old,
	}
	unrecorded := completeRetentionTestExecution(t, store, account.ID, "", old.Add(time.Second))
	delete(store.executionUsageLedger, unrecorded.ID)

	cutoff := time.Now().UTC().Add(-api.ExecutionReceiptRetention)
	if deleted, err := store.PurgeExpiredExecutions(ctx, cutoff, 100); err != nil || deleted != 0 {
		t.Fatalf("guarded purge = %d, %v; want 0, nil", deleted, err)
	}
	for _, id := range []string{inWorkflow.ID, unrecorded.ID} {
		if _, err := store.ExecutionByID(ctx, account.ID, id); err != nil {
			t.Fatalf("guarded receipt %s was purged: %v", id, err)
		}
	}

	job := store.executionWorkflowJobs["job-1"]
	job.Status = api.ManagedExecutionWorkflowSucceeded
	store.executionWorkflowJobs["job-1"] = job
	if deleted, err := store.PurgeExpiredExecutions(ctx, cutoff, 100); err != nil || deleted != 1 {
		t.Fatalf("purge after workflow finished = %d, %v; want 1, nil", deleted, err)
	}
	if _, err := store.PurgeExpiredExecutions(ctx, time.Time{}, 100); err == nil {
		t.Fatal("zero cutoff was accepted")
	}
}
