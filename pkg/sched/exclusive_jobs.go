package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

const exclusiveJobIncarnationPrefix = "job-operation/"

type exclusiveJobRunWorkRequest struct {
	Kind        string                  `json:"kind"`
	TriggerKind string                  `json:"trigger_kind,omitempty"`
	Run         api.CreateJobRunRequest `json:"run"`
}

func exclusiveJobScheduleIdempotencyKey(jobID, schedule, timezone string, occurrence time.Time) string {
	digest := sha256.Sum256([]byte(jobID + "\x00" + schedule + "\x00" + timezone + "\x00" + occurrence.UTC().Format(time.RFC3339Nano)))
	return "job-schedule:" + hex.EncodeToString(digest[:])
}

func exclusiveJobRunID(operationID string, generation int64) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("gregale/job-operation/%s/%d", operationID, generation))).String()
}

func (d *Drain) dispatchExclusiveJobOperation(ctx context.Context, owners state.ExclusiveWorkStore, op state.ExclusiveOperation) string {
	var accepted exclusiveJobRunWorkRequest
	if err := json.Unmarshal(op.Request, &accepted); err != nil || accepted.Kind != "job_run" || accepted.Run.Tasks < 1 {
		_ = owners.FailPendingExclusiveOperation(ctx, op.AccountID, op.ID, "accepted Job run request is invalid")
		return "failed"
	}
	job, err := d.store.JobGetByID(ctx, op.JobID)
	if err != nil || job.AccountID != op.AccountID || job.Status != "active" {
		_ = owners.FailPendingExclusiveOperation(ctx, op.AccountID, op.ID, "Job is unavailable")
		return "failed"
	}
	claim, err := owners.ClaimExclusiveOperation(ctx, op.AccountID, op.ID, exclusiveJobIncarnationPrefix+op.ID)
	if err != nil {
		if errors.Is(err, state.ErrQuotaExceeded) {
			_ = owners.DeferPendingExclusiveOperation(ctx, op.AccountID, op.ID, "account async capacity is full")
			return "retry"
		}
		if errors.Is(err, exclusivework.ErrBusy) {
			return "retry"
		}
		return "failed"
	}

	// Recovery may leave a previous generation's guest running. Cancel its
	// durable run before making the replacement runnable; storage fences every
	// old task result independently, even if guest shutdown is delayed.
	oldRuns, err := d.store.JobRunListByExclusiveOperation(ctx, op.AccountID, op.ID)
	if err != nil {
		_ = owners.RetryExclusiveOperation(ctx, claim, "could not inspect prior JobRun incarnations")
		return "retry"
	}
	for _, old := range oldRuns {
		if old.ExclusiveGeneration >= claim.Generation || isTerminalRunStatus(old.AggregateStatus) {
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, cancelErr := d.engine.CancelJob(cleanupCtx, op.AccountID, old.ID)
		cancel()
		if cancelErr != nil && !errors.Is(cancelErr, ErrJobRunTerminal) && !errors.Is(cancelErr, state.ErrNotFound) {
			d.log.WarnContext(ctx, "exclusive Job operation could not cancel stale run", "operation_id", op.ID, "run_id", old.ID, "err", cancelErr)
		}
	}

	request := accepted.Run
	if request.ExecutionClass == "" {
		request.ExecutionClass = "standard"
	}
	if request.FailurePolicy == "" {
		request.FailurePolicy = "continue"
	}
	if len(request.EnvOverrides) == 0 {
		request.EnvOverrides = map[string]string{}
	}
	triggerKind := accepted.TriggerKind
	if triggerKind == "" {
		triggerKind = "manual"
	}
	env, err := json.Marshal(request.EnvOverrides)
	if err != nil {
		_ = owners.FailExclusiveOperation(ctx, claim, "accepted Job environment is invalid")
		return "failed"
	}
	inputs := make([]state.JobInput, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		inputs = append(inputs, state.JobInput{ID: input.ID, Ref: input.Ref})
	}
	eligibleAt, latestStartAt := request.EligibleAt, request.LatestStartAt
	if request.ExecutionClass == "flexible" && eligibleAt == nil {
		now := d.now().UTC()
		eligibleAt = &now
	}
	runID := exclusiveJobRunID(op.ID, claim.Generation)
	run, _, err := d.store.JobRunCreate(ctx, op.JobID, op.AccountID, triggerKind,
		request.Parallelism, request.RetryMax, request.TaskTimeoutSec, env, request.Tasks,
		state.JobRunOptions{
			ID: runID, ExclusiveOperationID: op.ID, ExclusiveGeneration: claim.Generation,
			CommandArgs: request.Arguments, Inputs: inputs,
			InputManifestURI: request.InputManifestURI, InputManifestSHA256: request.InputManifestSHA256,
			ExecutionClass: request.ExecutionClass, EligibleAt: eligibleAt, LatestStartAt: latestStartAt,
			FailurePolicy: request.FailurePolicy,
		})
	if err != nil {
		if errors.Is(err, exclusivework.ErrStaleOwner) {
			return "lost_owner"
		}
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) {
			_ = owners.FailExclusiveOperation(ctx, claim, "JobRun could not be created")
			return "failed"
		}
		_ = owners.RetryExclusiveOperation(ctx, claim, "JobRun creation is temporarily unavailable")
		return "retry"
	}
	if d.notifier != nil {
		payload, _ := json.Marshal(map[string]string{"kind": "run_created", "job_id": job.ID, "run_id": run.ID, "account_id": op.AccountID})
		_ = d.notifier.Notify(ctx, "job_changed", string(payload))
	}
	go d.watchExclusiveJobOperation(ctx, owners, claim, run.ID)
	return "completed"
}

func (d *Drain) watchExclusiveJobOperation(ctx context.Context, owners state.ExclusiveWorkStore, initial exclusivework.Claim, runID string) {
	claim := initial
	var claimMu sync.Mutex
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	renewalFailure := make(chan error, 1)
	interval := initial.ExpiresAt.Sub(d.now()) / 3
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	go func() {
		defer close(renewalDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenewal:
				return
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				claimMu.Lock()
				current := claim
				claimMu.Unlock()
				next, err := owners.RenewExclusiveOperation(workerCtx, current)
				if err != nil {
					if d.ops != nil {
						outcome := "error"
						if errors.Is(err, exclusivework.ErrStaleOwner) {
							outcome = "lost"
						}
						d.ops.ObserveExclusiveOperationLeaseRenewal(outcome)
					}
					select {
					case renewalFailure <- err:
					default:
					}
					cancel()
					return
				}
				if d.ops != nil {
					d.ops.ObserveExclusiveOperationLeaseRenewal("renewed")
				}
				claimMu.Lock()
				claim = next
				claimMu.Unlock()
			}
		}
	}()

	poll := time.NewTicker(min(interval, time.Second))
	defer poll.Stop()
	for {
		select {
		case err := <-renewalFailure:
			if !errors.Is(err, exclusivework.ErrStaleOwner) {
				d.log.WarnContext(ctx, "exclusive Job operation lease renewal failed", "operation_id", initial.OperationID, "err", err)
			}
			d.cancelExclusiveJobRun(ctx, initial.AccountID, runID)
			return
		case <-workerCtx.Done():
			return
		case <-poll.C:
			run, err := d.store.JobRunGetByID(workerCtx, runID)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					close(stopRenewal)
					<-renewalDone
					claimMu.Lock()
					current := claim
					claimMu.Unlock()
					_ = owners.FailExclusiveOperation(ctx, current, "managed JobRun disappeared")
					return
				}
				d.log.WarnContext(ctx, "exclusive Job operation run lookup failed", "operation_id", initial.OperationID, "err", err)
				continue
			}
			if !isTerminalRunStatus(run.AggregateStatus) {
				continue
			}
			close(stopRenewal)
			<-renewalDone
			claimMu.Lock()
			current := claim
			claimMu.Unlock()
			if run.AggregateStatus == "succeeded" {
				result, _ := json.Marshal(map[string]any{
					"job_run_id": run.ID, "aggregate_status": run.AggregateStatus,
					"tasks": run.Tasks, "tasks_succeeded": run.TasksSucceeded,
					"tasks_failed": run.TasksFailed, "tasks_cancelled": run.TasksCancelled,
				})
				if len(result) > api.MaxExclusiveResultBytes {
					_ = owners.FailExclusiveOperation(ctx, current, "managed Job result exceeded platform limit")
					return
				}
				if err := owners.CommitExclusiveOperation(ctx, current, result, nil); err != nil && !errors.Is(err, exclusivework.ErrStaleOwner) {
					d.log.WarnContext(ctx, "exclusive Job operation result commit failed", "operation_id", initial.OperationID, "err", err)
				}
				return
			}
			_ = owners.FailExclusiveOperation(ctx, current, "managed JobRun finished with status "+run.AggregateStatus)
			return
		}
	}
}

func (d *Drain) cancelExclusiveJobRun(ctx context.Context, accountID, runID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := d.engine.CancelJob(cleanupCtx, accountID, runID); err != nil && !errors.Is(err, ErrJobRunTerminal) && !errors.Is(err, state.ErrNotFound) {
		d.log.WarnContext(ctx, "exclusive Job operation cleanup failed", "run_id", runID, "err", err)
	}
}
