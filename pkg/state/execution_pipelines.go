package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrExecutionPipelineStopped = errors.New("state: pipeline cancelled, expired, or lease lost")

type ExecutionPipelineStep struct {
	Label string                   `json:"label"`
	Task  *api.ExecutionTaskRef    `json:"task,omitempty"`
	Retry api.ExecutionRetryPolicy `json:"retry"`
}
type ExecutionPipelinePolicy struct {
	ProgressVersion   string                      `json:"progress_version,omitempty"`
	Rerun             *api.ExecutionPipelineRerun `json:"rerun,omitempty"`
	CompletionVersion string                      `json:"completion_version,omitempty"`
	Callback          *ExecutionTaskCallback      `json:"callback,omitempty"`
	ManifestDigest    string                      `json:"manifest_digest,omitempty"`
	SubmissionDigest  string                      `json:"submission_digest"`
	ScopedFileInputs  bool                        `json:"scoped_file_inputs,omitempty"`
	Wide              bool                        `json:"wide"`
	ControlWide       bool                        `json:"control_wide"`
	TotalTimeoutMS    int                         `json:"total_timeout_ms"`
	Steps             []ExecutionPipelineStep     `json:"steps"`
}
type ExecutionPipeline struct {
	Journal         ExecutionPipelineJournal
	Completion      *api.ExecutionPipelineCompletion
	DeliveredAt     *time.Time
	JobID           string
	Policy          ExecutionPipelinePolicy
	DeadlineAt      time.Time
	DueAt           time.Time
	CancelRequested bool
}
type ExecutionPipelineAdmission struct {
	JobID, LeaseToken string
	Step, Attempt     int
	PreviousRunID     string
}
type ExecutionPipelineStore interface {
	CreateExecutionPipeline(context.Context, CreateExecutionWorkflowJobParams, ExecutionPipelinePolicy) (ExecutionWorkflowJob, error)
	ClaimExecutionPipeline(context.Context, string, time.Time, time.Duration) (ExecutionWorkflowJobClaim, error)
	UpdateExecutionPipeline(context.Context, string, string, ExecutionWorkflowJobUpdate) error
	ExecutionPipelineByID(context.Context, string) (ExecutionPipeline, error)
	CancelExecutionPipeline(context.Context, string, string, *string, time.Time) error
}

func ExecutionPipelineRetryable(r Execution) bool {
	if r.Status != api.ExecutionStatusFailed || r.FailureCode == nil {
		return false
	}
	switch *r.FailureCode {
	case "restore_failed", "execution_transport_failed", "lease_expired":
		return true
	}
	return false
}
func ExecutionPipelineRetryAt(r Execution, p api.ExecutionRetryPolicy, attempt int) time.Time {
	if r.FinishedAt == nil {
		return time.Time{}
	}
	ms := int64(p.BackoffMS)
	for i := 1; i < attempt && ms < api.ExecutionPipelineMaxBackoffMS; i++ {
		ms *= 2
	}
	if ms > api.ExecutionPipelineMaxBackoffMS {
		ms = api.ExecutionPipelineMaxBackoffMS
	}
	return r.FinishedAt.Add(time.Duration(ms) * time.Millisecond)
}
func validatePipelinePolicy(p ExecutionPipelinePolicy, n int) error {
	if p.ScopedFileInputs && (p.Callback == nil || p.Rerun != nil) {
		return ErrExecutionInvalid
	}
	if p.ProgressVersion != "" && p.ProgressVersion != "v1" {
		return ErrExecutionInvalid
	}
	if p.Rerun != nil && (p.ProgressVersion != "v1" || !validEnvironmentID(p.Rerun.ParentID) || len(p.Rerun.Reused) > api.ExecutionWorkflowManagedMaxSteps) {
		return ErrExecutionInvalid
	}
	if p.CompletionVersion != "" && p.CompletionVersion != "v1" {
		return ErrExecutionInvalid
	}
	if err := validateExecutionTaskCallback(p.Callback); err != nil {
		return err
	}
	if p.TotalTimeoutMS < 1 || p.TotalTimeoutMS > api.ExecutionPipelineMaxTimeoutMS || len(p.Steps) != n {
		return ErrExecutionInvalid
	}
	for _, s := range p.Steps {
		if p.Callback != nil && (s.Task == nil || !validEnvironmentID(s.Task.ID)) {
			return ErrExecutionInvalid
		}
		if s.Label == "" || s.Retry.MaxAttempts < 1 || s.Retry.MaxAttempts > api.ExecutionPipelineMaxAttempts || s.Retry.MaxAttempts > 1 && !s.Retry.SafeToRepeat || s.Retry.BackoffMS < api.ExecutionPipelineMinBackoffMS || s.Retry.BackoffMS > api.ExecutionPipelineMaxBackoffMS {
			return ErrExecutionInvalid
		}
	}
	return nil
}
func clonePipeline(p ExecutionPipeline) ExecutionPipeline {
	journal, _ := json.Marshal(p.Journal)
	p.Journal = ExecutionPipelineJournal{}
	_ = json.Unmarshal(journal, &p.Journal)
	raw, _ := json.Marshal(p.Policy)
	var policy ExecutionPipelinePolicy
	_ = json.Unmarshal(raw, &policy)
	p.Policy = policy
	if p.Completion != nil {
		raw, _ := json.Marshal(p.Completion)
		p.Completion = nil
		_ = json.Unmarshal(raw, &p.Completion)
	}
	if p.DeliveredAt != nil {
		at := *p.DeliveredAt
		p.DeliveredAt = &at
	}
	return p
}
func pipelineAdmissionLabel(plan string, step, attempt int) string {
	return fmt.Sprintf("gwf:p2:%s:p:%d:a:%d", plan, step, attempt)
}
func validatePipelineAdmission(p CreateExecutionParams, job ExecutionWorkflowJob, meta ExecutionPipeline, previous *Execution, now time.Time) error {
	a := p.Pipeline
	if a == nil {
		return nil
	}
	if meta.CancelRequested || !now.Before(meta.DeadlineAt) || job.Status != api.ManagedExecutionWorkflowRunning || job.LeaseToken == nil || *job.LeaseToken != a.LeaseToken || job.LeaseExpiresAt == nil || !now.Before(*job.LeaseExpiresAt) {
		return ErrExecutionPipelineStopped
	}
	if p.DeadlineAt.After(meta.DeadlineAt) || job.AccountID != p.AccountID || job.WorkflowID != p.WorkflowID || !sameOptionalString(job.RunsPrincipalID, p.RunsPrincipalID) || a.Step < 0 || a.Step >= len(meta.Policy.Steps) || p.StepLabel != pipelineAdmissionLabel(job.PlanID, a.Step, a.Attempt) || p.FilesAccountWide != meta.Policy.Wide || p.ControlAccountWide != meta.Policy.ControlWide {
		return ErrExecutionInvalid
	}
	s := meta.Policy.Steps[a.Step]
	if meta.Policy.Rerun != nil {
		for _, ref := range meta.Policy.Rerun.Reused {
			if ref.Label == s.Label {
				return ErrExecutionInvalid
			}
		}
	}
	if a.Attempt < 1 || a.Attempt > s.Retry.MaxAttempts {
		return ErrExecutionInvalid
	}
	if p.Task != nil && (p.Task.Callback != nil || p.Task.PipelineEndpointID != pipelineEndpoint(meta.Policy)) {
		return ErrExecutionInvalid
	}
	if (s.Task == nil) != (p.Task == nil) || s.Task != nil && s.Task.ID != p.Task.VersionID {
		return ErrExecutionInvalid
	}
	if a.Attempt == 1 {
		if a.PreviousRunID != "" {
			return ErrExecutionInvalid
		}
		return nil
	}
	if previous == nil || previous.ID != a.PreviousRunID || previous.AccountID != p.AccountID || previous.WorkflowID != job.WorkflowID || !sameOptionalString(previous.RunsPrincipalID, job.RunsPrincipalID) || previous.StepLabel != pipelineAdmissionLabel(job.PlanID, a.Step, a.Attempt-1) || !s.Retry.SafeToRepeat || !ExecutionPipelineRetryable(*previous) || previous.FinishedAt == nil || now.Before(ExecutionPipelineRetryAt(*previous, s.Retry, a.Attempt-1)) {
		return ErrExecutionInvalid
	}
	return nil
}
