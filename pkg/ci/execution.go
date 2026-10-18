package ci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ExecutionQualification = "qualification"
	ExecutionJobs          = "jobs"
	StatusQueued           = "queued"
	StatusWaiting          = "waiting"
	StatusStarting         = "starting"
	StatusRunning          = "running"
	StatusCancelling       = "cancelling"
	StatusSucceeded        = "succeeded"
	StatusFailed           = "failed"
	StatusSkipped          = "skipped"
	ReasonTimeout          = "timeout"
	ReasonDependency       = "dependency_failed"
	ReasonCancelled        = "cancel_requested"
)

var (
	ErrLeaseBusy       = errors.New("CI execution lease is held")
	ErrLeaseLost       = errors.New("CI execution lease lost")
	ErrBackendProtocol = errors.New("invalid CI backend observation")
	ErrLogCursor       = errors.New("invalid CI log cursor or page size")
)

// Execution is separate from blocked metadata receipts. Jobs admission requires
// a fresh explicit request; qualification records never authorize compute.
type Execution struct {
	Rerun           *ExecutionRerun       `json:"rerun,omitempty"`
	Automation      *AutomationProvenance `json:"automation,omitempty"`
	ProfileID       string                `json:"profile_id,omitempty"`
	ID              string                `json:"id"`
	AccountID       string                `json:"account_id"`
	RevisionID      string                `json:"revision_id"`
	Pipeline        string                `json:"pipeline"`
	CommitSHA       string                `json:"commit_sha"`
	Mode            string                `json:"mode"`
	Status          string                `json:"status"`
	CancelRequested bool                  `json:"cancel_requested"`
	Plan            Plan                  `json:"plan"`
	Jobs            []JobExecution        `json:"jobs"`
	CreatedAt       time.Time             `json:"created_at"`
	FinishedAt      *time.Time            `json:"finished_at,omitempty"`
	Compute         *JobsExecution        `json:"compute,omitempty"`
}

type JobExecution struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason,omitempty"`
	Attempt *Attempt `json:"attempt,omitempty"`
}

// One attempt is permitted per job. Controller recovery always
// reuses its ID; it never retries user commands as a new attempt.
type Attempt struct {
	ID               string        `json:"id"`
	Number           int           `json:"number"`
	Status           string        `json:"status"`
	BackendStatus    string        `json:"backend_status,omitempty"`
	StartedAt        time.Time     `json:"started_at"`
	Deadline         time.Time     `json:"deadline"`
	FinishedAt       *time.Time    `json:"finished_at,omitempty"`
	CancelReason     string        `json:"cancel_reason,omitempty"`
	Steps            []StepResult  `json:"steps"`
	ExitCode         *int          `json:"exit_code,omitempty"`
	Logs             []byte        `json:"logs,omitempty"`
	LogTruncated     bool          `json:"log_truncated"`
	OutputIncomplete bool          `json:"output_incomplete,omitempty"`
	Artifacts        []ArtifactRef `json:"artifacts,omitempty"`
	TestReports      []TestReport  `json:"test_reports,omitempty"`
}

type StepResult struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// ArtifactRef contains metadata only, never an upload URL, credential, host path,
// or artifact body. BlobJobsOutput verifies existing payloads and ownership;
// guest publication, grant revocation and downloads still need qualification.
type ArtifactRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

// ExecutionStore is deliberately separate from the metadata Store exposed to
// apid. Its private lease operations keep all state transitions in this package.
type ExecutionStore interface {
	SubmitQualification(context.Context, string, string, SubmitRequest) (Execution, bool, error)
	Execution(context.Context, string, string) (Execution, error)
	CancelExecution(context.Context, string, string) (Execution, error)
	claimExecution(context.Context, string, string) (executionLease, error)
	checkExecutionLease(context.Context, executionLease) error
	finishExecutionTick(context.Context, executionLease, []attemptObservation) (Execution, error)
}

type executionRecord struct {
	Execution Execution
	Version   int64
	Fence     int64
	Until     time.Time
}

type executionLease struct {
	executionRecord
}

func terminal(status string) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusCancelled || status == StatusSkipped
}

func newExecution(revision Revision, req SubmitRequest, now time.Time) (Execution, error) {
	plan, _, err := Compile(revision.Definition)
	if err != nil {
		return Execution{}, err
	}
	if plan.DefinitionDigest != revision.Digest {
		return Execution{}, fmt.Errorf("CI revision digest mismatch")
	}
	out := Execution{ID: uuid.NewString(), AccountID: revision.AccountID, RevisionID: revision.ID,
		Pipeline: revision.Name, CommitSHA: req.CommitSHA, Mode: ExecutionQualification,
		Status: StatusQueued, Plan: plan, CreatedAt: now, Jobs: make([]JobExecution, len(plan.Jobs))}
	for i, job := range plan.Jobs {
		out.Jobs[i] = JobExecution{Name: job.Name, Status: StatusWaiting}
	}
	return out, nil
}

// prepareExecution persists all start/cancel intents BEFORE any backend call.
// This makes a crash between Start and its receipt recoverable by attempt ID.
func prepareExecution(row *Execution, now time.Time) {
	states := make(map[string]string, len(row.Jobs))
	for i := range row.Jobs {
		job, plan := &row.Jobs[i], row.Plan.Jobs[i]
		if job.Status == StatusWaiting {
			switch {
			case row.CancelRequested:
				job.Status, job.Reason = StatusCancelled, ReasonCancelled
			case dependencyFailed(plan.Needs, states):
				job.Status, job.Reason = StatusSkipped, ReasonDependency
			case dependenciesSucceeded(plan.Needs, states):
				job.Status = StatusStarting
				job.Attempt = &Attempt{ID: uuid.NewString(), Number: 1, Status: StatusStarting,
					StartedAt: now, Deadline: now.Add(time.Duration(plan.TimeoutSeconds) * time.Second), Steps: make([]StepResult, len(plan.Steps))}
				for j, step := range plan.Steps {
					job.Attempt.Steps[j] = StepResult{Name: step.Name, Status: StatusQueued}
				}
			}
		}
		if job.Attempt != nil && !terminal(job.Status) {
			a := job.Attempt
			if row.CancelRequested {
				a.CancelReason = ReasonCancelled
			} else if !terminal(a.BackendStatus) && !now.Before(a.Deadline) && a.CancelReason == "" {
				a.CancelReason = ReasonTimeout
			}
			if a.CancelReason != "" {
				job.Status, a.Status = StatusCancelling, StatusCancelling
			}
		}
		states[job.Name] = job.Status
	}
	settleExecution(row, now)
}

func dependencyFailed(needs []string, states map[string]string) bool {
	for _, name := range needs {
		if states[name] == StatusFailed || states[name] == StatusCancelled || states[name] == StatusSkipped {
			return true
		}
	}
	return false
}

func dependenciesSucceeded(needs []string, states map[string]string) bool {
	for _, name := range needs {
		if states[name] != StatusSucceeded {
			return false
		}
	}
	return true
}

func settleExecution(row *Execution, now time.Time) {
	allDone, failed := true, false
	for _, job := range row.Jobs {
		allDone = allDone && terminal(job.Status)
		failed = failed || job.Status == StatusFailed || job.Status == StatusSkipped || job.Status == StatusCancelled
	}
	row.Status = StatusRunning
	if row.CancelRequested {
		row.Status = StatusCancelling
	}
	if allDone {
		switch {
		case row.CancelRequested:
			row.Status = StatusCancelled
		case failed:
			row.Status = StatusFailed
		default:
			row.Status = StatusSucceeded
		}
		row.FinishedAt = &now
	}
}

func cancelExecution(row *Execution, now time.Time) {
	if terminal(row.Status) || row.CancelRequested {
		return
	}
	row.CancelRequested = true
	prepareExecution(row, now)
}

func validLease(row executionRecord, lease executionLease, now time.Time) bool {
	return row.Version == lease.Version && row.Fence == lease.Fence && now.Before(row.Until) && !terminal(row.Execution.Status)
}

func claimExecutionRecord(row *executionRecord, now time.Time) error {
	if terminal(row.Execution.Status) {
		return nil
	}
	if now.Before(row.Until) {
		return ErrLeaseBusy
	}
	prepareExecution(&row.Execution, now)
	row.Version++
	row.Fence++
	row.Until = time.Time{}
	if !terminal(row.Execution.Status) {
		row.Until = now.Add(time.Duration(api.CIExecutionLeaseSeconds) * time.Second)
	}
	return nil
}

func finishExecutionRecord(row *executionRecord, lease executionLease, observations []attemptObservation, now time.Time) error {
	if !validLease(*row, lease, now) {
		return ErrLeaseLost
	}
	if err := applyObservations(&row.Execution, observations, now); err != nil {
		return err
	}
	row.Version++
	row.Until = time.Time{}
	return nil
}

func cancelExecutionRecord(row *executionRecord, now time.Time) {
	if terminal(row.Execution.Status) || row.Execution.CancelRequested {
		return
	}
	cancelExecution(&row.Execution, now)
	row.Version++ // invalidates a controller's in-flight observations.
	if terminal(row.Execution.Status) {
		row.Until = time.Time{}
	}
}

type LogPage struct {
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	Data       []byte `json:"data"`
	Truncated  bool   `json:"truncated"`
	Complete   bool   `json:"complete"`
}

// ExecutionLogs returns byte offsets in the retained prefix. Raw bytes are JSON
// base64 encoded; cursors are never character offsets. Truncation is explicit.
func ExecutionLogs(ctx context.Context, store ExecutionStore, account, id, attemptID string, offset, limit int) (LogPage, error) {
	if offset < 0 || limit < 1 || limit > api.CILogPageMaxBytes {
		return LogPage{}, ErrLogCursor
	}
	row, err := store.Execution(ctx, account, id)
	if err != nil {
		return LogPage{}, err
	}
	for _, job := range row.Jobs {
		if a := job.Attempt; a != nil && a.ID == attemptID {
			if offset > len(a.Logs) {
				return LogPage{}, ErrLogCursor
			}
			end := min(len(a.Logs), offset+limit)
			return LogPage{Offset: offset, NextOffset: end, Data: a.Logs[offset:end], Truncated: a.LogTruncated,
				Complete: terminal(a.Status) && end == len(a.Logs)}, nil
		}
	}
	return LogPage{}, ErrNotFound
}
