package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// ScheduleOccurrence is the durable decision record for one scheduled time.
// Its policy snapshot and reason make a skipped, late, or replaced occurrence
// explainable after the schedule itself has changed.
type ScheduleOccurrence struct {
	WorkDecision         *workpolicy.Decision
	OutcomeCode          string
	ID                   string
	AccountID            string
	CronID               string
	JobID                string
	ScheduleRevision     int64
	ScheduledFor         time.Time
	StartDeadlineAt      *time.Time
	SchedulePolicy       workpolicy.SchedulePolicy
	Status               string
	Reason               string
	BlockingOccurrenceID string
	ExclusiveOperationID string
	InvocationID         string
	AppTaskID            string
	JobRunID             string
	StartedAt            *time.Time
	FinishedAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// InvocationWorkClassification is attached atomically to a terminal or retry
// transition for an invocation governed by explicit FailureRules.
type InvocationWorkClassification struct {
	Decision    *workpolicy.Decision
	OutcomeCode string
}

// ClassifiedInvocationCompletionStore persists a confirmed application
// result and its policy decision with invocation completion.
type ClassifiedInvocationCompletionStore interface {
	CompleteInvocationWithWorkClassification(ctx context.Context, id string, result json.RawMessage, decision workpolicy.Decision, outcomeCode string) error
}

// JobScheduledOccurrenceOptions pins the nominal occurrence time and the
// candidate revision observed by the scheduler. The store rereads all policy
// fields under lock before committing any outcome.
type JobScheduledOccurrenceOptions struct {
	ScheduledFor     time.Time
	ScheduleRevision int64
	Disposition      string
	Reason           string
}

// CronScheduledOccurrenceOptions pins the nominal cron boundary, candidate
// schedule revision, and any scheduler-side missed-run disposition. Empty
// Disposition asks the store to evaluate deadline and overlap policy.
type CronScheduledOccurrenceOptions struct {
	ScheduledFor     time.Time
	ScheduleRevision int64
	Disposition      string
	Reason           string
	// ExclusiveAdmission opts a command cron into the managed operation lane.
	// Admission and the occurrence cursor commit in the same store transaction.
	ExclusiveAdmission *ExclusiveAdmission
}

type JobScheduleOccurrenceStore interface {
	JobRunCreateScheduledOccurrence(ctx context.Context, jobID, schedule, timezone string, expectedLastScheduledAt *time.Time, firedAt time.Time, options JobScheduledOccurrenceOptions) (JobRun, bool, error)
}

type ScheduleOccurrenceHistoryStore interface {
	ScheduleOccurrenceListByJob(ctx context.Context, jobID string, limit int, before string) ([]ScheduleOccurrence, error)
	ScheduleOccurrenceListByCron(ctx context.Context, cronID string, limit int, before string) ([]ScheduleOccurrence, error)
}

// ScheduledCronInvocationStore creates an HTTP-Cron occurrence and its
// pending synthetic invocation in one transaction, or records a policy
// decision without creating an invocation.
type ScheduledCronInvocationStore interface {
	CreateScheduledCronInvocationOccurrence(ctx context.Context, cronID string, expectedLastFiredAt *time.Time, evaluatedAt time.Time, options CronScheduledOccurrenceOptions, invocation Invocation) (Invocation, ScheduleOccurrence, bool, error)
}

// ScheduledInvocationDeadlineStore settles scheduled invocations that never
// started before their first-start deadline. Claim paths still enforce the
// deadline atomically, so this sweep only writes the durable terminal outcome.
type ScheduledInvocationDeadlineStore interface {
	ExpireUnstartedScheduledCronInvocations(ctx context.Context, now time.Time, limit int) (int, error)
}
