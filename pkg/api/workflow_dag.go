package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	ErrWorkflowEmptySteps          = errors.New("workflow: must have at least one step")
	ErrWorkflowNameRequired        = errors.New("workflow: name cannot be empty")
	ErrWorkflowPlanNotAllowed      = errors.New("workflow: plan does not allow workflows")
	ErrWorkflowInvalidTrigger      = errors.New("workflow: trigger type must be manual")
	ErrWorkflowDuplicateStep       = errors.New("workflow: duplicate step name")
	ErrWorkflowInvalidStepTarget   = errors.New("workflow: step must specify exactly one of run, path, or wait_for_event")
	ErrWorkflowInvalidPath         = errors.New("workflow: step path must start with '/'")
	ErrWorkflowInvalidMethod       = errors.New("workflow: step method is not supported")
	ErrWorkflowInvalidRun          = errors.New("workflow: step run cannot be empty")
	ErrWorkflowInvalidInput        = errors.New("workflow: step input must be valid JSON")
	ErrWorkflowUnknownDependency   = errors.New("workflow: step depends on unknown step")
	ErrWorkflowDuplicateDependency = errors.New("workflow: step has a duplicate dependency")
	ErrWorkflowSelfDependency      = errors.New("workflow: step cannot depend on itself")
	ErrWorkflowDAGCycle            = errors.New("workflow: circular dependency detected in steps")
	ErrWorkflowTimeoutInvalid      = errors.New("workflow: step timeout cannot be negative")
	ErrWorkflowTimeoutExceeded     = errors.New("workflow: step timeout exceeds plan limit")
	ErrWorkflowWaitTimeoutInvalid  = errors.New("workflow: wait_for_event timeout must be between 1s and the plan limit")
	ErrWorkflowRetryInvalid        = errors.New("workflow: retry must have 1-25 attempts and fixed or exponential backoff")
	ErrWorkflowUnknownOnTimeout    = errors.New("workflow: on_timeout references unknown step")
)

// WorkflowTriggerSpec describes how a workflow is started. Manual is the
// first and currently only supported trigger; a pointer keeps an omitted
// trigger backward-compatible with the default manual behavior.
type WorkflowTriggerSpec struct {
	Type string `json:"type" yaml:"type" toml:"type"`
}

// WorkflowSpec defines the declarative structure of a workflow.
type WorkflowSpec struct {
	Name    string               `json:"name" yaml:"name" toml:"name"`
	Trigger *WorkflowTriggerSpec `json:"trigger,omitempty" yaml:"trigger,omitempty" toml:"trigger,omitempty"`
	Steps   []WorkflowStepSpec   `json:"steps" yaml:"steps" toml:"steps"`
}

// WorkflowStepSpec defines an individual step in a workflow DAG. Run and
// Input are the ADR-081 wire fields. Path and Method remain accepted for the
// existing HTTP wake executor and are intentionally additive during the
// runtime migration.
type WorkflowStepSpec struct {
	Name         string             `json:"name" yaml:"name" toml:"name"`
	Run          string             `json:"run,omitempty" yaml:"run,omitempty" toml:"run,omitempty"`
	Input        json.RawMessage    `json:"input,omitempty" yaml:"input,omitempty" toml:"input,omitempty"`
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
	Backoff     string `json:"backoff,omitempty" yaml:"backoff,omitempty" toml:"backoff,omitempty"` // "exponential" | "fixed"
}

// UnmarshalJSON keeps strict decoding intact when a WorkflowStepSpec uses
// its custom unmarshaler. json.Decoder.DisallowUnknownFields does not flow
// through the json.Unmarshal call made by WorkflowStepSpec.UnmarshalJSON,
// so nested retry fields need the same explicit allow-list.
func (r *WorkflowRetrySpec) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		if key != "max_attempts" && key != "backoff" {
			return fmt.Errorf("workflow retry: unknown field %q", key)
		}
	}
	type alias WorkflowRetrySpec
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = WorkflowRetrySpec(decoded)
	return nil
}

// UnmarshalJSON accepts ADR-081 duration strings (for example, "30s") and
// the old integer nanosecond representation. The explicit field check keeps
// strict JSON decoding for nested workflow steps even though this type has a
// custom unmarshaler.
func (s *WorkflowStepSpec) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]struct{}{
		"name": {}, "run": {}, "input": {}, "path": {}, "method": {},
		"depends_on": {}, "wait_for_event": {}, "timeout": {},
		"on_timeout": {}, "retry": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("workflow step: unknown field %q", key)
		}
	}

	type wire struct {
		Name         string             `json:"name"`
		Run          string             `json:"run"`
		Input        json.RawMessage    `json:"input"`
		Path         string             `json:"path"`
		Method       string             `json:"method"`
		DependsOn    []string           `json:"depends_on"`
		WaitForEvent string             `json:"wait_for_event"`
		Timeout      json.RawMessage    `json:"timeout"`
		OnTimeout    string             `json:"on_timeout"`
		Retry        *WorkflowRetrySpec `json:"retry"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	timeout, err := decodeWorkflowTimeout(w.Timeout)
	if err != nil {
		return err
	}
	*s = WorkflowStepSpec{
		Name: w.Name, Run: w.Run, Input: cloneRawJSON(w.Input), Path: w.Path,
		Method: w.Method, DependsOn: append([]string(nil), w.DependsOn...),
		WaitForEvent: w.WaitForEvent, Timeout: timeout, OnTimeout: w.OnTimeout,
		Retry: w.Retry,
	}
	return nil
}

// MarshalJSON emits the stable ADR-081 duration-string representation while
// retaining the old Path/Method fields for the current HTTP executor.
func (s WorkflowStepSpec) MarshalJSON() ([]byte, error) {
	var timeout any
	if s.Timeout != 0 {
		timeout = s.Timeout.String()
	}
	return json.Marshal(struct {
		Name         string             `json:"name"`
		Run          string             `json:"run,omitempty"`
		Input        json.RawMessage    `json:"input,omitempty"`
		Path         string             `json:"path,omitempty"`
		Method       string             `json:"method,omitempty"`
		DependsOn    []string           `json:"depends_on,omitempty"`
		WaitForEvent string             `json:"wait_for_event,omitempty"`
		Timeout      any                `json:"timeout,omitempty"`
		OnTimeout    string             `json:"on_timeout,omitempty"`
		Retry        *WorkflowRetrySpec `json:"retry,omitempty"`
	}{
		Name: s.Name, Run: s.Run, Input: s.Input, Path: s.Path, Method: s.Method,
		DependsOn: s.DependsOn, WaitForEvent: s.WaitForEvent, Timeout: timeout,
		OnTimeout: s.OnTimeout, Retry: s.Retry,
	})
}

// UnmarshalYAML converts YAML maps to the same JSON wire shape used by the
// deploy DTO. yaml.v3 can decode time.Duration strings, but it cannot decode
// an object directly into json.RawMessage, so the conversion is necessary for
// ADR-081's input object. Unknown keys are checked here because implementing a
// custom YAML unmarshaler bypasses yaml.Decoder.KnownFields for this struct.
func (s *WorkflowStepSpec) UnmarshalYAML(node *yaml.Node) error {
	var fields map[string]any
	if err := node.Decode(&fields); err != nil {
		return err
	}
	allowed := map[string]struct{}{
		"name": {}, "run": {}, "input": {}, "path": {}, "method": {},
		"depends_on": {}, "wait_for_event": {}, "timeout": {},
		"on_timeout": {}, "retry": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("workflow step: unknown field %q", key)
		}
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return s.UnmarshalJSON(b)
}

func decodeWorkflowTimeout(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("workflow: invalid timeout: %w", err)
		}
		d, err := parseWorkflowDuration(value)
		if err != nil {
			return 0, fmt.Errorf("workflow: invalid timeout %q: %w", value, err)
		}
		return d, nil
	}
	var nanos int64
	if err := json.Unmarshal(raw, &nanos); err != nil {
		return 0, fmt.Errorf("workflow: timeout must be a duration string: %w", err)
	}
	return time.Duration(nanos), nil
}

// parseWorkflowDuration follows time.ParseDuration and adds the day suffix
// used by ADR-081 examples (for example, "7d" for the maximum wait). Go's
// standard parser intentionally has no day unit because a day can be
// calendar-dependent; workflow waits are fixed 24-hour intervals, so the
// conversion is unambiguous here.
func parseWorkflowDuration(value string) (time.Duration, error) {
	if !strings.HasSuffix(value, "d") {
		return time.ParseDuration(value)
	}
	hours, err := time.ParseDuration(strings.TrimSuffix(value, "d") + "h")
	if err != nil {
		return 0, err
	}
	if hours > time.Duration(1<<63-1)/24 || hours < time.Duration(-1<<63)/24 {
		return 0, errors.New("duration exceeds time.Duration range")
	}
	return hours * 24, nil
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// ValidateWorkflowDAG validates the step dependencies, timeouts, retry
// policy, and cycles using Kahn's algorithm for topological sorting. Returns
// the deterministic sorted step execution order if valid.
func ValidateWorkflowDAG(spec WorkflowSpec, plan Plan) ([]string, error) {
	if !plan.WorkflowsAllowed() {
		return nil, fmt.Errorf("%w: %q", ErrWorkflowPlanNotAllowed, plan)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, ErrWorkflowNameRequired
	}
	if spec.Trigger != nil && spec.Trigger.Type != "manual" {
		return nil, fmt.Errorf("%w: %q", ErrWorkflowInvalidTrigger, spec.Trigger.Type)
	}
	if len(spec.Steps) == 0 {
		return nil, ErrWorkflowEmptySteps
	}

	stepMap := make(map[string]WorkflowStepSpec, len(spec.Steps))
	inDegree := make(map[string]int, len(spec.Steps))
	adj := make(map[string][]string, len(spec.Steps))

	maxTimeout := plan.WorkflowStepMaxTimeout()
	maxWaitDays := plan.WorkflowMaxWaitDays()
	maxWaitDuration := time.Duration(maxWaitDays) * 24 * time.Hour

	for _, step := range spec.Steps {
		if strings.TrimSpace(step.Name) == "" {
			return nil, errors.New("workflow: step name cannot be empty")
		}
		if _, exists := stepMap[step.Name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrWorkflowDuplicateStep, step.Name)
		}
		stepMap[step.Name] = step
		inDegree[step.Name] = 0

		hasRun := strings.TrimSpace(step.Run) != ""
		hasPath := strings.TrimSpace(step.Path) != ""
		hasEvent := strings.TrimSpace(step.WaitForEvent) != ""
		if boolCount(hasRun, hasPath, hasEvent) != 1 {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowInvalidStepTarget, step.Name)
		}
		if hasRun && !validWorkflowRunName(step.Run) {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowInvalidRun, step.Name)
		}
		if hasPath && !validWorkflowPath(step.Path) {
			return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowInvalidPath, step.Name, step.Path)
		}
		if step.Method != "" && !validWorkflowMethod(step.Method) {
			return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowInvalidMethod, step.Name, step.Method)
		}
		if len(step.Input) > 0 && !json.Valid(step.Input) {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowInvalidInput, step.Name)
		}
		if step.Retry != nil {
			if step.Retry.MaxAttempts < 1 || step.Retry.MaxAttempts > 25 ||
				(step.Retry.Backoff != "" && step.Retry.Backoff != "fixed" && step.Retry.Backoff != "exponential") {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowRetryInvalid, step.Name)
			}
		}

		if step.Timeout < 0 {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowTimeoutInvalid, step.Name)
		}
		if hasEvent {
			if step.Timeout < time.Second || maxWaitDays <= 0 || step.Timeout > maxWaitDuration {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowWaitTimeoutInvalid, step.Name)
			}
		} else if step.Timeout > 0 && (maxTimeout <= 0 || step.Timeout > maxTimeout) {
			return nil, fmt.Errorf("%w in step %q: %v > %v", ErrWorkflowTimeoutExceeded, step.Name, step.Timeout, maxTimeout)
		}
	}

	// Validate dependencies and on_timeout references.
	for _, step := range spec.Steps {
		if step.OnTimeout != "" {
			if _, ok := stepMap[step.OnTimeout]; !ok {
				return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowUnknownOnTimeout, step.Name, step.OnTimeout)
			}
		}

		seenDeps := make(map[string]struct{}, len(step.DependsOn))
		for _, dep := range step.DependsOn {
			if _, duplicate := seenDeps[dep]; duplicate {
				return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowDuplicateDependency, step.Name, dep)
			}
			seenDeps[dep] = struct{}{}
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

	// Kahn's algorithm with a sorted ready queue makes the returned order
	// stable across Go map iterations and therefore safe to persist/audit.
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)
	for name := range adj {
		sort.Strings(adj[name])
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
				sort.Strings(queue)
			}
		}
	}

	if len(sorted) != len(spec.Steps) {
		return nil, ErrWorkflowDAGCycle
	}

	return sorted, nil
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func validWorkflowRunName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && ((r >= '0' && r <= '9') || r == '-' || r == '.')) {
			continue
		}
		return false
	}
	return true
}

func validWorkflowPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "\r\n")
}

func validWorkflowMethod(value string) bool {
	switch strings.ToUpper(value) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}
