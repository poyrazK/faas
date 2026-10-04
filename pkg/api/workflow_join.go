package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrWorkflowJoinInvalid = errors.New("workflow: invalid branch join")

// WorkflowJoinSpec waits for all dependencies and selects the first successful
// dependency in OutputFrom order. Only conditionally inactive branches may be
// skipped. Its output is {source: stepName, value: dependencyOutput}.
type WorkflowJoinSpec struct {
	OutputFrom []string `json:"output_from" yaml:"output_from" toml:"output_from"`
}

func (j *WorkflowJoinSpec) UnmarshalJSON(data []byte) error {
	type plain WorkflowJoinSpec
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: %w", ErrWorkflowJoinInvalid, err)
	}
	*j = WorkflowJoinSpec(value)
	return nil
}

// ValidateWorkflowJoinStep requires an explicit priority for every dependency.
// Joins are control steps; action, guard, retry and exception options would be
// ambiguous here and must instead be configured on downstream action steps.
func ValidateWorkflowJoinStep(step WorkflowStepSpec) error {
	if step.Join == nil {
		return nil
	}
	if len(step.Input) != 0 || step.Method != "" || step.When != nil || step.Timeout != 0 || step.OnTimeout != "" || step.OnFailure != "" || step.Retry != nil {
		return fmt.Errorf("%w: join cannot have input, method, when, timeout, exception routes, or retry", ErrWorkflowJoinInvalid)
	}
	if len(step.DependsOn) < 2 || len(step.DependsOn) > WorkflowJoinMaxDependencies || len(step.Join.OutputFrom) != len(step.DependsOn) {
		return fmt.Errorf("%w: output_from must order all 2-%d dependencies", ErrWorkflowJoinInvalid, WorkflowJoinMaxDependencies)
	}
	dependencies := make(map[string]bool, len(step.DependsOn))
	for _, name := range step.DependsOn {
		dependencies[name] = true
	}
	for _, name := range step.Join.OutputFrom {
		if !dependencies[name] {
			return fmt.Errorf("%w: output_from contains an unknown or repeated dependency", ErrWorkflowJoinInvalid)
		}
		delete(dependencies, name)
	}
	if len(dependencies) != 0 {
		return ErrWorkflowJoinInvalid
	}
	return nil
}
