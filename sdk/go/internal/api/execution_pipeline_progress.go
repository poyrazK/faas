package api

// Pipeline progress contains only observed state and scoped Run references.
type ExecutionPipelineProgress struct {
	Status string                          `json:"status"`
	Steps  []ExecutionPipelineStepProgress `json:"steps"`
}
type ExecutionPipelineStepProgress struct {
	Label     string `json:"label"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	RetryAt   string `json:"retry_at,omitempty"`
	EventsURL string `json:"events_url,omitempty"`
}
type ExecutionPipelineEvent struct {
	Sequence  int64                          `json:"sequence"`
	Type      string                         `json:"type"`
	Status    string                         `json:"status,omitempty"`
	Step      *ExecutionPipelineStepProgress `json:"step,omitempty"`
	CreatedAt string                         `json:"created_at"`
}
type ExecutionPipelineEventsResponse struct {
	PipelineID   string                     `json:"pipeline_id"`
	Events       []ExecutionPipelineEvent   `json:"events"`
	NextSequence int64                      `json:"next_sequence"`
	Gap          bool                       `json:"gap,omitempty"`
	Terminal     bool                       `json:"terminal"`
	Progress     *ExecutionPipelineProgress `json:"progress,omitempty"`
}

// Reruns require the original manifest because terminal plans are erased.
// RepeatSteps acknowledges every previously started step that will run again.
type RerunExecutionPipelineRequest struct {
	WorkflowID  string                                `json:"workflow_id"`
	Original    CreateManagedExecutionWorkflowRequest `json:"original"`
	RepeatSteps []string                              `json:"repeat_steps,omitempty"`
}
type ExecutionPipelineRerun struct {
	ParentID         string                        `json:"parent_id"`
	ParentWorkflowID string                        `json:"parent_workflow_id"`
	ParentPlanID     string                        `json:"parent_plan_id"`
	Reused           []ExecutionPipelineReusedStep `json:"reused"`
}
type ExecutionPipelineReusedStep struct {
	Label        string `json:"label"`
	RunID        string `json:"run_id"`
	OutputSHA256 string `json:"output_sha256"`
}
