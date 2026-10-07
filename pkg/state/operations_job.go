// adr: 664
package state

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// JobOperationAuthority contains a narrow runtime capability, never the host's
// lease token. It is valid only while task zero of this generation is leased.
type JobOperationAuthority struct {
	RunID      string
	InstanceID string
	Generation int
	Attempt    int
	Capability string `json:"-"`
}

func (JobOperationAuthority) String() string   { return "JobOperationAuthority{redacted}" }
func (JobOperationAuthority) GoString() string { return "JobOperationAuthority{redacted}" }

func (a JobOperationAuthority) LogValue() slog.Value { return slog.StringValue(a.String()) }

type JobOperationStore interface {
	OperationForJobRun(context.Context, string) (Operation, bool, error)
	OperationJobDispatchEnv(context.Context, string, string, string) (map[string]string, error)
	OperationJobControl(context.Context, string, JobOperationAuthority) (api.OperationJobControlResponse, error)
	ReportOperationJob(context.Context, string, JobOperationAuthority, string, api.OperationJobReportRequest) (Operation, error)
}

func operationJobCapability(runID, instanceID, lease string) string {
	digest := sha256.Sum256([]byte("gregale:customer-operation:job:v1\x00" + runID + "\x00" + instanceID + "\x00" + lease))
	return hex.EncodeToString(digest[:])
}
func validateOperationJobAuthority(op Operation, task JobTask, a JobOperationAuthority, now time.Time) error {
	if op.Generation < 1 || op.Generation > math.MaxInt32 || a.Attempt < 1 || a.Attempt > math.MaxInt32 {
		return ErrOperationStaleAttempt
	}
	if op.JobRunID != a.RunID || op.Generation != a.Generation || a.Attempt != task.Attempt || task.TaskIndex != 0 || task.Status != "claimed" || !operationIsActive(op) || task.InstanceID == nil || *task.InstanceID != a.InstanceID || task.LeaseToken == nil || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(now) || !operationJobDeadline(op, task).After(now) {
		return ErrOperationStaleAttempt
	}
	expected := operationJobCapability(task.RunID, *task.InstanceID, *task.LeaseToken)
	if len(a.Capability) != len(expected) || subtle.ConstantTimeCompare([]byte(a.Capability), []byte(expected)) != 1 {
		return ErrOperationStaleAttempt
	}
	return nil
}
func operationJobDispatchEnv(op Operation, task JobTask, instanceID, lease string, now time.Time) (map[string]string, error) {
	a := JobOperationAuthority{RunID: task.RunID, InstanceID: instanceID, Generation: op.Generation, Attempt: task.Attempt, Capability: operationJobCapability(task.RunID, instanceID, lease)}
	if err := validateOperationJobAuthority(op, task, a, now); err != nil {
		return nil, err
	}
	if op.CancellationRequested {
		return nil, ErrConflict
	}
	input, err := operations.CanonicalJSON(op.JobInput)
	if err != nil || len(input) > api.OperationJobInputMaxBytes {
		return nil, ErrInvalidArgument
	}
	return map[string]string{
		"GREGALE_CUSTOMER_OPERATION_ACCOUNT_ID":         op.AccountID,
		"GREGALE_CUSTOMER_OPERATION_APP_ID":             op.AppID,
		"GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID": op.PlatformTenantID,
		"GREGALE_CUSTOMER_OPERATION_SCOPE":              op.Scope,
		"GREGALE_CUSTOMER_OPERATION_ID":                 op.ID,
		"GREGALE_CUSTOMER_OPERATION_GENERATION":         fmt.Sprint(op.Generation),
		"GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY":     a.Capability,
		"GREGALE_CUSTOMER_OPERATION_INPUT":              string(input),
	}, nil
}
func prepareOperationJob(op *Operation, inv Invocation, job Job, plan api.Plan) (JobRun, error) {
	if job.AccountID != op.AccountID || job.Kind != "batch" || job.Status != "active" || job.ImageMaterializationStatus != "ready" || job.ImageResolvedDigest == "" || job.ImageStorageKey == "" || len(job.Command) == 0 {
		return JobRun{}, fmt.Errorf("%w: operation requires an active materialized batch job", ErrInvalidArgument)
	}
	if !plan.JobsAllowed() || job.RAMMB > api.JobRAMMB[plan.PlanIndex()] || job.TaskTimeoutS > api.JobTaskTimeoutSec[plan.PlanIndex()] {
		return JobRun{}, ErrConflict
	}
	if len(inv.Payload) > api.OperationJobInputMaxBytes {
		return JobRun{}, NewOperationLimitError("job_operation_input_bytes", api.OperationJobInputMaxBytes, int64(len(inv.Payload)))
	}
	var env map[string]string
	if err := json.Unmarshal(job.EnvOverrides, &env); err != nil {
		return JobRun{}, ErrInvalidArgument
	}
	if len(env) > api.OperationJobEnvMaxEntries {
		return JobRun{}, NewOperationLimitError("job_operation_environment_entries", api.OperationJobEnvMaxEntries, int64(len(env)))
	}
	for key, value := range env {
		if len(value) > api.OperationJobInputMaxBytes {
			return JobRun{}, ErrInvalidArgument
		}
		if len(key) >= len("GREGALE_CUSTOMER_OPERATION_") && key[:len("GREGALE_CUSTOMER_OPERATION_")] == "GREGALE_CUSTOMER_OPERATION_" {
			return JobRun{}, fmt.Errorf("%w: reserved operation environment variable", ErrInvalidArgument)
		}
	}
	retry, ram, timeout := 0, job.RAMMB, job.TaskTimeoutS
	run := JobRun{ID: newOperationID(), JobID: job.ID, AccountID: op.AccountID, TriggerKind: "manual", Tasks: 1, Parallelism: 1, RetryMax: &retry, TaskTimeoutS: &timeout, Command: append([]string(nil), job.Command...), EnvOverrides: json.RawMessage(`{}`), ImageRefSnapshot: job.ImageRef, ImageResolvedDigestSnapshot: job.ImageResolvedDigest, ImageStorageKeySnapshot: job.ImageStorageKey, RAMMBSnapshot: &ram, EffectiveEnvSnapshot: append(json.RawMessage(nil), job.EnvOverrides...), ExecutionClass: "standard", FailurePolicy: "continue", AggregateStatus: "queued", CreatedAt: op.CreatedAt}
	op.CurrentInvocationID, op.JobRunID = "", run.ID
	op.JobSnapshot = &run
	op.JobInput = append(json.RawMessage(nil), inv.Payload...)
	op.JobReports = map[string]string{}
	return run, nil
}
func operationJobEvent(op *Operation, task JobTask, kind string, data map[string]any, now time.Time) api.OperationEvent {
	data["job_run_id"], data["generation"] = task.RunID, op.Generation
	return operationEvent(op, Invocation{Attempts: task.Attempt}, kind, data, now)
}
func operationJobProjection(op *Operation, task JobTask, now time.Time) []api.OperationEvent {
	if op.JobRunID != task.RunID || op.State.Terminal() || op.State == api.OperationRequiresReconciliation {
		return nil
	}
	kind := ""
	switch task.Status {
	case "claimed":
		if op.State == api.OperationAccepted {
			op.State = api.OperationRunning
			kind = "running"
		}
	case "succeeded":
		if len(op.JobResultReceipt) > 0 {
			op.State, op.Result, kind = api.OperationSucceeded, append(json.RawMessage(nil), op.JobResultReceipt...), "succeeded"
		} else {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "job_result_missing", "reconciliation_required"
		}
	case "cancelled":
		if task.StartedAt == nil {
			op.State, kind = api.OperationCancelled, "cancelled"
		} else {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "execution_outcome_unknown", "reconciliation_required"
		}
	case "failed", "timeout", "oom":
		op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "execution_outcome_unknown", "reconciliation_required"
	}
	if kind == "" {
		return nil
	}
	if kind != "running" {
		op.ExpiresAt = now.Add(time.Duration(op.PlanLimits.ResultRetentionSeconds) * time.Second)
		op.EventExpiresAt = now.Add(time.Duration(op.PlanLimits.EventRetentionSeconds) * time.Second)
	}
	// Publish references and events with the final result retention window.
	refreshOperationArtifactExpiry(op)
	var events []api.OperationEvent
	if op.State == api.OperationSucceeded {
		events = publishJobArtifacts(op, task, false, now)
	}
	return append(events, operationJobEvent(op, task, kind, map[string]any{"state": op.State, "failure_code": op.FailureCode}, now))
}
func operationJobExecution(op Operation, task JobTask) api.OperationExecution {
	return api.OperationExecution{Generation: op.Generation, JobRunID: task.RunID, State: task.Status, Attempts: task.Attempt, CreatedAt: task.CreatedAt, CompletedAt: task.FinishedAt}
}
func operationJobReport(op *Operation, def OperationDefinition, task JobTask, kind string, report api.OperationJobReportRequest, now time.Time) (api.OperationEvent, bool, error) {
	if kind != "progress" && kind != "result" {
		return api.OperationEvent{}, false, ErrInvalidArgument
	}
	if len(report.ReportID) == 0 || len(report.ReportID) > api.OperationReportIDMaxBytes || strings.ContainsAny(report.ReportID, "\x00\r\n") {
		return api.OperationEvent{}, false, ErrInvalidArgument
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return api.OperationEvent{}, false, err
	}
	fingerprint, err := operations.InputFingerprint(raw)
	if err != nil {
		return api.OperationEvent{}, false, err
	}
	if prior, ok := op.JobReports[report.ReportID]; ok {
		if prior != kind+":"+fingerprint {
			return api.OperationEvent{}, false, ErrOperationInputConflict
		}
		return api.OperationEvent{}, false, nil
	}
	if op.CancellationRequested {
		return api.OperationEvent{}, false, ErrConflict
	}
	var event api.OperationEvent
	if kind == "progress" {
		if report.Progress == nil || len(report.Result) > 0 {
			return event, false, ErrInvalidArgument
		}
		progress := *report.Progress
		progress.ReportID = report.ReportID
		event, err = operationProgress(op, Invocation{Attempts: task.Attempt}, def, progress, op.PlanLimits, now)
	} else {
		if report.Progress != nil || len(op.JobResultReceipt) > 0 {
			return event, false, ErrOperationInputConflict
		}
		contract, e := operations.Compile(def.Spec, op.PlanLimits)
		if e != nil {
			return event, false, e
		}
		if e = contract.ValidateOutput(report.Result, op.ValueMaxBytes); e != nil {
			return event, false, fmt.Errorf("%w: %w", ErrInvalidArgument, e)
		}
		if op.ReportCount >= op.PlanLimits.ReportsPerOperation {
			return event, false, NewOperationLimitError("reports_per_operation", int64(op.PlanLimits.ReportsPerOperation), int64(op.ReportCount+1))
		}
		op.ReportCount++
		op.JobResultReceipt = append(json.RawMessage(nil), report.Result...)
		event = operationJobEvent(op, task, "result_prepared", map[string]any{"result_prepared": true}, now)
	}
	if err != nil {
		return event, false, err
	}
	if op.JobReports == nil {
		op.JobReports = map[string]string{}
	}
	op.JobReports[report.ReportID] = kind + ":" + fingerprint
	return event, true, nil
}

type OperationJobImageRetentionStore interface {
	OperationJobImageRetained(context.Context, string) (bool, error)
}

func operationJobDeadline(op Operation, task JobTask) time.Time {
	if task.StartedAt == nil || op.JobSnapshot == nil || op.JobSnapshot.TaskTimeoutS == nil {
		return time.Time{}
	}
	return task.StartedAt.Add(time.Duration(*op.JobSnapshot.TaskTimeoutS+api.OperationJobDispatchGraceSeconds) * time.Second)
}

func operationJobPolicyAvailable(op Operation, plan api.Plan) bool {
	snapshot := op.JobSnapshot
	return plan.JobsAllowed() && snapshot != nil && snapshot.RAMMBSnapshot != nil && snapshot.TaskTimeoutS != nil && *snapshot.RAMMBSnapshot > 0 && *snapshot.TaskTimeoutS > 0 && *snapshot.RAMMBSnapshot <= api.JobRAMMB[plan.PlanIndex()] && *snapshot.TaskTimeoutS <= api.JobTaskTimeoutSec[plan.PlanIndex()]
}
