package state

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func invocationTimePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func invocationFromSQLC(row sqlc.Invocation) (Invocation, error) {
	inv := Invocation{
		ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID), EnvironmentID: pgUUIDString(row.EnvironmentID),
		DeploymentScope: row.DeploymentScope, QueueBindingID: pgUUIDString(row.QueueBindingID), ReplayGeneration: row.ReplayGeneration,
		PlatformTenantID: pgUUIDString(row.PlatformTenantID), OccurrenceID: pgUUIDString(row.OccurrenceID),
		StartDeadlineAt: invocationTimePointer(row.StartDeadlineAt), OutcomeCode: row.OutcomeCode,
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
	if err := decodeInvocationWorkPolicy(&inv, row.FailureRules, row.WorkDecision); err != nil {
		return Invocation{}, err
	}
	return inv, nil
}

func invocationsFromSQLC(rows []sqlc.Invocation) ([]Invocation, error) {
	var out []Invocation // Preserve the historical nil empty due list.
	for _, row := range rows {
		inv, err := invocationFromSQLC(row)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

// Raw and SQLC row readers must retain the same scheduled-work decisions.
func decodeInvocationWorkPolicy(inv *Invocation, failureRules, workDecision []byte) error {
	if len(failureRules) > 0 {
		var rules workpolicy.FailureRules
		if err := json.Unmarshal(failureRules, &rules); err != nil {
			return fmt.Errorf("state: decode invocation failure rules: %w", err)
		}
		inv.FailureRules = &rules
	}
	if len(workDecision) > 0 {
		var decision workpolicy.Decision
		if err := json.Unmarshal(workDecision, &decision); err != nil {
			return fmt.Errorf("state: decode invocation work decision: %w", err)
		}
		inv.WorkDecision = &decision
	}
	return nil
}
