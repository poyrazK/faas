package state

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func operationClaim(op *Operation, inv Invocation, now time.Time) (string, api.OperationEvent, error) {
	if op.CurrentInvocationID != inv.ID || (op.State != api.OperationAccepted && op.State != api.OperationRunning) {
		return "", api.OperationEvent{}, ErrOperationStaleAttempt
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", api.OperationEvent{}, err
	}
	capability := hex.EncodeToString(secret)
	op.ExecutionCapabilityDigest = operationCapabilityDigest(capability)
	op.ExecutionAttempt = inv.Attempts
	op.State, op.Progress = api.OperationRunning, nil
	op.FailureCode = ""
	return capability, operationEvent(op, inv, "running", map[string]any{"state": op.State, "attempt": inv.Attempts}, now), nil
}

func operationCapabilityDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func operationExecutionHeaders(inv Invocation, op Operation, capability string) Invocation {
	inv.OperationID = op.ID
	headers := map[string]string{}
	_ = json.Unmarshal(inv.Headers, &headers)
	if headers == nil {
		headers = map[string]string{}
	}
	headers[api.OperationIDHeader] = op.ID
	headers[api.OperationAttemptHeader] = strconv.Itoa(inv.Attempts)
	headers[api.OperationCapabilityHeader] = capability
	inv.Headers, _ = json.Marshal(headers)
	return inv
}

// InvocationHasOperation is metadata for scheduler fencing. Store ownership
// comes from the execution association, never from these transport headers.
func InvocationHasOperation(inv Invocation) bool {
	return inv.OperationID != ""
}

func operationEvent(op *Operation, inv Invocation, kind string, data any, now time.Time) api.OperationEvent {
	op.LatestSequence++
	op.UpdatedAt = now
	body, _ := json.Marshal(data)
	return api.OperationEvent{OperationID: op.ID, Sequence: op.LatestSequence, Type: kind, ExecutionID: inv.ID, Attempt: inv.Attempts, Data: body, CreatedAt: now}
}

func ValidateOperationExecutionAuthority(op Operation, inv Invocation, authority OperationExecutionAuthority, now time.Time) error {
	if authority.AccountID != op.AccountID || authority.AppID != op.AppID || authority.InstanceID == "" || authority.InstanceID != inv.InstanceID {
		return ErrNotFound
	}
	if op.CurrentInvocationID != inv.ID || authority.InvocationID != inv.ID || authority.Attempt <= 0 || authority.Attempt != inv.Attempts || op.ExecutionAttempt != authority.Attempt || op.State != api.OperationRunning || inv.State != InvocationDispatching || inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(now) {
		return ErrOperationStaleAttempt
	}
	digest := operationCapabilityDigest(authority.Capability)
	if len(authority.Capability) != 64 || subtle.ConstantTimeCompare([]byte(digest), []byte(op.ExecutionCapabilityDigest)) != 1 {
		return ErrNotFound
	}
	return nil
}

func operationReportFingerprint(report api.OperationReportRequest) string {
	raw, _ := json.Marshal(report)
	fingerprint, _ := operations.InputFingerprint(raw)
	return fingerprint
}

func operationProgress(op *Operation, inv Invocation, def OperationDefinition, report api.OperationReportRequest, limits api.OperationPlanLimits, now time.Time) (api.OperationEvent, error) {
	limits = op.PlanLimits
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > limits.ReportBytes {
		return api.OperationEvent{}, NewOperationLimitError("report_bytes", int64(limits.ReportBytes), int64(len(raw)))
	}
	contract, err := operations.Compile(def.Spec, op.PlanLimits)
	if err != nil {
		return api.OperationEvent{}, fmt.Errorf("state: compile progress contract: %w", err)
	}
	if err := contract.ValidateProgress(report); err != nil {
		return api.OperationEvent{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if op.ReportCount >= limits.ReportsPerOperation {
		return api.OperationEvent{}, NewOperationLimitError("reports_per_operation", int64(limits.ReportsPerOperation), int64(op.ReportCount)+1)
	}
	if prior := op.Progress; prior != nil && prior.Attempt == inv.Attempts {
		if now.Sub(prior.UpdatedAt) < time.Duration(limits.ReportMinIntervalMS)*time.Millisecond {
			return api.OperationEvent{}, NewOperationLimitError("report_min_interval_ms", int64(limits.ReportMinIntervalMS), now.Sub(prior.UpdatedAt).Milliseconds())
		}
		stageIndex := func(stage string) int {
			for i, s := range def.Spec.ProgressStages {
				if s == stage {
					return i
				}
			}
			return -1
		}
		if stageIndex(report.Stage) < stageIndex(prior.Stage) || (report.Stage == prior.Stage && (report.Total != prior.Total || report.Completed < prior.Completed)) {
			return api.OperationEvent{}, ErrConflict
		}
	}
	op.ReportCount++
	op.Progress = &api.OperationProgress{Stage: report.Stage, Completed: report.Completed, Total: report.Total, Attempt: inv.Attempts, UpdatedAt: now}
	return operationEvent(op, inv, "progress", op.Progress, now), nil
}

// A successful execution with invalid output is uncertain business work. Do
// not encourage a caller to regenerate an externally visible result.
func operationInvocationTransition(op *Operation, inv Invocation, def OperationDefinition, limits api.Limits, uncertain bool, now time.Time) (api.OperationEvent, error) {
	if op.CurrentInvocationID != inv.ID {
		return api.OperationEvent{}, ErrOperationStaleAttempt
	}
	limits.Operations = op.PlanLimits
	limits.MaxSourceBytesPerInvocation = op.ValueMaxBytes
	kind := ""
	switch inv.State {
	case InvocationPending:
		op.State, kind = api.OperationAccepted, "accepted"
	case InvocationCompleted:
		contract, err := operations.Compile(def.Spec, limits.Operations)
		if err != nil {
			return api.OperationEvent{}, err
		}
		if err := contract.ValidateOutput(inv.Result, limits.MaxSourceBytesPerInvocation); err != nil {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "invalid_output", "reconciliation_required"
		} else {
			op.State, op.FailureCode, kind = api.OperationSucceeded, "", "succeeded"
			op.Result = append(json.RawMessage(nil), inv.Result...)
		}
	case InvocationFailed, InvocationDeadLetter, InvocationCancelled, InvocationExpired:
		if uncertain {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "execution_outcome_unknown", "reconciliation_required"
		} else if inv.State == InvocationCancelled {
			op.State, op.FailureCode, kind = api.OperationCancelled, "", "cancelled"
		} else {
			op.State, op.FailureCode, kind = api.OperationFailed, "execution_failed", "failed"
		}
	default:
		return api.OperationEvent{}, ErrConflict
	}
	op.ExecutionCapabilityDigest = ""
	if op.State.Terminal() || op.State == api.OperationRequiresReconciliation {
		op.ExpiresAt = now.Add(time.Duration(limits.Operations.ResultRetentionSeconds) * time.Second)
		op.EventExpiresAt = now.Add(time.Duration(limits.Operations.EventRetentionSeconds) * time.Second)
		refreshOperationArtifactExpiry(op)
	}
	return operationEvent(op, inv, kind, map[string]any{"state": op.State, "failure_code": op.FailureCode}, now), nil
}

func operationNeedsReconciliation(op Operation, def OperationDefinition, inv Invocation, failure FailOptions) bool {
	return inv.State == InvocationDispatching && !failure.DispatchNotStarted && def.Spec.Recovery != api.OperationRecoverySafeRetry
}

func operationCompletionPayload(op Operation) ([]byte, error) {
	return json.Marshal(struct {
		Operation api.OperationResponse `json:"operation"`
	}{op.OperationResponse})
}
