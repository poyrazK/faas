package api

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrWorkflowEmptySteps         = errors.New("workflow: must have at least one step")
	ErrWorkflowDuplicateStep      = errors.New("workflow: duplicate step name")
	ErrWorkflowInvalidStepTarget  = errors.New("workflow: step must specify either path or wait_for_event, but not both")
	ErrWorkflowUnknownDependency  = errors.New("workflow: step depends on unknown step")
	ErrWorkflowSelfDependency     = errors.New("workflow: step cannot depend on itself")
	ErrWorkflowDAGCycle           = errors.New("workflow: circular dependency detected in steps")
	ErrWorkflowTimeoutExceeded    = errors.New("workflow: step timeout exceeds plan limit")
	ErrWorkflowWaitTimeoutInvalid = errors.New("workflow: wait_for_event timeout must be between 1s and 7d")
	ErrWorkflowUnknownOnTimeout   = errors.New("workflow: on_timeout references unknown step")
)

// WorkflowSpec defines the declarative structure of a workflow.
type WorkflowSpec struct {
	Name  string             `json:"name" yaml:"name" toml:"name"`
	Steps []WorkflowStepSpec `json:"steps" yaml:"steps" toml:"steps"`
}

// WorkflowStepSpec defines an individual step in a workflow DAG.
type WorkflowStepSpec struct {
	Name         string             `json:"name" yaml:"name" toml:"name"`
	Path         string             `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Method       string             `json:"method,omitempty" yaml:"method,omitempty" toml:"method,omitempty"`
	DependsOn    []string           `json:"depends_on,omitempty" yaml:"depends_on,omitempty" toml:"depends_on,omitempty"`
	WaitForEvent string             `json:"wait_for_event,omitempty" yaml:"wait_for_event,omitempty" toml:"wait_for_event,omitempty"`
	Timeout      time.Duration      `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	OnTimeout    string             `json:"on_timeout,omitempty" yaml:"on_timeout,omitempty" toml:"on_timeout,omitempty"`
	Retry        *WorkflowRetrySpec `json:"retry,omitempty" yaml:"retry,omitempty" toml:"retry,omitempty"`
}

// WorkflowRetrySpec configures step retry policies.
type WorkflowRetrySpec struct {
	MaxAttempts int    `json:"max_attempts" yaml:"max_attempts" toml:"max_attempts"`
	Backoff     string `json:"backoff" yaml:"backoff" toml:"backoff"` // "exponential" | "fixed"
}

// ValidateWorkflowDAG validates the step dependencies, timeouts, and cycles
// using Kahn's algorithm for topological sorting. Returns the sorted step
// execution order if valid.
func ValidateWorkflowDAG(spec WorkflowSpec, plan Plan) ([]string, error) {
	if len(spec.Steps) == 0 {
		return nil, ErrWorkflowEmptySteps
	}

	stepMap := make(map[string]WorkflowStepSpec, len(spec.Steps))
	inDegree := make(map[string]int, len(spec.Steps))
	adj := make(map[string][]string, len(spec.Steps))

	maxTimeout := plan.WorkflowStepMaxTimeout()
	maxWaitDuration := time.Duration(WorkflowMaxWaitDays) * 24 * time.Hour

	for _, step := range spec.Steps {
		if step.Name == "" {
			return nil, errors.New("workflow: step name cannot be empty")
		}
		if _, exists := stepMap[step.Name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrWorkflowDuplicateStep, step.Name)
		}
		stepMap[step.Name] = step
		inDegree[step.Name] = 0

		// Target validation: path vs wait_for_event
		hasPath := step.Path != ""
		hasEvent := step.WaitForEvent != ""
		if (hasPath && hasEvent) || (!hasPath && !hasEvent) {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowInvalidStepTarget, step.Name)
		}

		// wait_for_event timeout validation
		if hasEvent {
			if step.Timeout <= 0 || step.Timeout > maxWaitDuration {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowWaitTimeoutInvalid, step.Name)
			}
		} else {
			// Plan step timeout limit validation for active execution steps
			if maxTimeout > 0 && step.Timeout > maxTimeout {
				return nil, fmt.Errorf("%w in step %q: %v > %v", ErrWorkflowTimeoutExceeded, step.Name, step.Timeout, maxTimeout)
			}
		}
	}

	// Validate dependencies and on_timeout references
	for _, step := range spec.Steps {
		if step.OnTimeout != "" {
			if _, ok := stepMap[step.OnTimeout]; !ok {
				return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowUnknownOnTimeout, step.Name, step.OnTimeout)
			}
		}

		for _, dep := range step.DependsOn {
			if dep == step.Name {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowSelfDependency, step.Name)
			}
			if _, ok := stepMap[dep]; !ok {
				return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowUnknownDependency, step.Name, dep)
			}
			adj[dep] = append(adj[dep], step.Name)
			inDegree[step.Name]++
		}
	}

	// Kahn's algorithm for topological sorting
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		sorted = append(sorted, curr)

		for _, neighbor := range adj[curr] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if len(sorted) != len(spec.Steps) {
		return nil, ErrWorkflowDAGCycle
	}

	return sorted, nil
}
