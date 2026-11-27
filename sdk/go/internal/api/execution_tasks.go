package api

import "encoding/json"

// PublishExecutionTaskRequest is an immutable, account-owned tool definition.
// Definition may contain source, but it is never returned by catalog reads.
type PublishExecutionTaskRequest struct {
	Name             string                 `json:"name"`
	Version          int                    `json:"version"`
	Definition       CreateExecutionRequest `json:"definition"`
	AgentEndpointIDs []string               `json:"agent_endpoint_ids,omitempty"`
}
type ExecutionTaskRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}
type ExecutionTaskVersionResponse struct {
	ExecutionTaskRef
	Runtime          ExecutionRuntime         `json:"runtime"`
	Profile          ExecutionProfile         `json:"profile"`
	EnvironmentID    string                   `json:"environment_id,omitempty"`
	Limits           ResolvedExecutionLimits  `json:"limits"`
	InputSchema      json.RawMessage          `json:"input_schema,omitempty"`
	ResultSchema     json.RawMessage          `json:"result_schema,omitempty"`
	IntegrationIDs   []string                 `json:"integration_ids,omitempty"`
	OutputFiles      []string                 `json:"output_files,omitempty"`
	ArtifactStorage  ExecutionArtifactStorage `json:"artifact_storage,omitempty"`
	AgentEndpointIDs []string                 `json:"agent_endpoint_ids,omitempty"`
	Status           string                   `json:"status"`
	CreatedAt        string                   `json:"created_at"`
}
type ExecutionTaskListResponse struct {
	Tasks      []ExecutionTaskVersionResponse `json:"tasks"`
	NextCursor string                         `json:"next_cursor,omitempty"`
}
type InvokeExecutionTaskRequest struct {
	CustomerRef    string                   `json:"customer_ref,omitempty"`
	Name           string                   `json:"name"`
	Version        int                      `json:"version"`
	Input          json.RawMessage          `json:"input,omitempty"`
	InputFiles     []ExecutionFileInput     `json:"input_files,omitempty"`
	ArtifactInputs []ExecutionArtifactInput `json:"artifact_inputs,omitempty"`
	WorkflowID     string                   `json:"workflow_id,omitempty"`
	StepLabel      string                   `json:"step_label,omitempty"`
}
