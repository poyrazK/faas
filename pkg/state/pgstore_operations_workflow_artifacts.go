package state

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func workflowArtifactAuthorityTx(ctx context.Context, tx pgx.Tx, id string, a OperationWorkflowAuthority) (Operation, WorkflowRun, error) {
	op, run, _, err := workflowOperationAuthorityTx(ctx, tx, id, a, true)
	return op, run, err
}

func workflowOperationAuthorityTx(ctx context.Context, tx pgx.Tx, id string, a OperationWorkflowAuthority, artifact bool) (Operation, WorkflowRun, workflowOperationWindow, error) {
	q := sqlc.New()
	if a.Attempt < 1 || a.Attempt > math.MaxInt32 {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrInvalidArgument
	}
	for _, value := range []string{id, a.AccountID, a.AppID, a.InstanceID, a.RunID} {
		if _, err := operationUUID(value); err != nil {
			return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
		}
	}
	if _, err := q.LockWorkflowRecovery(ctx, tx, mustPgUUID(a.RunID)); err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, mustPgUUID(a.RunID))
	if err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
	}
	if op.ID != id {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrNotFound
	}
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, tx, mustPgUUID(a.RunID))
	if err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, mapErr(err)
	}
	run := *workflowRunFromSQLC(row)
	proof, err := q.WorkflowOutboundAttempt(ctx, tx, sqlc.WorkflowOutboundAttemptParams{RunID: mustPgUUID(a.RunID), StepName: a.StepName, Attempt: int32(a.Attempt)})
	if err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrOperationStaleAttempt
	}
	if err := validateOperationWorkflowExecutionAuthority(op, run, a, pgUUIDString(proof.OutboundAttemptToken), !artifact); err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
	}
	valid, err := q.CustomerOperationWorkflowInstance(ctx, tx, sqlc.CustomerOperationWorkflowInstanceParams{InstanceID: mustPgUUID(a.InstanceID), AppID: mustPgUUID(op.AppID), DeploymentID: mustPgUUID(op.DeploymentID)})
	if err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
	}
	if !valid {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrNotFound
	}
	if artifact {
		if err := validateOperationWorkflowAuthority(op, run, a, pgUUIDString(proof.OutboundAttemptToken)); err != nil {
			return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
		}
	}
	window, err := workflowOperationWindowTx(ctx, tx, a.RunID, a.StepName, a.Attempt)
	return op, run, window, err
}

func (s *PgStore) ReuseWorkflowOperationArtifact(ctx context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest) (api.OperationWorkflowArtifactResponse, error) {
	_, _, response, err := s.workflowArtifactTransaction(ctx, id, a, req, "reuse", "")
	return response, err
}
func (s *PgStore) ReserveWorkflowOperationArtifact(ctx context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest) (OperationResultBlob, Operation, error) {
	blob, op, _, err := s.workflowArtifactTransaction(ctx, id, a, req, "reserve", "")
	return blob, op, err
}
func (s *PgStore) PrepareVerifiedWorkflowOperationArtifact(ctx context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest, blobID string) (api.OperationWorkflowArtifactResponse, error) {
	_, _, response, err := s.workflowArtifactTransaction(ctx, id, a, req, "prepare", blobID)
	return response, err
}

func (s *PgStore) workflowArtifactTransaction(ctx context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest, action, blobID string) (OperationResultBlob, Operation, api.OperationWorkflowArtifactResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationWorkflowArtifactResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if action == "reserve" {
		if err := q.LockCustomerOperationArtifactQuota(ctx, tx, a.AccountID); err != nil {
			return OperationResultBlob{}, Operation{}, api.OperationWorkflowArtifactResponse{}, err
		}
	}
	op, run, err := workflowArtifactAuthorityTx(ctx, tx, id, a)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationWorkflowArtifactResponse{}, err
	}
	blob, response, err := workflowArtifactMutationTx(ctx, tx, &op, run, a, req, action, blobID)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationWorkflowArtifactResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationWorkflowArtifactResponse{}, err
	}
	return blob, op, response, nil
}

func workflowArtifactMutationTx(ctx context.Context, tx pgx.Tx, op *Operation, run WorkflowRun, a OperationWorkflowAuthority, req api.OperationArtifactRequest, action, blobID string) (OperationResultBlob, api.OperationWorkflowArtifactResponse, error) {
	q := sqlc.New()
	receipt, exists, err := workflowArtifactReceipt(*op, a, req)
	if err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	if exists {
		row, err := q.LockCustomerOperationBlob(ctx, tx, mustPgUUID(receipt.BlobID))
		if err != nil {
			return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, mapErr(err)
		}
		// A blob lock wait must not extend the step's authority or deadline.
		if _, _, err := workflowArtifactAuthorityTx(ctx, tx, op.ID, a); err != nil {
			return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
		}
		blob := operationPGBlob(row)
		response, err := rebindWorkflowArtifact(op, a, req.ReportID, receipt, blob)
		if err != nil {
			return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
		}
		raw, err := json.Marshal(op)
		if err != nil {
			return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
		}
		err = q.UpdateCustomerOperation(ctx, tx, sqlc.UpdateCustomerOperationParams{ID: mustPgUUID(op.ID), State: string(op.State), Record: raw, ExpiresAt: operationBlobTime(op.ExpiresAt)})
		return blob, response, err
	}
	if action == "reuse" {
		copy := cloneOperation(*op)
		_, err := prepareWorkflowArtifact(&copy, a, req, OperationResultBlob{}, time.Now().UTC())
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{Available: false}, err
	}
	now := time.Now().UTC()
	if action == "reserve" {
		blob, err := reserveWorkflowArtifactTx(ctx, tx, *op, a, req, now)
		return blob, api.OperationWorkflowArtifactResponse{}, err
	}
	parsed, err := operationUUID(blobID)
	if err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	row, err := q.LockCustomerOperationBlob(ctx, tx, parsed)
	if err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, mapErr(err)
	}
	if _, _, err := workflowArtifactAuthorityTx(ctx, tx, op.ID, a); err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	now = time.Now().UTC()
	blob := operationPGBlob(row)
	if err := validateWorkflowArtifactBlob(blob, *op, a, req, now); err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	artifact, err := prepareWorkflowArtifact(op, a, req, blob, now)
	if err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	n, err := q.RetainCustomerOperationBlob(ctx, tx, sqlc.RetainCustomerOperationBlobParams{ID: parsed, Now: operationBlobTime(now)})
	if err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	if n != 1 {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, ErrOperationStaleAttempt
	}
	event := operationWorkflowEvent(op, run, "artifact_prepared", map[string]any{"artifact_id": artifact.ID, "workflow_step": a.StepName}, now)
	if err := operationSaveTx(ctx, tx, *op, event); err != nil {
		return OperationResultBlob{}, api.OperationWorkflowArtifactResponse{}, err
	}
	return blob, api.OperationWorkflowArtifactResponse{Available: true, Artifact: &artifact}, nil
}

func reserveWorkflowArtifactTx(ctx context.Context, tx pgx.Tx, op Operation, a OperationWorkflowAuthority, req api.OperationArtifactRequest, now time.Time) (OperationResultBlob, error) {
	q := sqlc.New()
	blob := newOperationResultBlob(op, Invocation{Attempts: a.Attempt}, req, now)
	blob.ExecutionID, blob.WorkflowRunID, blob.WorkflowStep = "", a.RunID, a.StepName
	copy := cloneOperation(op)
	if _, err := prepareWorkflowArtifact(&copy, a, req, blob, now); err != nil {
		return OperationResultBlob{}, err
	}
	account := mustPgUUID(op.AccountID)
	plan, err := q.CustomerOperationAccountPlan(ctx, tx, account)
	if err != nil {
		return OperationResultBlob{}, mapErr(err)
	}
	usage, err := q.CustomerOperationBlobUsage(ctx, tx, account)
	if err != nil {
		return OperationResultBlob{}, err
	}
	if err := checkOperationBlobQuota(api.MustLimitsFor(api.Plan(plan)).Operations, usage.BlobCount, usage.Bytes, req.SizeBytes); err != nil {
		return OperationResultBlob{}, err
	}
	err = q.InsertCustomerOperationBlob(ctx, tx, sqlc.InsertCustomerOperationBlobParams{ID: mustPgUUID(blob.ID), OperationID: mustPgUUID(op.ID), AccountID: account, Generation: int32(op.Generation), WorkflowRunID: mustPgUUID(a.RunID), WorkflowStep: pgtype.Text{String: a.StepName, Valid: true}, Attempt: int32(a.Attempt), ReportID: req.ReportID, Fingerprint: blob.Fingerprint, StorageKey: blob.StorageKey, SizeBytes: blob.SizeBytes, ExpiresAt: operationBlobTime(blob.ExpiresAt)})
	return blob, err
}

var _ OperationWorkflowArtifactStore = (*PgStore)(nil)
