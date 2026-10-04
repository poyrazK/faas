package state

// adr: 491
import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Convert the atomic claim response without rereading a possibly cancelled or
// reassigned task. All original occurrence, exclusive owner and outcome pins
// must survive the quota-aware selector.
func appTaskFromQuotaClaim(row sqlc.AppTask) (AppTask, error) {
	task := AppTask{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID),
		Kind: AppTaskKind(row.Kind), Command: append([]string(nil), row.Command...), CommandShell: row.CommandShell, DeploymentScope: row.DeploymentScope,
		ArtifactKey: row.ArtifactKey, ImageDigest: row.ImageDigest, Status: AppTaskStatus(row.Status), TimeoutSeconds: int(row.TimeoutSeconds), MaxOutputBytes: int(row.MaxOutputBytes),
		RetryMax: int(row.RetryMax), RetryBackoffSeconds: int(row.RetryBackoffSeconds), AttemptCount: int(row.AttemptCount), RetryAt: timestamptzToTimePtr(row.RetryAt),
		LeaseToken: executionUUIDPtr(row.LeaseToken), LeaseOwner: executionStringPtr(row.LeaseOwner), LeaseExpiresAt: timestamptzToTimePtr(row.LeaseExpiresAt), CancelRequested: timestamptzToTimePtr(row.CancelRequestedAt),
		StdoutTail: row.StdoutTail, StderrTail: row.StderrTail, OutputTruncated: row.OutputTruncated, ExitCode: executionIntPtr(row.ExitCode), FailureCode: executionStringPtr(row.FailureCode), FailureMessage: executionStringPtr(row.FailureMessage),
		StartedAt: timestamptzToTimePtr(row.StartedAt), FinishedAt: timestamptzToTimePtr(row.FinishedAt), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		CronID: pgUUIDString(row.CronID), ScheduledFor: timestamptzToTimePtr(row.ScheduledFor), OccurrenceID: pgUUIDString(row.OccurrenceID), StartDeadlineAt: timestamptzToTimePtr(row.StartDeadlineAt),
		OutcomeCode: row.OutcomeCode, ExclusiveOperationID: pgUUIDString(row.ExclusiveOperationID),
	}
	if row.ExclusiveGeneration.Valid {
		task.ExclusiveGeneration = row.ExclusiveGeneration.Int64
	}
	for _, field := range []struct {
		raw []byte
		out any
	}{{row.FailureRules, &task.FailureRules}, {row.WorkDecision, &task.WorkDecision}} {
		if len(field.raw) > 0 {
			if err := json.Unmarshal(field.raw, field.out); err != nil {
				return AppTask{}, fmt.Errorf("state: decode quota-aware app task claim: %w", err)
			}
		}
	}
	return task, nil
}
