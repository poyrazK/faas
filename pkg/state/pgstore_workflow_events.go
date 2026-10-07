package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListMatchingEventWorkflows(ctx context.Context, accountID, source, typ, after string, limit int) ([]PublishedEventRecipient, error) {
	rows, err := sqlc.New().ListMatchingEventWorkflows(ctx, s.pool, sqlc.ListMatchingEventWorkflowsParams{
		AccountID: mustPgUUID(accountID), Source: source, EventType: typ, AfterID: after, BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	result := make([]PublishedEventRecipient, 0, len(rows))
	for _, raw := range rows {
		var recipient PublishedEventRecipient
		if err := json.Unmarshal(raw, &recipient); err != nil {
			return nil, err
		}
		result = append(result, recipient)
	}
	return result, nil
}

func (s *PgStore) AdmitEventWorkflow(ctx context.Context, outboxID int64, token, recipientID string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New()
	accepted, err := queries.LockEventWorkflowRecipient(ctx, tx, sqlc.LockEventWorkflowRecipientParams{
		OutboxID: outboxID, ClaimToken: mustPgUUID(token), RecipientID: recipientID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrConflict
	}
	if err != nil {
		return "", err
	}
	var recipient PublishedEventRecipient
	if err := json.Unmarshal(accepted.Recipient, &recipient); err != nil || recipient.ID != recipientID || len(recipient.Workflow) == 0 {
		return "", fmt.Errorf("%w: captured recipient is missing or invalid", ErrWorkflowEventDefinitionInvalid)
	}
	prior, err := queries.GetEventWorkflowReceipt(ctx, tx, sqlc.GetEventWorkflowReceiptParams{
		OutboxID: outboxID, RecipientID: mustPgUUID(recipientID),
	})
	if err == nil {
		if prior.Valid {
			return uuidFromPgtype(prior).String(), nil
		}
		return "", nil // The run was pruned; its durable receipt still wins.
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if err := queries.LockWorkflowRunAdmission(ctx, tx, recipient.AppID); err != nil {
		return "", err
	}
	target, err := queries.LockEventWorkflowTarget(ctx, tx, mustPgUUID(recipient.AppID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if target.AppStatus == string(AppDeleted) || uuidFromPgtype(target.AccountID).String() != recipient.AccountID {
		return "", ErrNotFound
	}
	plan := api.Plan(target.Plan)
	if (target.AccountStatus != "active" && target.AccountStatus != "past_due") || target.AbuseHoldAt.Valid ||
		!plan.WorkflowsAllowed() || target.MaintenanceMode || (target.PlatformTenantRequired && recipient.PlatformTenantID == "") {
		return "", ErrWorkflowEventTargetUnavailable
	}
	if recipient.PlatformTenantID != "" {
		if !target.PlatformTenantRequired || lockTenantPublishedEventBinding(ctx, tx, recipient.AccountID, recipient.PlatformTenantID, recipient.AppID) != nil {
			return "", ErrWorkflowEventTargetUnavailable
		}
	}
	run, err := eventWorkflowRun(recipient, accepted.Payload, plan)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrWorkflowEventDefinitionInvalid, err)
	}
	active, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(recipient.AppID)})
	if err != nil {
		return "", err
	}
	if int(active) >= plan.WorkflowMaxConcurrentRuns() {
		return "", ErrWorkflowRunQuotaExceeded
	}
	if err := queries.InsertEventWorkflowRun(ctx, tx, sqlc.InsertEventWorkflowRunParams{
		DeploymentID: mustPgUUID(run.DeploymentID), ID: mustPgUUID(run.ID), AppID: mustPgUUID(run.AppID), WorkflowName: run.WorkflowName,
		PlatformTenantID: run.PlatformTenantID, Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot,
	}); err != nil {
		return "", err
	}
	if err := queries.InsertEventWorkflowReceipt(ctx, tx, sqlc.InsertEventWorkflowReceiptParams{
		OutboxID: outboxID, RecipientID: mustPgUUID(recipientID), RunID: mustPgUUID(run.ID),
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return run.ID, nil
}
