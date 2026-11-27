package api

import "encoding/json"

// ExecutionPipelineOptions opts into the versioned, durable task pipeline lane.
// The deadline includes queueing, executions and retry backoff.
type ExecutionPipelineOptions struct {
	TotalTimeoutMS int `json:"total_timeout_ms,omitempty"`
}
type ExecutionRetryPolicy struct {
	MaxAttempts  int  `json:"max_attempts"`
	BackoffMS    int  `json:"backoff_ms,omitempty"`
	SafeToRepeat bool `json:"safe_to_repeat,omitempty"`
}
type ExecutionTaskBatch struct {
	Name        string                   `json:"name"`
	Version     int                      `json:"version"`
	CustomerRef string                   `json:"customer_ref,omitempty"`
	Retry       *ExecutionRetryPolicy    `json:"retry,omitempty"`
	Items       []ExecutionTaskBatchItem `json:"items"`
}
type ExecutionTaskBatchItem struct {
	Label      string               `json:"label"`
	Input      json.RawMessage      `json:"input,omitempty"`
	InputFiles []ExecutionFileInput `json:"input_files,omitempty"`
}
type ExecutionPipelineResponse struct {
	Progress        *ExecutionPipelineProgress      `json:"progress,omitempty"`
	Rerun           *ExecutionPipelineRerun         `json:"rerun,omitempty"`
	Completion      *ExecutionPipelineCompletion    `json:"completion,omitempty"`
	DeadlineAt      string                          `json:"deadline_at"`
	CancelRequested bool                            `json:"cancel_requested,omitempty"`
	Steps           []ExecutionPipelineStepResponse `json:"steps"`
}
type ExecutionPipelineStepResponse struct {
	Label       string                             `json:"label"`
	Task        *ExecutionTaskRef                  `json:"task,omitempty"`
	MaxAttempts int                                `json:"max_attempts"`
	Attempts    []ExecutionPipelineAttemptResponse `json:"attempts"`
}
type ExecutionPipelineAttemptResponse struct {
	RunID         string          `json:"run_id"`
	Attempt       int             `json:"attempt"`
	PreviousRunID string          `json:"previous_run_id,omitempty"`
	Status        ExecutionStatus `json:"status"`
	FailureCode   string          `json:"failure_code,omitempty"`
}
