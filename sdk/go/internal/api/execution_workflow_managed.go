package api

import "encoding/json"

const (
	ExecutionWorkflowManagedMaxSteps       = 16
	ExecutionWorkflowManagedMaxParallel    = 8
	ExecutionWorkflowManagedPlanMaxBytes   = 4 << 20
	ExecutionWorkflowResultSchemaMaxBytes  = 64 << 10
	ExecutionWorkflowResultSchemasMaxBytes = 1 << 20
)

const (
	ExecutionWorkflowManagedFailureFailFast            = "fail_fast"
	ExecutionWorkflowManagedFailureContinueIndependent = "continue_independent"
)

// CreateManagedExecutionWorkflowRequest submits a bounded DAG for control-plane
// orchestration. Every step runs in its own disposable Run.
type CreateManagedExecutionWorkflowRequest struct {
	WorkflowID       string                               `json:"workflow_id"`
	Version          string                               `json:"version"`
	MaxParallelSteps int                                  `json:"max_parallel_steps,omitempty"`
	FailurePolicy    string                               `json:"failure_policy,omitempty"`
	Steps            []CreateManagedExecutionWorkflowStep `json:"steps"`
	Pipeline         *ExecutionPipelineOptions            `json:"pipeline,omitempty"`
	Batch            *ExecutionTaskBatch                  `json:"batch,omitempty"`
}

// CreateManagedExecutionWorkflowStep runs after its declared dependencies
// succeed. Independent steps may run concurrently. InputFromPreviousResult
// also makes the immediately preceding step an implicit dependency.
type CreateManagedExecutionWorkflowStep struct {
	Label                    string                                  `json:"label"`
	DependsOn                []string                                `json:"depends_on,omitempty"`
	InputFromPreviousResult  bool                                    `json:"input_from_previous_result,omitempty"`
	IncludeDependencyResults bool                                    `json:"include_dependency_results,omitempty"`
	ArtifactInputs           []ManagedExecutionWorkflowArtifactInput `json:"artifact_inputs,omitempty"`
	ResultSchema             json.RawMessage                         `json:"result_schema,omitempty"`
	Request                  CreateExecutionRequest                  `json:"request"`
	Task                     *InvokeExecutionTaskRequest             `json:"task,omitempty"`
	Retry                    *ExecutionRetryPolicy                   `json:"retry,omitempty"`
}

// ManagedExecutionWorkflowArtifactInput selects an output artifact from an
// earlier step and stages it in this step's fresh ephemeral files bundle.
type ManagedExecutionWorkflowArtifactInput struct {
	FromStep string `json:"from_step"`
	Name     string `json:"name"`
	Path     string `json:"path"`
}

type ManagedExecutionWorkflowStatus string

const (
	ManagedExecutionWorkflowQueued    ManagedExecutionWorkflowStatus = "queued"
	ManagedExecutionWorkflowRunning   ManagedExecutionWorkflowStatus = "running"
	ManagedExecutionWorkflowSucceeded ManagedExecutionWorkflowStatus = "succeeded"
	ManagedExecutionWorkflowFailed    ManagedExecutionWorkflowStatus = "failed"
)

// ManagedExecutionWorkflowResponse contains workflow metadata only; source
// and input remain encrypted at rest and are never echoed to callers.
type ManagedExecutionWorkflowResponse struct {
	ID         string                         `json:"id,omitempty"`
	WorkflowID string                         `json:"workflow_id"`
	PlanID     string                         `json:"plan_id"`
	Status     ManagedExecutionWorkflowStatus `json:"status"`
	StepCount  int                            `json:"step_count"`
	NextStep   int                            `json:"next_step"`
	Error      string                         `json:"error,omitempty"`
	CreatedAt  string                         `json:"created_at"`
	Pipeline   *ExecutionPipelineResponse     `json:"pipeline,omitempty"`
}
