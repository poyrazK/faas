package api

// ExecutionPipelineCompletion is an immutable metadata manifest. Results and
// artifact bytes remain on scoped Run receipts with their existing retention.
type ExecutionPipelineCompletion struct {
	Rerun        *ExecutionPipelineRerun          `json:"rerun,omitempty"`
	Version      string                           `json:"version"`
	CompletionID string                           `json:"completion_id"`
	WorkflowID   string                           `json:"workflow_id"`
	Outcome      string                           `json:"outcome"`
	FinishedAt   string                           `json:"finished_at"`
	Receipt      string                           `json:"receipt"`
	Usage        ExecutionUsage                   `json:"usage"`
	Steps        []ExecutionPipelineCompletedStep `json:"steps"`
}
type ExecutionPipelineCompletedStep struct {
	OutputSHA256     string                              `json:"output_sha256,omitempty"`
	ReusedRunID      string                              `json:"reused_run_id,omitempty"`
	Label            string                              `json:"label"`
	Task             *ExecutionTaskRef                   `json:"task,omitempty"`
	Outcome          string                              `json:"outcome"`
	ResultValidation string                              `json:"result_validation,omitempty"`
	Attempts         []ExecutionPipelineCompletedAttempt `json:"attempts"`
}
type ExecutionPipelineCompletedAttempt struct {
	RunID       string                         `json:"run_id"`
	Attempt     int                            `json:"attempt"`
	Status      ExecutionStatus                `json:"status"`
	FailureCode string                         `json:"failure_code,omitempty"`
	Receipt     string                         `json:"receipt"`
	Usage       ExecutionUsage                 `json:"usage"`
	Artifacts   []ExecutionPipelineArtifactRef `json:"artifacts"`
}
type ExecutionPipelineArtifactRef struct {
	Name      string `json:"name"`
	SizeBytes int    `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
}

const ExecutionPipelineFinishedWebhookEvent = "execution.pipeline.finished"

type ExecutionPipelineFinishedWebhookPayload struct {
	EventID    string `json:"event_id"`
	WorkflowID string `json:"workflow_id"`
	Outcome    string `json:"outcome"`
	FinishedAt string `json:"finished_at"`
	ReceiptURL string `json:"receipt_url"`
}

type ExecutionPipelineReceipt struct {
	ID         string                       `json:"id"`
	Completion *ExecutionPipelineCompletion `json:"completion,omitempty"`
}
