package ci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AttemptKey is the durable external identity. A backend MUST atomically fence
// lower Fence values, deduplicate Start by this identity, and never restart a
// terminal attempt. Cancel MUST persist a tombstone even when it precedes Start;
// for a running task it reports terminal only after confirming the task stopped.
// Losing a controller lease does not authorize another attempt or repeat work.
type AttemptKey struct {
	AccountID   string
	ExecutionID string
	JobName     string
	AttemptID   string
	Fence       int64
}

type StartRequest struct {
	Key        AttemptKey
	Repository string
	CommitSHA  string
	Job        PlannedJob
	Deadline   time.Time
}

type ObserveRequest struct {
	Key       AttemptKey
	LogOffset int
	LogLimit  int
}

// Observation contains ordered step progress and one bounded log page. A backend
// must retain output for replay from LogOffset after controller failure. Terminal
// status certifies the task has stopped. MoreLogs delays completion until output
// is drained or the retained-prefix bound is reached. Artifacts are collected on
// failure/cancellation too; only complete terminal manifests are accepted.
type Observation struct {
	Status           string
	Steps            []StepResult
	ExitCode         *int
	LogOffset        int
	Logs             []byte
	MoreLogs         bool
	Artifacts        []ArtifactRef
	TestReports      []TestReport
	OutputIncomplete bool
}

// Backend is a compute-owner contract, not a host command runner.
// Implementations must honor ctx deadlines,
// account ownership, stable attempt IDs, and the fencing/tombstone rules above.
type Backend interface {
	Start(context.Context, StartRequest) error
	Observe(context.Context, ObserveRequest) (Observation, error)
	Cancel(context.Context, AttemptKey) error
}

type Engine struct {
	Store   ExecutionStore
	Backend Backend
}

type attemptObservation struct {
	ID string
	Observation
}

// Tick performs one bounded reconciliation.
// All start intents are durable before external calls; external errors are
// retried with the same attempt identity, never as new user-command attempts.
func (e Engine) Tick(ctx context.Context, account, id string) (Execution, error) {
	if e.Store == nil || e.Backend == nil {
		return Execution{}, fmt.Errorf("CI execution requires an explicit store and backend")
	}
	lease, err := e.Store.claimExecution(ctx, account, id)
	if err != nil {
		return Execution{}, err
	}
	return e.reconcile(ctx, lease)
}

func (e Engine) reconcile(ctx context.Context, lease executionLease) (Execution, error) {
	if terminal(lease.Execution.Status) {
		return lease.Execution, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIExecutionLeaseSeconds)*time.Second)
	defer cancel()
	observations, backendErr := e.observeJobs(ctx, lease)
	row, saveErr := e.Store.finishExecutionTick(ctx, lease, observations)
	return row, errors.Join(backendErr, saveErr)
}

func (e Engine) observeJobs(ctx context.Context, lease executionLease) ([]attemptObservation, error) {
	row := lease.Execution
	observations := make([]attemptObservation, 0, len(row.Jobs))
	for i, job := range row.Jobs {
		if job.Attempt == nil || terminal(job.Status) {
			continue
		}
		if err := e.Store.checkExecutionLease(ctx, lease); err != nil {
			return observations, err
		}
		a := job.Attempt
		key := AttemptKey{AccountID: row.AccountID, ExecutionID: row.ID, JobName: job.Name, AttemptID: a.ID, Fence: lease.Fence}
		err := e.ensureIntent(ctx, row, row.Plan.Jobs[i], a, key)
		var observation Observation
		if errors.Is(err, ErrArtifactInput) && a.CancelReason == "" {
			observation = unavailableArtifactInputs(row.Plan.Jobs[i])
		} else {
			if err != nil {
				return observations, err
			}
			observation, err = e.Backend.Observe(ctx, ObserveRequest{Key: key, LogOffset: len(a.Logs), LogLimit: logLimit(a)})
			if err != nil {
				return observations, fmt.Errorf("observe CI attempt %s: %w", a.ID, err)
			}
		}
		observation = cancelledSteps(*a, observation)
		observation = completeObservationReports(row.Plan.Jobs[i], observation)
		if err := validateObservation(row.Plan.Jobs[i], *a, observation); err != nil {
			return observations, err
		}
		observations = append(observations, attemptObservation{ID: a.ID, Observation: observation})
	}
	return observations, nil
}

func (e Engine) ensureIntent(ctx context.Context, row Execution, plan PlannedJob, a *Attempt, key AttemptKey) error {
	if terminal(a.BackendStatus) {
		return nil // task already stopped; only drain retained output.
	}
	if a.CancelReason != "" {
		if err := e.Backend.Cancel(ctx, key); err != nil {
			return fmt.Errorf("cancel CI attempt %s: %w", a.ID, err)
		}
		return nil
	}
	err := e.Backend.Start(ctx, StartRequest{Key: key, Repository: row.Plan.Repository,
		CommitSHA: row.CommitSHA, Job: clone(plan), Deadline: a.Deadline})
	if err != nil {
		return fmt.Errorf("start CI attempt %s: %w", a.ID, err)
	}
	return nil
}

func logLimit(a *Attempt) int {
	return min(api.CILogPageMaxBytes, api.CILogMaxBytesPerAttempt-len(a.Logs))
}

func applyObservations(row *Execution, observations []attemptObservation, now time.Time) error {
	byID := make(map[string]Observation, len(observations))
	for _, observation := range observations {
		if _, exists := byID[observation.ID]; exists {
			return fmt.Errorf("%w: repeated attempt", ErrBackendProtocol)
		}
		byID[observation.ID] = observation.Observation
	}
	for i := range row.Jobs {
		job := &row.Jobs[i]
		a := job.Attempt
		if a == nil {
			continue
		}
		report, ok := byID[a.ID]
		if !ok {
			continue
		}
		delete(byID, a.ID)
		report = cancelledSteps(*a, report)
		report = completeObservationReports(row.Plan.Jobs[i], report)
		if terminal(job.Status) {
			return fmt.Errorf("%w: attempt already finished", ErrBackendProtocol)
		}
		if err := validateObservation(row.Plan.Jobs[i], *a, report); err != nil {
			return err
		}
		if !terminal(a.BackendStatus) && !now.Before(a.Deadline) && a.CancelReason == "" {
			a.CancelReason = ReasonTimeout
		}
		a.Steps, a.ExitCode, a.BackendStatus = clone(report.Steps), clone(report.ExitCode), report.Status
		a.Logs = append(a.Logs, report.Logs...)
		a.LogTruncated = a.LogTruncated || (len(a.Logs) == api.CILogMaxBytesPerAttempt && report.MoreLogs)
		a.OutputIncomplete = a.OutputIncomplete || report.OutputIncomplete
		if terminal(report.Status) {
			a.Artifacts = clone(report.Artifacts)
			a.TestReports = clone(report.TestReports)
		}
		if !terminal(report.Status) || (report.MoreLogs && !a.LogTruncated) {
			job.Status, a.Status = StatusRunning, StatusRunning
			if a.CancelReason != "" {
				job.Status, a.Status = StatusCancelling, StatusCancelling
			}
			continue
		}
		a.FinishedAt = &now
		job.Status, a.Status = report.Status, report.Status
		if a.CancelReason == ReasonTimeout {
			job.Status, a.Status, job.Reason = StatusFailed, StatusFailed, ReasonTimeout
		} else if row.CancelRequested || report.Status == StatusCancelled {
			job.Status, a.Status, job.Reason = StatusCancelled, StatusCancelled, ReasonCancelled
		}
	}
	if len(byID) != 0 {
		return fmt.Errorf("%w: unknown attempt", ErrBackendProtocol)
	}
	settleExecution(row, now)
	return nil
}
