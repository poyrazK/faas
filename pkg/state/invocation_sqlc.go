package state

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func invocationTimePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func invocationFromSQLC(row sqlc.Invocation) Invocation {
	inv := Invocation{
		ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID), EnvironmentID: pgUUIDString(row.EnvironmentID),
		Source: InvocationSource(row.Source), State: InvocationState(row.State), QueueName: row.QueueName, Method: row.Method, Path: row.Path,
		Payload: row.Payload, Headers: row.Headers, DueAt: row.DueAt.Time, ScheduledAt: invocationTimePointer(row.ScheduledAt),
		AckURL: row.AckUrl.String, Result: row.Result, LeaseExpiresAt: invocationTimePointer(row.LeaseExpiresAt),
		ReceivedAt: invocationTimePointer(row.ReceivedAt), CompletedAt: invocationTimePointer(row.CompletedAt),
		Attempts: int(row.Attempts), QuotaReserved: row.QuotaReserved, LastError: row.LastError.String, CreatedAt: row.CreatedAt.Time, InstanceID: row.InstanceID.String,
		DeadlineAt: invocationTimePointer(row.DeadlineAt), RetryPolicyJSON: row.RetryPolicy, ResultRetentionUntil: invocationTimePointer(row.ResultRetentionUntil),
		LastReplayedAt: invocationTimePointer(row.LastReplayedAt), OnSuccessDestinationID: pgUUIDString(row.OnSuccessDestinationID), OnFailureDestinationID: pgUUIDString(row.OnFailureDestinationID),
		WorkPolicyName: row.WorkPolicyName.String, WorkKeyDigest: row.WorkKeyDigest, WorkExpiresAt: invocationTimePointer(row.WorkExpiresAt),
		WorkSequence: row.WorkSequence.Int64, WorkPolicyRevision: row.WorkPolicyRevision.Int64, WorkFairnessDigest: row.WorkFairnessDigest, WorkFairnessLimit: int(row.WorkFairnessLimit.Int32),
	}
	if row.CronID.Valid {
		id := pgUUIDString(row.CronID)
		inv.CronID = &id
	}
	if row.Outcome.Valid {
		outcome := InvocationOutcome(row.Outcome.String)
		inv.Outcome = &outcome
	}
	return inv
}

func invocationsFromSQLC(rows []sqlc.Invocation) []Invocation {
	var out []Invocation // Preserve the historical nil empty due list.
	for _, row := range rows {
		out = append(out, invocationFromSQLC(row))
	}
	return out
}
