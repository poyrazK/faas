package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func enqueueInvocationRow(ctx context.Context, db sqlc.DBTX, inv Invocation) (Invocation, error) {
	if inv.DeploymentScope != "" && api.ValidateScope(inv.DeploymentScope) != nil {
		return Invocation{}, ErrInvalidArgument
	}
	payload, err := jsonOrEmpty(inv.Payload)
	if err != nil {
		return Invocation{}, fmt.Errorf("state: invocations payload: %w", err)
	}
	headers, err := jsonOrEmpty(inv.Headers)
	if err != nil {
		return Invocation{}, fmt.Errorf("state: invocations headers: %w", err)
	}
	params := sqlc.EnqueueInvocationRowParams{
		Source: string(inv.Source), QueueName: inv.QueueName, State: string(inv.State),
		Method: inv.Method, Path: inv.Path, Payload: payload, Headers: headers,
		DueAt:       pgtype.Timestamptz{Time: inv.DueAt.UTC(), Valid: true},
		ScheduledAt: nullableTimestamptzPtr(inv.ScheduledAt), AckUrl: inv.AckURL,
		LeaseExpiresAt: nullableTimestamptzPtr(inv.LeaseExpiresAt),
		DeadlineAt:     nullableTimestamptzPtr(inv.DeadlineAt), RetryPolicy: inv.RetryPolicyJSON,
		ResultRetentionUntil: nullableTimestamptzPtr(inv.ResultRetentionUntil),
		WorkPolicyName:       inv.WorkPolicyName, WorkKeyDigest: inv.WorkKeyDigest,
		WorkExpiresAt:      nullableTimestamptzPtr(inv.WorkExpiresAt),
		WorkSequence:       pgtype.Int8{Int64: inv.WorkSequence, Valid: inv.WorkSequence != 0},
		WorkPolicyRevision: pgtype.Int8{Int64: inv.WorkPolicyRevision, Valid: inv.WorkPolicyRevision != 0},
		WorkFairnessDigest: inv.WorkFairnessDigest,
		WorkFairnessLimit:  pgtype.Int4{Int32: int32(inv.WorkFairnessLimit), Valid: inv.WorkFairnessLimit != 0},
		DeploymentScope:    inv.DeploymentScope,
	}
	if len(params.RetryPolicy) == 0 {
		params.RetryPolicy = nil
	}
	cronID := ""
	if inv.CronID != nil {
		cronID = *inv.CronID
	}
	for _, input := range []struct {
		name     string
		value    string
		required bool
		target   *pgtype.UUID
	}{
		{"id", inv.ID, false, &params.ID},
		{"app_id", inv.AppID, true, &params.AppID},
		{"account_id", inv.AccountID, true, &params.AccountID},
		{"cron_id", cronID, false, &params.CronID},
		{"on_success_destination_id", inv.OnSuccessDestinationID, false, &params.OnSuccessDestinationID},
		{"on_failure_destination_id", inv.OnFailureDestinationID, false, &params.OnFailureDestinationID},
		{"platform_tenant_id", inv.PlatformTenantID, false, &params.PlatformTenantID},
		{"queue_binding_id", inv.QueueBindingID, false, &params.QueueBindingID},
	} {
		if input.value == "" && !input.required {
			continue
		}
		id, err := uuid.Parse(input.value)
		if err != nil {
			return Invocation{}, fmt.Errorf("%w: invocation %s must be a UUID", ErrInvalidArgument, input.name)
		}
		*input.target = pgtype.UUID{Bytes: id, Valid: true}
	}
	row, err := sqlc.New().EnqueueInvocationRow(ctx, db, params)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	out := invocationFromSQL(row)
	// Preserve historical support for synthetic result fields on the returned
	// object; these fields are not persisted by admission.
	if len(inv.Result) > 0 {
		out.Result = inv.Result
	}
	if inv.LastError != "" {
		out.LastError = inv.LastError
	}
	if inv.CompletedAt != nil {
		out.CompletedAt = inv.CompletedAt
	}
	return out, nil
}

func invocationFromSQL(row sqlc.Invocation) Invocation {
	inv := Invocation{
		ID: uuidString(row.ID), AppID: uuidString(row.AppID), AccountID: uuidString(row.AccountID),
		DeploymentScope: row.DeploymentScope, PlatformTenantID: uuidString(row.PlatformTenantID),
		InstanceID: row.InstanceID.String, Source: InvocationSource(row.Source),
		QueueBindingID: uuidString(row.QueueBindingID), QueueName: row.QueueName, State: InvocationState(row.State),
		Method: row.Method, Path: row.Path, Payload: row.Payload, Headers: row.Headers,
		DueAt: timestamptzToTime(row.DueAt), ScheduledAt: timestamptzToTimePtr(row.ScheduledAt),
		AckURL: row.AckUrl.String, Result: row.Result,
		LeaseExpiresAt: timestamptzToTimePtr(row.LeaseExpiresAt), ReceivedAt: timestamptzToTimePtr(row.ReceivedAt),
		CompletedAt: timestamptzToTimePtr(row.CompletedAt), Attempts: int(row.Attempts),
		QuotaReserved: row.QuotaReserved, LastError: row.LastError.String, CreatedAt: timestamptzToTime(row.CreatedAt),
		WorkPolicyName: row.WorkPolicyName.String, WorkPolicyRevision: row.WorkPolicyRevision.Int64,
		WorkKeyDigest: row.WorkKeyDigest, WorkFairnessDigest: row.WorkFairnessDigest,
		WorkFairnessLimit: int(row.WorkFairnessLimit.Int32), WorkExpiresAt: timestamptzToTimePtr(row.WorkExpiresAt),
		WorkSequence: row.WorkSequence.Int64, DeadlineAt: timestamptzToTimePtr(row.DeadlineAt),
		RetryPolicyJSON: row.RetryPolicy, ResultRetentionUntil: timestamptzToTimePtr(row.ResultRetentionUntil),
		LastReplayedAt:         timestamptzToTimePtr(row.LastReplayedAt),
		OnSuccessDestinationID: uuidString(row.OnSuccessDestinationID), OnFailureDestinationID: uuidString(row.OnFailureDestinationID),
	}
	if row.CronID.Valid {
		id := uuidString(row.CronID)
		inv.CronID = &id
	}
	if row.Outcome.Valid {
		outcome := InvocationOutcome(row.Outcome.String)
		inv.Outcome = &outcome
	}
	return inv
}
