package state

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreManagedExecutionWorkflowLeaseAndPayloadRetention(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(context.Background(), "managed-workflow@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Truncate(time.Millisecond)
	params := CreateExecutionWorkflowJobParams{
		AccountID: account.ID, WorkflowID: "managed-chain", PlanID: "0123456789abcdef01234567",
		StepCount: 2, SealedPlan: []byte("encrypted-plan"), PayloadKID: "age1recipient", CreatedAt: createdAt,
	}
	row, err := store.CreateExecutionWorkflowJob(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateExecutionWorkflowJob: %v", err)
	}
	if row.Status != api.ManagedExecutionWorkflowQueued || string(row.SealedPlan) != "encrypted-plan" {
		t.Fatalf("created job = %+v", row)
	}
	if _, err := store.CreateExecutionWorkflowJob(context.Background(), params); !errors.Is(err, ErrExecutionWorkflowJobExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	claim, err := store.ClaimExecutionWorkflowJob(context.Background(), "worker-a", createdAt.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("ClaimExecutionWorkflowJob: %v", err)
	}
	if _, err := store.ClaimExecutionWorkflowJob(context.Background(), "worker-b", createdAt.Add(2*time.Second), time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("concurrent claim error = %v, want ErrNotFound", err)
	}
	updateAt := createdAt.Add(3 * time.Second)
	update := ExecutionWorkflowJobUpdate{Status: api.ManagedExecutionWorkflowQueued, NextStep: 1, ScheduledFor: updateAt, UpdatedAt: updateAt}
	if err := store.UpdateExecutionWorkflowJob(context.Background(), row.ID, "stale-token", update); !errors.Is(err, ErrExecutionWorkflowLeaseLost) {
		t.Fatalf("stale lease update error = %v", err)
	}
	if err := store.UpdateExecutionWorkflowJob(context.Background(), row.ID, claim.ClaimToken, update); err != nil {
		t.Fatalf("release claim: %v", err)
	}
	if _, err := store.ClaimExecutionWorkflowJob(context.Background(), "worker-b", updateAt.Add(-time.Second), time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("early claim error = %v, want ErrNotFound", err)
	}
	claim, err = store.ClaimExecutionWorkflowJob(context.Background(), "worker-b", updateAt, time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	finishAt := updateAt.Add(time.Second)
	if err := store.UpdateExecutionWorkflowJob(context.Background(), row.ID, claim.ClaimToken, ExecutionWorkflowJobUpdate{
		Status: api.ManagedExecutionWorkflowSucceeded, NextStep: 2, ScheduledFor: finishAt, UpdatedAt: finishAt,
	}); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	terminal, err := store.ExecutionWorkflowJobByKey(context.Background(), account.ID, params.WorkflowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != api.ManagedExecutionWorkflowSucceeded || terminal.NextStep != 2 || len(terminal.SealedPlan) != 0 || terminal.PayloadKID != "" {
		t.Fatalf("terminal job retains plan or lost progress: %+v", terminal)
	}
}

func TestMemStoreManagedExecutionWorkflowQueueLimit(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(context.Background(), "managed-workflow-limit@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Millisecond)
	params := make([]CreateExecutionWorkflowJobParams, 0, ExecutionWorkflowManagedMaxActivePerAccount)
	for i := 0; i < ExecutionWorkflowManagedMaxActivePerAccount; i++ {
		job := CreateExecutionWorkflowJobParams{
			AccountID: account.ID, WorkflowID: "managed-queue-" + strconv.Itoa(i), PlanID: "0123456789abcdef01234567",
			StepCount: 1, SealedPlan: []byte("sealed"), PayloadKID: "kid", CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if _, err := store.CreateExecutionWorkflowJob(context.Background(), job); err != nil {
			t.Fatalf("create workflow %d: %v", i, err)
		}
		params = append(params, job)
	}
	if _, err := store.CreateExecutionWorkflowJob(context.Background(), params[0]); !errors.Is(err, ErrExecutionWorkflowJobExists) {
		t.Fatalf("duplicate in full queue error = %v", err)
	}
	overflow := params[0]
	overflow.WorkflowID = "managed-queue-overflow"
	if _, err := store.CreateExecutionWorkflowJob(context.Background(), overflow); !errors.Is(err, ErrExecutionWorkflowQueueFull) {
		t.Fatalf("full queue error = %v, want ErrExecutionWorkflowQueueFull", err)
	}
	claim, err := store.ClaimExecutionWorkflowJob(context.Background(), "queue-test", base.Add(time.Duration(ExecutionWorkflowManagedMaxActivePerAccount+1)*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := base.Add(time.Duration(ExecutionWorkflowManagedMaxActivePerAccount+2) * time.Second)
	if err := store.UpdateExecutionWorkflowJob(context.Background(), claim.ID, claim.ClaimToken, ExecutionWorkflowJobUpdate{
		Status: api.ManagedExecutionWorkflowSucceeded, NextStep: 1, ScheduledFor: finishedAt, UpdatedAt: finishedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateExecutionWorkflowJob(context.Background(), overflow); err != nil {
		t.Fatalf("terminal workflow did not release queue slot: %v", err)
	}
}

func TestMemStoreManagedExecutionWorkflowOwnershipFilter(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(context.Background(), "managed-workflow-ownership@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	first, second := uuid.NewString(), uuid.NewString()
	base := time.Now().UTC()
	for i, principal := range []string{first, second} {
		_, err := store.CreateExecutionWorkflowJob(context.Background(), CreateExecutionWorkflowJobParams{
			AccountID: account.ID, RunsPrincipalID: &principal, WorkflowID: "same-name",
			PlanID: "0123456789abcdef0123456" + string(rune('0'+i)), StepCount: 1,
			SealedPlan: []byte("sealed"), PayloadKID: "kid", CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ExecutionWorkflowJobByKey(context.Background(), account.ID, "same-name", &first)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunsPrincipalID == nil || *got.RunsPrincipalID != first {
		t.Fatalf("principal filter returned %+v", got)
	}
	got, err = store.ExecutionWorkflowJobByKey(context.Background(), account.ID, "same-name", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunsPrincipalID == nil || *got.RunsPrincipalID != second {
		t.Fatalf("broad lookup did not return latest workflow: %+v", got)
	}
}

func TestMemStoreAccountDeletionErasesManagedWorkflowPlan(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(context.Background(), "managed-workflow-delete@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.CreateExecutionWorkflowJob(context.Background(), CreateExecutionWorkflowJobParams{
		AccountID: account.ID, WorkflowID: "delete-me", PlanID: "0123456789abcdef01234567",
		StepCount: 1, SealedPlan: []byte("encrypted-plan"), PayloadKID: "kid", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStatus(context.Background(), account.ID, AccountDeletedPending); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecutionWorkflowJobByKey(context.Background(), account.ID, row.WorkflowID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted account still owns workflow job: %v", err)
	}
}
