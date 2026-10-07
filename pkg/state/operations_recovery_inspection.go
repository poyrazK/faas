// adr: 598
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This interface is intentionally separate from the execution/control store.
type OperationRecoveryInspectionStore interface {
	InspectOperationRecovery(context.Context, string, string) (api.OperationRecoveryInspection, error)
	PreviewOperationRecovery(context.Context, string, string, api.OperationRecoveryPreviewRequest) (api.OperationRecoveryPreview, error)
}

type operationRecoverySnapshot struct {
	op                                                           Operation
	def                                                          OperationDefinition
	inv                                                          Invocation
	jobTask                                                      *JobTask
	run                                                          *WorkflowRun
	steps                                                        map[string]WorkflowStep
	blobs                                                        map[string]OperationResultBlob // keyed by storage binding
	plan                                                         api.Plan
	codeAvailable, tenantActive, targetAvailable, runningAttempt bool
	activeRuns                                                   int
}

func operationRecoveryInspection(s operationRecoverySnapshot, now time.Time) api.OperationRecoveryInspection {
	op := s.op
	i := api.OperationRecoveryInspection{OperationID: op.ID, Generation: op.Generation, State: op.State, FailureCode: op.FailureCode,
		CancellationRequested: op.CancellationRequested, ExecutionKind: "http", ExecutionState: string(s.inv.State),
		InvocationID: op.CurrentInvocationID, WorkflowRunID: op.WorkflowRunID, JobRunID: op.JobRunID, Attempt: s.inv.Attempts,
		DeploymentID: op.DeploymentID, ReleaseID: op.ReleaseID, Steps: []api.OperationRecoveryStep{}, Artifacts: []api.OperationRecoveryArtifact{}, ObservedAt: now}
	if s.jobTask != nil {
		i.ExecutionKind, i.ExecutionState, i.Attempt = "job", s.jobTask.Status, s.jobTask.Attempt
		i.Steps = append(i.Steps, api.OperationRecoveryStep{Name: "task-0", State: s.jobTask.Status, Attempt: s.jobTask.Attempt, Confirmed: s.jobTask.Status == "succeeded" && len(op.JobResultReceipt) > 0, OutcomeUnknown: s.jobTask.StartedAt != nil && op.State == api.OperationRequiresReconciliation})
	}
	if s.run != nil {
		i.ExecutionKind, i.ExecutionState, i.Attempt = "workflow", s.run.Status, 0
		var spec api.WorkflowSpec
		_ = json.Unmarshal(s.run.DefinitionSnapshot, &spec)
		for _, action := range spec.Steps {
			step, exists := s.steps[action.Name]
			status := step.Status
			if !exists {
				status = "unavailable"
			}
			confirmed := status == WorkflowStepStatusSucceeded
			i.Steps = append(i.Steps, api.OperationRecoveryStep{Name: action.Name, State: status, Attempt: step.Attempt, Confirmed: confirmed,
				OutcomeUnknown: step.Attempt > 0 && !confirmed && status != WorkflowStepStatusSkipped})
			if step.Attempt > i.Attempt {
				i.Attempt = step.Attempt
			}
		}
	}
	seen := map[string]bool{}
	for _, receipt := range op.WorkflowArtifactReceipts {
		state := "prepared"
		if receipt.Published {
			state = "attached"
		}
		i.Artifacts = append(i.Artifacts, operationRecoveryArtifact(s, receipt.Artifact, receipt.Generation, receipt.Attempt, receipt.StepName, state, now))
		seen[receipt.Artifact.ID] = true
	}
	for _, receipt := range op.JobArtifactReceipts {
		state := "prepared"
		if receipt.Published {
			state = "attached"
		}
		i.Artifacts = append(i.Artifacts, operationRecoveryArtifact(s, receipt.Artifact, receipt.Generation, receipt.Attempt, "", state, now))
		seen[receipt.Artifact.ID] = true
	}
	for _, artifact := range op.Artifacts {
		if !seen[artifact.ID] {
			i.Artifacts = append(i.Artifacts, operationRecoveryArtifact(s, artifact, op.Generation, op.ExecutionAttempt, "", "attached", now))
		}
	}
	sort.Slice(i.Artifacts, func(a, b int) bool { return i.Artifacts[a].ID < i.Artifacts[b].ID })
	// Eligibility varies with other work and wall-clock expiry. Neither is a
	// durable inspection revision. Independent delivery status is excluded too.
	revision := i
	revision.ObservedAt = time.Time{}
	revision.Artifacts = append([]api.OperationRecoveryArtifact(nil), i.Artifacts...)
	for n := range revision.Artifacts {
		revision.Artifacts[n].Retained = false
	}
	raw, _ := json.Marshal(struct {
		Inspection     api.OperationRecoveryInspection
		Sequence       int64
		Definition     string
		RecoveryCount  int
		JobReceipts    map[string]OperationJobArtifactReceipt
		Receipts       map[string]OperationWorkflowArtifactReceipt
		Bindings       map[string]string
		BlobEvidence   map[string]operationRecoveryBlobEvidence
		RunResumeCount int
		RunCancelledAt *time.Time
	}{Inspection: revision, Sequence: op.LatestSequence, Definition: op.DefinitionRevision, RecoveryCount: op.RecoveryCount, Receipts: op.WorkflowArtifactReceipts, JobReceipts: op.JobArtifactReceipts, Bindings: op.ArtifactStorageKeys, BlobEvidence: recoveryBlobEvidence(s.blobs), RunResumeCount: recoveryRunResumeCount(s.run), RunCancelledAt: recoveryRunCancelledAt(s.run)})
	digest := sha256.Sum256(raw)
	i.InspectionRevision = "sha256:" + hex.EncodeToString(digest[:])
	i.RetryBlockers, _ = operationRecoveryRetryPlan(s, now)
	return i
}

func recoveryRunResumeCount(run *WorkflowRun) int {
	if run != nil {
		return run.ResumeCount
	}
	return 0
}
func recoveryRunCancelledAt(run *WorkflowRun) *time.Time {
	if run != nil {
		return run.CancelledAt
	}
	return nil
}

// Hash binding facts, excluding cleanup scheduling/lease observation changes.
type operationRecoveryBlobEvidence struct {
	ID, OperationID, AccountID, ExecutionID, RunID, JobRunID, Step, Fingerprint, State string
	Generation, Attempt                                                                int
	SizeBytes                                                                          int64
}

func recoveryBlobEvidence(blobs map[string]OperationResultBlob) map[string]operationRecoveryBlobEvidence {
	states := make(map[string]operationRecoveryBlobEvidence, len(blobs))
	for key, blob := range blobs {
		states[key] = operationRecoveryBlobEvidence{ID: blob.ID, OperationID: blob.OperationID, AccountID: blob.AccountID, ExecutionID: blob.ExecutionID,
			RunID: blob.WorkflowRunID, JobRunID: blob.JobRunID, Step: blob.WorkflowStep, Fingerprint: blob.Fingerprint, State: blob.State, Generation: blob.Generation, Attempt: blob.Attempt, SizeBytes: blob.SizeBytes}
	}
	return states
}

func operationRecoveryArtifact(s operationRecoverySnapshot, a api.OperationResultArtifact, generation, attempt int, step, state string, now time.Time) api.OperationRecoveryArtifact {
	key := s.op.ArtifactStorageKeys[a.ID]
	blob, exists := s.blobs[key]
	expires := s.op.ExpiresAt
	if a.ExpiresAt != nil {
		expires = *a.ExpiresAt
	}
	retained := exists && key != "" && blob.StorageKey == key && blob.OperationID == s.op.ID && blob.AccountID == s.op.AccountID && blob.State == "retained" && expires.After(now)
	retained = retained && blob.SizeBytes == a.SizeBytes
	if step != "" {
		matched := false
		for _, receipt := range s.op.WorkflowArtifactReceipts {
			if receipt.Artifact.ID == a.ID {
				matched = blob.ID == receipt.BlobID && blob.WorkflowRunID == s.op.WorkflowRunID && blob.WorkflowStep == step && blob.ExecutionID == "" && blob.Fingerprint == receipt.Fingerprint
				break
			}
		}
		retained = retained && matched
	} else if s.op.JobRunID != "" {
		matched := false
		for _, receipt := range s.op.JobArtifactReceipts {
			if receipt.Artifact.ID == a.ID {
				matched = blob.ID == receipt.BlobID && blob.JobRunID == s.op.JobRunID && receipt.RunID == s.op.JobRunID && blob.ExecutionID == "" && blob.WorkflowRunID == "" && blob.WorkflowStep == "" && blob.Fingerprint == receipt.Fingerprint && blob.Generation == generation && blob.Attempt == attempt
				break
			}
		}
		retained = retained && matched
	} else {
		retained = retained && blob.ExecutionID == s.op.CurrentInvocationID && blob.Generation == generation && blob.Attempt == attempt
	}
	return api.OperationRecoveryArtifact{ID: a.ID, Name: a.Name, WorkflowStep: step, Generation: generation, Attempt: attempt, State: state,
		Retained: retained, SizeBytes: a.SizeBytes, SHA256: a.SHA256, ExpiresAt: expires}
}

func operationRecoveryBaseBlockers(s operationRecoverySnapshot, now time.Time) []string {
	b := []string{}
	if s.op.State != api.OperationRequiresReconciliation {
		b = append(b, "reconciliation_not_required")
	}
	if !s.op.ExpiresAt.After(now) {
		b = append(b, "retention_expired")
	}
	if s.op.RecoveryCount >= s.op.PlanLimits.RecoveriesPerOperation {
		b = append(b, "recovery_limit")
	}
	if s.run != nil && s.run.ResumeCount+1 != s.op.Generation {
		b = append(b, "execution_generation_changed")
	}
	return b
}

func operationRecoveryRetryPlan(s operationRecoverySnapshot, now time.Time) ([]string, []string) {
	b := operationRecoveryBaseBlockers(s, now)
	if !s.codeAvailable {
		b = append(b, "pinned_code_unavailable")
	}
	if !api.MustLimitsFor(s.plan).Operations.Allowed {
		b = append(b, "plan_admission")
	}
	if !s.tenantActive {
		b = append(b, "customer_suspended")
	}
	if s.jobTask != nil {
		if !s.targetAvailable || !operationJobPolicyAvailable(s.op, s.plan) {
			b = append(b, "job_target_unavailable")
		}
		if s.jobTask.Status == "queued" || s.jobTask.Status == "claimed" {
			b = append(b, "job_attempt_running")
		}
		return b, []string{"task-0"}
	}
	if s.run == nil {
		return b, nil
	}
	if !s.targetAvailable {
		b = append(b, "workflow_target_unavailable")
	}
	_, names, err := workflowResumePlan(*s.run, s.steps, s.run.ResumeCount, s.plan)
	if err != nil {
		code := "workflow_resume_unsafe"
		if errors.Is(err, ErrWorkflowResumeLimit) {
			code = "workflow_resume_limit"
		}
		if errors.Is(err, ErrWorkflowResumeConflict) {
			code = "workflow_resume_conflict"
		}
		b = append(b, code)
	}
	if s.runningAttempt {
		b = append(b, "workflow_attempt_running")
	}
	if s.activeRuns >= s.plan.WorkflowMaxConcurrentRuns() {
		b = append(b, "workflow_concurrency_limit")
	}
	return b, names
}

func operationRecoveryPreview(s operationRecoverySnapshot, req api.OperationRecoveryPreviewRequest, now time.Time) (api.OperationRecoveryPreview, error) {
	// Share the apply contract's format checks, without accepting or recording
	// fabricated evidence. Output-schema validation below uses the same planner.
	validation := api.OperationRecoveryRequest{RecoveryID: "preview", Evidence: "preview", ExpectedGeneration: req.ExpectedGeneration, Resolution: req.Resolution, Result: req.Result}
	if _, err := operationRecoveryFingerprint(validation, api.Limits{MaxSourceBytesPerInvocation: s.op.ValueMaxBytes}); err != nil {
		return api.OperationRecoveryPreview{}, err
	}
	if req.ExpectedGeneration != s.op.Generation {
		return api.OperationRecoveryPreview{}, ErrConflict
	}
	i := operationRecoveryInspection(s, now)
	p := api.OperationRecoveryPreview{Inspection: i, Resolution: req.Resolution, EvidenceRequired: true, Blockers: operationRecoveryBaseBlockers(s, now),
		ReusedSteps: []string{}, ReopenedSteps: []string{}, ReusableArtifactIDs: []string{}, PublishArtifactIDs: []string{}}
	if req.Resolution == "safe_to_retry" {
		var names []string
		p.Blockers, names = operationRecoveryRetryPlan(s, now)
		p.ReopenedSteps = append(p.ReopenedSteps, names...)
		p.StartsNewExecution, p.ClearsArtifactReferences = s.run == nil, s.run == nil
		for _, step := range i.Steps {
			if step.Confirmed {
				p.ReusedSteps = append(p.ReusedSteps, step.Name)
			}
		}
		if s.run != nil {
			for _, artifact := range i.Artifacts {
				if artifact.Retained {
					p.ReusableArtifactIDs = append(p.ReusableArtifactIDs, artifact.ID)
				}
			}
		}
	} else if len(p.Blockers) == 0 {
		copy := cloneOperation(s.op)
		var err error
		if s.jobTask != nil {
			_, err = operationJobRecovery(&copy, s.def, validation, now)
		} else if s.run != nil {
			_, err = operationWorkflowRecovery(&copy, s.def, *s.run, validation, now)
		} else {
			_, _, err = prepareOperationRecovery(&copy, s.inv, s.def, api.MustLimitsFor(s.plan), validation, now)
		}
		if errors.Is(err, ErrInvalidArgument) {
			p.Blockers = append(p.Blockers, "result_contract_invalid")
		} else if err != nil {
			return api.OperationRecoveryPreview{}, err
		}
		if err == nil && req.Resolution == "succeeded" && (s.run != nil || s.jobTask != nil) {
			for _, artifact := range i.Artifacts {
				if artifact.State == "prepared" && artifact.Generation == s.op.Generation {
					p.PublishArtifactIDs = append(p.PublishArtifactIDs, artifact.ID)
				}
			}
		}
	}
	p.Eligible = len(p.Blockers) == 0
	return p, nil
}
