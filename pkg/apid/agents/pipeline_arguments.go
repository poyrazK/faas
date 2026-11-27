package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
)

func pipelineArguments(raw json.RawMessage) (api.CreateManagedExecutionWorkflowRequest, map[string][]string, error) {
	var args struct {
		api.CreateManagedExecutionWorkflowRequest
		Steps []struct {
			api.CreateManagedExecutionWorkflowStep
			FileGrants []string `json:"file_grants"`
		} `json:"steps"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&args)
	r := args.CreateManagedExecutionWorkflowRequest
	if err != nil {
		return r, nil, err
	}
	if r.WorkflowID != "" || r.Version != "" {
		return r, nil, fmt.Errorf("pipeline identities are server-owned")
	}
	var grants map[string][]string
	count := 0
	for _, step := range args.Steps {
		r.Steps = append(r.Steps, step.CreateManagedExecutionWorkflowStep)
		if len(step.FileGrants) == 0 {
			continue
		}
		count += len(step.FileGrants)
		if count > api.AgentExecutionFileGrantMaxCount {
			return r, nil, fmt.Errorf("too many pipeline file grants")
		}
		for _, token := range step.FileGrants {
			if token == "" || len(token) > api.AgentExecutionFileGrantMaxBytes {
				return r, nil, fmt.Errorf("invalid pipeline file grant")
			}
		}
		if grants == nil {
			grants = make(map[string][]string)
		}
		grants[step.Label] = step.FileGrants
	}
	normalized, err := runpipeline.Normalize(r)
	if err != nil {
		return r, nil, err
	}
	for _, step := range normalized.Steps {
		if step.Task == nil || len(step.Task.InputFiles) > 0 || len(step.Task.ArtifactInputs) > 0 || step.Task.CustomerRef != "" {
			return r, nil, fmt.Errorf("only named task inputs and pipeline artifact handoffs are allowed")
		}
	}
	return r, grants, nil
}
func pipelineTool() rapTool {
	task := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "version": map[string]any{"type": "integer", "minimum": 1}, "input": map[string]any{}}, "required": []string{"name", "version"}, "additionalProperties": false}
	retry := map[string]any{"type": "object", "properties": map[string]any{"max_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": api.ExecutionPipelineMaxAttempts}, "backoff_ms": map[string]any{"type": "integer", "minimum": api.ExecutionPipelineMinBackoffMS, "maximum": api.ExecutionPipelineMaxBackoffMS}, "safe_to_repeat": map[string]any{"type": "boolean"}}, "required": []string{"max_attempts"}, "additionalProperties": false}
	handoff := map[string]any{"type": "object", "properties": map[string]any{"from_step": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"from_step", "name", "path"}, "additionalProperties": false}
	fileGrants := map[string]any{"type": "array", "maxItems": api.AgentExecutionFileGrantMaxCount,
		"items": map[string]any{"type": "string", "maxLength": api.AgentExecutionFileGrantMaxBytes}}
	step := map[string]any{"type": "object", "properties": map[string]any{"file_grants": fileGrants, "label": map[string]any{"type": "string"}, "task": task, "retry": retry, "depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "input_from_previous_result": map[string]any{"type": "boolean"}, "include_dependency_results": map[string]any{"type": "boolean"}, "result_schema": map[string]any{}, "artifact_inputs": map[string]any{"type": "array", "items": handoff}}, "required": []string{"label", "task"}, "additionalProperties": false}
	item := map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"}, "input": map[string]any{}}, "required": []string{"label"}, "additionalProperties": false}
	batch := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "version": map[string]any{"type": "integer", "minimum": 1}, "retry": retry, "items": map[string]any{"type": "array", "minItems": 1, "maxItems": api.ExecutionWorkflowManagedMaxSteps, "items": item}}, "required": []string{"name", "version", "items"}, "additionalProperties": false}
	options := map[string]any{"type": "object", "properties": map[string]any{"total_timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": api.ExecutionPipelineMaxTimeoutMS}}, "additionalProperties": false}
	schema := map[string]any{"type": "object", "properties": map[string]any{"steps": map[string]any{"type": "array", "minItems": 1, "maxItems": api.ExecutionWorkflowManagedMaxSteps, "items": step}, "batch": batch, "pipeline": options, "max_parallel_steps": map[string]any{"type": "integer", "minimum": 1, "maximum": api.ExecutionWorkflowManagedMaxParallel}, "failure_policy": map[string]any{"type": "string", "enum": []string{api.ExecutionWorkflowManagedFailureContinueIndependent, api.ExecutionWorkflowManagedFailureFailFast}}}, "oneOf": []any{map[string]any{"required": []string{"steps"}}, map[string]any{"required": []string{"batch"}}}, "additionalProperties": false}
	return rapTool{Name: toolPipeline, Description: "Execute a bounded graph or batch of explicitly versioned Run tasks. One terminal pipeline receipt arrives as this call's result and wakes the thread. Every task must be delegated to this endpoint.", InputSchema: schema}
}
