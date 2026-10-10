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
	runID, _, err := admitEventWorkflowTx(ctx, queries, tx, outboxID, recipient, accepted.Payload)
	if err != nil {
		return "", err
	}
	if err := validateEventRoutingClaim(ctx, queries, tx, PublishedEventRoutingClaim{
		OutboxID: outboxID, SubscriptionID: recipientID, ClaimToken: token,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return runID, nil
}

func admitEventWorkflowTx(ctx context.Context, queries *sqlc.Queries, tx pgx.Tx, outboxID int64, recipient PublishedEventRecipient, payload []byte) (string, bool, error) {
	prior, err := queries.GetEventWorkflowReceipt(ctx, tx, sqlc.GetEventWorkflowReceiptParams{
		OutboxID: outboxID, RecipientID: mustPgUUID(recipient.ID),
	})
	if err == nil {
		if prior.Valid {
			return uuidFromPgtype(prior).String(), false, nil
		}
		return "", false, nil // The run was pruned; its durable receipt still wins.
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	if err := queries.LockWorkflowRunAdmission(ctx, tx, recipient.AppID); err != nil {
		return "", false, err
	}
	target, err := queries.LockEventWorkflowTarget(ctx, tx, mustPgUUID(recipient.AppID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	if target.AppStatus == string(AppDeleted) || uuidFromPgtype(target.AccountID).String() != recipient.AccountID {
		return "", false, ErrNotFound
	}
	plan := api.Plan(target.Plan)
	if (target.AccountStatus != "active" && target.AccountStatus != "past_due") || target.AbuseHoldAt.Valid ||
		!plan.WorkflowsAllowed() || target.MaintenanceMode || (target.PlatformTenantRequired && recipient.PlatformTenantID == "") {
		return "", false, ErrWorkflowEventTargetUnavailable
	}
	if recipient.PlatformTenantID != "" {
		if !target.PlatformTenantRequired || lockTenantPublishedEventBinding(ctx, tx, recipient.AccountID, recipient.PlatformTenantID, recipient.AppID) != nil {
			return "", false, ErrWorkflowEventTargetUnavailable
		}
	}
	run, err := eventWorkflowRun(recipient, payload, plan)
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrWorkflowEventDefinitionInvalid, err)
	}
	paused, err := queries.AutomationFailurePaused(ctx, tx, sqlc.AutomationFailurePausedParams{AppID: mustPgUUID(recipient.AppID), Name: run.WorkflowName})
	if err != nil {
		return "", false, err
	}
	if paused {
		return "", false, ErrWorkflowEventTargetUnavailable
	}
	active, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(recipient.AppID)})
	if err != nil {
		return "", false, err
	}
	if int(active) >= plan.WorkflowMaxConcurrentRuns() {
		return "", false, ErrWorkflowRunQuotaExceeded
	}
	if err := queries.InsertEventWorkflowRun(ctx, tx, sqlc.InsertEventWorkflowRunParams{
		DeploymentID: mustPgUUID(run.DeploymentID), ID: mustPgUUID(run.ID), AppID: mustPgUUID(run.AppID), WorkflowName: run.WorkflowName,
		PlatformTenantID: run.PlatformTenantID, Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot,
	}); err != nil {
		return "", false, err
	}
	if err := queries.InsertEventWorkflowReceipt(ctx, tx, sqlc.InsertEventWorkflowReceiptParams{
		OutboxID: outboxID, RecipientID: mustPgUUID(recipient.ID), RunID: mustPgUUID(run.ID),
	}); err != nil {
		return "", false, err
	}
	return run.ID, true, nil
}
