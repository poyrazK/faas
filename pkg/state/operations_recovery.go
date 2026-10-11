package state

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func operationRecoveryFingerprint(req api.OperationRecoveryRequest, limits api.Limits) (string, error) {
	if revision := req.ExpectedInspectionRevision; revision != "" {
		if !strings.HasPrefix(revision, "sha256:") || len(revision) != len("sha256:")+64 || revision != strings.ToLower(revision) {
			return "", ErrInvalidArgument
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(revision, "sha256:")); err != nil {
			return "", ErrInvalidArgument
		}
	}
	if len(req.RecoveryID) < 1 || len(req.RecoveryID) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(req.RecoveryID, "\x00\r\n") || req.ExpectedGeneration <= 0 {
		return "", ErrInvalidArgument
	}
	if strings.TrimSpace(req.Evidence) == "" || len(req.Evidence) > api.OperationRecoveryEvidenceMaxBytes || strings.ContainsRune(req.Evidence, '\x00') || len(req.Result) > limits.MaxSourceBytesPerInvocation {
		return "", ErrInvalidArgument
	}
	switch req.Resolution {
	case "succeeded":
		if len(req.Result) == 0 {
			return "", ErrInvalidArgument
		}
	case "safe_to_retry", "failed", "cancelled":
		if len(req.Result) != 0 {
			return "", ErrInvalidArgument
		}
	default:
		return "", ErrInvalidArgument
	}
	raw, _ := json.Marshal(req)
	return operations.InputFingerprint(raw)
}

func prepareOperationRecovery(op *Operation, original Invocation, def OperationDefinition, limits api.Limits, req api.OperationRecoveryRequest, now time.Time) (Invocation, api.OperationEvent, error) {
	limits.Operations = op.PlanLimits
	limits.MaxSourceBytesPerInvocation = op.ValueMaxBytes
	if op.State != api.OperationRequiresReconciliation || op.Generation != req.ExpectedGeneration || op.CurrentInvocationID != original.ID {
		return Invocation{}, api.OperationEvent{}, ErrConflict
	}
	if op.RecoveryCount >= limits.Operations.RecoveriesPerOperation {
		return Invocation{}, api.OperationEvent{}, NewOperationLimitError("recoveries_per_operation", int64(limits.Operations.RecoveriesPerOperation), int64(op.RecoveryCount)+1)
	}
	op.RecoveryCount++
	op.ExecutionCapabilityDigest = ""
	op.FailureCode = ""
	inv := original
	kind := req.Resolution
	switch req.Resolution {
	case "safe_to_retry":
		// Use the retained original input and deployment; recovery cannot amend
		// work or silently switch releases. A new execution gets a fresh lease.
		inv.ID = newOperationID()
		inv.State, inv.Attempts, inv.QuotaReserved = InvocationPending, 0, false
		inv.InstanceID, inv.LastError = "", ""
		inv.ReceivedAt, inv.CompletedAt, inv.LeaseExpiresAt = nil, nil, nil
		inv.Result, inv.Outcome = nil, nil
		inv.DueAt, inv.CreatedAt = now, now
		deadline := now.Add(time.Duration(limits.MaxAsyncInvocationDeadlineSeconds) * time.Second)
		inv.DeadlineAt = &deadline
		op.Generation++
		op.CurrentInvocationID = inv.ID
		op.ExecutionAttempt = 0
		op.State, op.Progress, op.Result = api.OperationAccepted, nil, nil
		op.Artifacts = nil
		op.ArtifactStorageKeys = nil
		op.CancellationRequested = false
		op.CompletionDelivery = api.OperationDeliveryResponse{State: "not_requested"}
		if def.Spec.CompletionWebhookID != "" {
			op.CompletionDelivery.State = "awaiting_outcome"
		}
		kind = "recovery_requested"
	case "succeeded":
		contract, err := operations.Compile(def.Spec, limits.Operations)
		if err != nil {
			return Invocation{}, api.OperationEvent{}, err
		}
		if err := contract.ValidateOutput(req.Result, limits.MaxSourceBytesPerInvocation); err != nil {
			return Invocation{}, api.OperationEvent{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		op.State = api.OperationSucceeded
		op.Result = append(json.RawMessage(nil), req.Result...)
	case "failed":
		op.State, op.FailureCode = api.OperationFailed, "reconciled_failure"
	case "cancelled":
		op.State = api.OperationCancelled
	default:
		return Invocation{}, api.OperationEvent{}, ErrInvalidArgument
	}
	op.ExpiresAt = now.Add(time.Duration(limits.Operations.ResultRetentionSeconds) * time.Second)
	op.EventExpiresAt = now.Add(time.Duration(limits.Operations.EventRetentionSeconds) * time.Second)
	refreshOperationArtifactExpiry(op)
	evidenceDigest, _ := operations.InputFingerprint(mustOperationJSON(req.Evidence))
	event := operationEvent(op, inv, kind, map[string]any{"state": op.State, "resolution": req.Resolution, "generation": op.Generation, "evidence_digest": evidenceDigest}, now)
	return inv, event, nil
}

func mustOperationJSON(v any) []byte { b, _ := json.Marshal(v); return b }
