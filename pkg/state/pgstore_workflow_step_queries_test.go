package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgCustomerOperationWorkflowStepQueriesFenceAndCommitAttempt(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	run, _ := seedResumeRun(t, NewPgStore(pool), simpleResumeSpec())

	var runID pgtype.UUID
	if err := runID.Scan(run.ID); err != nil {
		t.Fatalf("parse workflow run ID: %v", err)
	}
	queries := sqlc.New()
	stepName := "send"
	initial, err := queries.GetCustomerOperationWorkflowStep(ctx, pool, sqlc.GetCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName,
	})
	if err != nil {
		t.Fatalf("read initial workflow step: %v", err)
	}
	if initial.Status != WorkflowStepStatusPending {
		t.Fatalf("initial status = %q, want pending", initial.Status)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	// A skipped generation must not claim the pending step.
	if _, err := queries.StartCustomerOperationWorkflowStep(ctx, tx, sqlc.StartCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName, Attempt: initial.Attempt + 2, Input: []byte(`{"resolved":true}`),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("start with a skipped attempt = %v, want no rows", err)
	}

	attempt := initial.Attempt + 1
	resolvedInput := []byte(`{"resolved":"frozen"}`)
	startedInput, err := queries.StartCustomerOperationWorkflowStep(ctx, tx, sqlc.StartCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName, Attempt: attempt, Input: resolvedInput,
	})
	if err != nil {
		t.Fatalf("start next workflow attempt: %v", err)
	}
	if compactWorkflowStepQueryJSON(t, startedInput) != compactWorkflowStepQueryJSON(t, resolvedInput) {
		t.Fatalf("started input = %s, want %s", startedInput, resolvedInput)
	}
	if err := queries.InsertCustomerOperationWorkflowStepAttempt(ctx, tx, sqlc.InsertCustomerOperationWorkflowStepAttemptParams{
		RunID: runID, StepName: stepName, Attempt: attempt,
	}); err != nil {
		t.Fatalf("insert attempt receipt: %v", err)
	}
	if err := queries.TouchCustomerOperationWorkflowStep(ctx, tx, sqlc.TouchCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName,
	}); err != nil {
		t.Fatalf("touch current step: %v", err)
	}

	output := []byte(`{"delivered":true}`)
	stepRows, err := queries.CompleteCustomerOperationWorkflowStep(ctx, tx, sqlc.CompleteCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName, Attempt: attempt, Status: WorkflowStepStatusSucceeded, Output: output,
	})
	if err != nil || stepRows != 1 {
		t.Fatalf("complete step = rows %d, err %v; want one row", stepRows, err)
	}
	attemptRows, err := queries.CompleteCustomerOperationWorkflowStepAttempt(ctx, tx, sqlc.CompleteCustomerOperationWorkflowStepAttemptParams{
		RunID: runID, StepName: stepName, Attempt: attempt, Status: WorkflowStepStatusSucceeded,
		HttpStatus: pgtype.Int4{Int32: 204, Valid: true},
	})
	if err != nil || attemptRows != 1 {
		t.Fatalf("complete attempt = rows %d, err %v; want one row", attemptRows, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	gotStep, err := queries.GetCustomerOperationWorkflowStep(ctx, pool, sqlc.GetCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName,
	})
	if err != nil {
		t.Fatalf("read completed workflow step: %v", err)
	}
	if gotStep.Status != WorkflowStepStatusSucceeded || gotStep.Attempt != attempt || compactWorkflowStepQueryJSON(t, gotStep.Input) != compactWorkflowStepQueryJSON(t, resolvedInput) || compactWorkflowStepQueryJSON(t, gotStep.Output) != compactWorkflowStepQueryJSON(t, output) || !gotStep.FinishedAt.Valid {
		t.Fatalf("completed workflow step lost its attempt data: %+v", gotStep)
	}
	gotAttempt, err := queries.GetCustomerOperationWorkflowStepAttempt(ctx, pool, sqlc.GetCustomerOperationWorkflowStepAttemptParams{
		RunID: runID, StepName: stepName, Attempt: attempt,
	})
	if err != nil {
		t.Fatalf("read completed attempt receipt: %v", err)
	}
	if gotAttempt.Status != WorkflowStepStatusSucceeded || !gotAttempt.HttpStatus.Valid || gotAttempt.HttpStatus.Int32 != 204 || !gotAttempt.FinishedAt.Valid {
		t.Fatalf("completed attempt receipt lost its terminal result: %+v", gotAttempt)
	}

	var currentStep string
	if err := pool.QueryRow(ctx, "SELECT current_step FROM workflow_runs WHERE id=$1", runID).Scan(&currentStep); err != nil {
		t.Fatalf("read current workflow step: %v", err)
	}
	if currentStep != stepName {
		t.Fatalf("current step = %q, want %q", currentStep, stepName)
	}
	if _, err := queries.StartCustomerOperationWorkflowStep(ctx, pool, sqlc.StartCustomerOperationWorkflowStepParams{
		RunID: runID, StepName: stepName, Attempt: attempt + 1, Input: resolvedInput,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("restart terminal step = %v, want no rows", err)
	}
}

func compactWorkflowStepQueryJSON(t *testing.T, value []byte) string {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		t.Fatalf("compact workflow step JSON %q: %v", value, err)
	}
	return compact.String()
}
