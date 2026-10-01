package state

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// appTaskFromSQLC preserves the task's immutable deployment, policy and lease
// pins when the generated claim/dispatch queries return its complete row.
func appTaskFromSQLC(row sqlc.AppTask) (AppTask, error) {
	task := AppTask{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
		DeploymentID: pgUUIDString(row.DeploymentID), Kind: AppTaskKind(row.Kind), Status: AppTaskStatus(row.Status),
		Command: row.Command, CommandShell: row.CommandShell, DeploymentScope: row.DeploymentScope,
		ArtifactKey: row.ArtifactKey, ImageDigest: row.ImageDigest,
		TimeoutSeconds: int(row.TimeoutSeconds), MaxOutputBytes: int(row.MaxOutputBytes),
		RetryMax: int(row.RetryMax), RetryBackoffSeconds: int(row.RetryBackoffSeconds), AttemptCount: int(row.AttemptCount),
		RetryAt: timestamptzToTimePtr(row.RetryAt), LeaseToken: executionUUIDPtr(row.LeaseToken),
		LeaseOwner: executionStringPtr(row.LeaseOwner), LeaseExpiresAt: timestamptzToTimePtr(row.LeaseExpiresAt),
		CancelRequested: timestamptzToTimePtr(row.CancelRequestedAt), StdoutTail: row.StdoutTail, StderrTail: row.StderrTail,
		OutputTruncated: row.OutputTruncated, ExitCode: executionIntPtr(row.ExitCode),
		FailureCode: executionStringPtr(row.FailureCode), FailureMessage: executionStringPtr(row.FailureMessage),
		StartedAt: timestamptzToTimePtr(row.StartedAt), FinishedAt: timestamptzToTimePtr(row.FinishedAt),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		ScheduledFor: timestamptzToTimePtr(row.ScheduledFor), StartDeadlineAt: timestamptzToTimePtr(row.StartDeadlineAt),
		OutcomeCode: row.OutcomeCode,
	}
	if row.CronID.Valid {
		task.CronID = pgUUIDString(row.CronID)
	}
	if row.OccurrenceID.Valid {
		task.OccurrenceID = pgUUIDString(row.OccurrenceID)
	}
	if len(row.FailureRules) > 0 {
		if err := json.Unmarshal(row.FailureRules, &task.FailureRules); err != nil {
			return AppTask{}, err
		}
	}
	if len(row.WorkDecision) > 0 {
		if err := json.Unmarshal(row.WorkDecision, &task.WorkDecision); err != nil {
			return AppTask{}, err
		}
	}
	return task, nil
}
