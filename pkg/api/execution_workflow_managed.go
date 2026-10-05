package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

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
	WorkflowID string                         `json:"workflow_id"`
	PlanID     string                         `json:"plan_id"`
	Status     ManagedExecutionWorkflowStatus `json:"status"`
	StepCount  int                            `json:"step_count"`
	NextStep   int                            `json:"next_step"`
	Error      string                         `json:"error,omitempty"`
	CreatedAt  string                         `json:"created_at"`
}

func ValidateCreateManagedExecutionWorkflow(req CreateManagedExecutionWorkflowRequest) error {
	if req.WorkflowID == "" || ValidateExecutionWorkflowMetadata(req.WorkflowID, "") != nil {
		return fmt.Errorf("workflow_id is invalid")
	}
	if req.Version == "" || len(req.Version) > 24 || ValidateExecutionWorkflowMetadata(req.Version, "") != nil {
		return fmt.Errorf("version must be a short identifier of at most 24 characters")
	}
	if len(req.Steps) == 0 || len(req.Steps) > ExecutionWorkflowManagedMaxSteps {
		return fmt.Errorf("managed workflow must contain between 1 and %d steps", ExecutionWorkflowManagedMaxSteps)
	}
	if req.MaxParallelSteps < 0 || req.MaxParallelSteps > ExecutionWorkflowManagedMaxParallel {
		return fmt.Errorf("max_parallel_steps must be 0 (default) or between 1 and %d", ExecutionWorkflowManagedMaxParallel)
	}
	if req.FailurePolicy != "" && req.FailurePolicy != ExecutionWorkflowManagedFailureFailFast && req.FailurePolicy != ExecutionWorkflowManagedFailureContinueIndependent {
		return fmt.Errorf("failure_policy must be %q or %q", ExecutionWorkflowManagedFailureFailFast, ExecutionWorkflowManagedFailureContinueIndependent)
	}
	labels := make(map[string]struct{}, len(req.Steps))
	totalResultSchemaBytes := 0
	for _, step := range req.Steps {
		if step.Label == "" || strings.TrimSpace(step.Label) != step.Label || len(step.Label) > 96 {
			return fmt.Errorf("step labels must be trimmed text between 1 and 96 bytes")
		}
		for _, r := range step.Label {
			if unicode.IsControl(r) {
				return fmt.Errorf("step labels must not contain control characters")
			}
		}
		if _, duplicate := labels[step.Label]; duplicate {
			return fmt.Errorf("step label %q is repeated", step.Label)
		}
		labels[step.Label] = struct{}{}
		if step.Request.WorkflowID != "" || step.Request.StepLabel != "" {
			return fmt.Errorf("step %q must leave workflow_id and step_label to the server", step.Label)
		}
		if len(step.ArtifactInputs) > ExecutionArtifactInputMaxFiles {
			return fmt.Errorf("step %q has more than %d artifact inputs", step.Label, ExecutionArtifactInputMaxFiles)
		}
		if len(step.ResultSchema) > ExecutionWorkflowResultSchemaMaxBytes {
			return fmt.Errorf("step %q result_schema exceeds %d bytes", step.Label, ExecutionWorkflowResultSchemaMaxBytes)
		}
		if len(step.ResultSchema) != 0 {
			totalResultSchemaBytes += len(step.ResultSchema)
			if totalResultSchemaBytes > ExecutionWorkflowResultSchemasMaxBytes {
				return fmt.Errorf("workflow result schemas exceed %d bytes in total", ExecutionWorkflowResultSchemasMaxBytes)
			}
			if !json.Valid(step.ResultSchema) {
				return fmt.Errorf("step %q result_schema is invalid JSON", step.Label)
			}
		}
		if len(step.Request.ArtifactInputs) != 0 {
			return fmt.Errorf("step %q must declare managed DAG artifact_inputs on the step, not in request", step.Label)
		}
	}
	_, err := ManagedExecutionWorkflowStepDependencies(req)
	return err
}

// ManagedExecutionWorkflowStepDependencies returns the plan's direct
// prerequisites in stable order. Dependencies must point backward in the
// submitted plan, which makes cycles impossible and keeps recovery predictable.
func ManagedExecutionWorkflowStepDependencies(req CreateManagedExecutionWorkflowRequest) (map[string][]string, error) {
	indices := make(map[string]int, len(req.Steps))
	for i, step := range req.Steps {
		indices[step.Label] = i
	}
	dependencies := make(map[string][]string, len(req.Steps))
	for i, step := range req.Steps {
		deps := make([]string, 0, len(step.DependsOn)+len(step.ArtifactInputs)+1)
		seen := make(map[string]struct{}, len(step.DependsOn)+len(step.ArtifactInputs)+1)
		artifactDestinations := make(map[string]struct{}, len(step.Request.Files)+len(step.ArtifactInputs))
		for _, file := range step.Request.Files {
			artifactDestinations[file.Path] = struct{}{}
		}
		addDependency := func(label string) error {
			prior, ok := indices[label]
			if !ok || prior >= i {
				return fmt.Errorf("step %q dependency %q must name an earlier step", step.Label, label)
			}
			if _, exists := seen[label]; !exists {
				seen[label] = struct{}{}
				deps = append(deps, label)
			}
			return nil
		}
		for _, label := range step.DependsOn {
			if strings.TrimSpace(label) != label || label == "" {
				return nil, fmt.Errorf("step %q has an invalid dependency label", step.Label)
			}
			if _, duplicate := seen[label]; duplicate {
				return nil, fmt.Errorf("step %q repeats dependency %q", step.Label, label)
			}
			if err := addDependency(label); err != nil {
				return nil, err
			}
		}
		if step.InputFromPreviousResult {
			if i == 0 {
				return nil, fmt.Errorf("step %q cannot use a previous result because it is first", step.Label)
			}
			if len(step.Request.Input) != 0 {
				return nil, fmt.Errorf("step %q cannot set input and input_from_previous_result", step.Label)
			}
			if err := addDependency(req.Steps[i-1].Label); err != nil {
				return nil, err
			}
		}
		for _, input := range step.ArtifactInputs {
			if strings.TrimSpace(input.FromStep) != input.FromStep || input.FromStep == "" ||
				strings.TrimSpace(input.Name) != input.Name || input.Name == "" ||
				strings.TrimSpace(input.Path) != input.Path || input.Path == "" {
				return nil, fmt.Errorf("step %q has an invalid artifact input reference", step.Label)
			}
			if err := ValidateExecutionOutputFiles([]string{input.Name}); err != nil {
				return nil, fmt.Errorf("step %q has an invalid source artifact name", step.Label)
			}
			if err := ValidateExecutionOutputFiles([]string{input.Path}); err != nil {
				return nil, fmt.Errorf("step %q has an invalid artifact destination path", step.Label)
			}
			if _, duplicate := artifactDestinations[input.Path]; duplicate {
				return nil, fmt.Errorf("step %q repeats artifact destination path %q", step.Label, input.Path)
			}
			artifactDestinations[input.Path] = struct{}{}
			prior, ok := indices[input.FromStep]
			if !ok || prior >= i {
				return nil, fmt.Errorf("step %q artifact input must name an earlier step", step.Label)
			}
			declared := false
			for _, output := range req.Steps[prior].Request.OutputFiles {
				if output == input.Name {
					declared = true
					break
				}
			}
			if !declared {
				return nil, fmt.Errorf("step %q references artifact %q not declared by step %q", step.Label, input.Name, input.FromStep)
			}
			if err := addDependency(input.FromStep); err != nil {
				return nil, err
			}
		}
		if step.IncludeDependencyResults && len(step.Request.Input) != 0 {
			return nil, fmt.Errorf("step %q cannot set input and include_dependency_results", step.Label)
		}
		if step.IncludeDependencyResults && step.InputFromPreviousResult {
			return nil, fmt.Errorf("step %q cannot combine include_dependency_results with input_from_previous_result", step.Label)
		}
		if step.IncludeDependencyResults && len(deps) == 0 {
			return nil, fmt.Errorf("step %q includes dependency results but has no dependencies", step.Label)
		}
		dependencies[step.Label] = deps
	}
	return dependencies, nil
}
