package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var (
	ErrWorkflowEmptySteps              = errors.New("workflow: must have at least one step")
	ErrWorkflowNameRequired            = errors.New("workflow: name cannot be empty")
	ErrWorkflowPlanNotAllowed          = errors.New("workflow: plan does not allow workflows")
	ErrWorkflowInvalidTrigger          = errors.New("workflow: trigger type must be manual")
	ErrWorkflowDuplicateStep           = errors.New("workflow: duplicate step name")
	ErrWorkflowInvalidStepTarget       = errors.New("workflow: step must specify exactly one of run, path, wait_for_event, wait_for_callback, wait_for_duration, or wait_for_condition")
	ErrWorkflowInvalidPath             = errors.New("workflow: step path must start with '/'")
	ErrWorkflowInvalidMethod           = errors.New("workflow: step method is not supported")
	ErrWorkflowInvalidRun              = errors.New("workflow: step run cannot be empty")
	ErrWorkflowInvalidInput            = errors.New("workflow: step input must be valid JSON")
	ErrWorkflowUnknownDependency       = errors.New("workflow: step depends on unknown step")
	ErrWorkflowDuplicateDependency     = errors.New("workflow: step has a duplicate dependency")
	ErrWorkflowSelfDependency          = errors.New("workflow: step cannot depend on itself")
	ErrWorkflowDAGCycle                = errors.New("workflow: circular dependency detected in steps")
	ErrWorkflowTimeoutInvalid          = errors.New("workflow: step timeout cannot be negative")
	ErrWorkflowTimeoutExceeded         = errors.New("workflow: step timeout exceeds plan limit")
	ErrWorkflowWaitTimeoutInvalid      = errors.New("workflow: event or callback wait timeout must be between 1s and the plan limit")
	ErrWorkflowConditionTimeoutInvalid = errors.New("workflow: condition wait timeout must be between 1s and 7d")
	ErrWorkflowWaitDurationInvalid     = errors.New("workflow: wait_for_duration must be between 1s and the plan limit")
	ErrWorkflowWaitOptionsInvalid      = errors.New("workflow: wait_for_duration cannot have input, method, timeout, on_timeout, on_failure, or retry")
	ErrWorkflowCallbackOptionsInvalid  = errors.New("workflow: wait_for_callback cannot have input, method, or retry")
	ErrWorkflowConditionInvalid        = errors.New("workflow: wait_for_condition needs a valid checker, interval, and 1-1000 attempts")
	ErrWorkflowConditionOptionsInvalid = errors.New("workflow: wait_for_condition cannot have input, method, or retry")
	ErrWorkflowReservedEventName       = errors.New("workflow: wait_for_event name uses a reserved callback prefix")
	ErrWorkflowRetryInvalid            = errors.New("workflow: retry must have 1-25 attempts and fixed or exponential backoff")
	ErrWorkflowUnknownOnTimeout        = errors.New("workflow: on_timeout references unknown step")
	ErrWorkflowUnknownOnFailure        = errors.New("workflow: on_failure references unknown step")
	ErrWorkflowInvalidOnFailure        = errors.New("workflow: on_failure must target a distinct handler step and may only be used on handler steps")
	ErrWorkflowDuplicateFailureTarget  = errors.New("workflow: a failure handler can only handle one source step")
	ErrWorkflowFailureHandlerDependent = errors.New("workflow: an on_failure handler cannot have dependent steps")
	ErrWorkflowInvalidFailureContext   = errors.New("workflow: failure context is only available in an on_failure handler")
	ErrWorkflowInputTemplateInvalid    = errors.New("workflow: invalid step input template")
	ErrWorkflowInputOutputDependency   = errors.New("workflow: step output references must name a direct dependency")
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
	Name             string                 `json:"name" yaml:"name" toml:"name"`
	Run              string                 `json:"run,omitempty" yaml:"run,omitempty" toml:"run,omitempty"`
	Input            json.RawMessage        `json:"input,omitempty" yaml:"input,omitempty" toml:"input,omitempty"`
	Path             string                 `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Method           string                 `json:"method,omitempty" yaml:"method,omitempty" toml:"method,omitempty"`
	DependsOn        []string               `json:"depends_on,omitempty" yaml:"depends_on,omitempty" toml:"depends_on,omitempty"`
	WaitForEvent     string                 `json:"wait_for_event,omitempty" yaml:"wait_for_event,omitempty" toml:"wait_for_event,omitempty"`
	WaitForCallback  bool                   `json:"wait_for_callback,omitempty" yaml:"wait_for_callback,omitempty" toml:"wait_for_callback,omitempty"`
	WaitForDuration  time.Duration          `json:"wait_for_duration,omitempty" yaml:"wait_for_duration,omitempty" toml:"wait_for_duration,omitempty"`
	WaitForCondition *WorkflowConditionSpec `json:"wait_for_condition,omitempty" yaml:"wait_for_condition,omitempty" toml:"wait_for_condition,omitempty"`
	Timeout          time.Duration          `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	OnTimeout        string                 `json:"on_timeout,omitempty" yaml:"on_timeout,omitempty" toml:"on_timeout,omitempty"`
	OnFailure        string                 `json:"on_failure,omitempty" yaml:"on_failure,omitempty" toml:"on_failure,omitempty"`
	Retry            *WorkflowRetrySpec     `json:"retry,omitempty" yaml:"retry,omitempty" toml:"retry,omitempty"`
}

// WorkflowConditionSpec calls a named checker on a durable schedule. A checker
// returns a JSON object with a required boolean `done` field; its full result
// becomes the next check's input and, when done, the step output.
type WorkflowConditionSpec struct {
	Run         string        `json:"run" yaml:"run" toml:"run"`
	Interval    time.Duration `json:"interval" yaml:"interval" toml:"interval"`
	MaxAttempts int           `json:"max_attempts" yaml:"max_attempts" toml:"max_attempts"`
}

func (c *WorkflowConditionSpec) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		if key != "run" && key != "interval" && key != "max_attempts" {
			return fmt.Errorf("workflow condition: unknown field %q", key)
		}
	}
	var wire struct {
		Run         string          `json:"run"`
		Interval    json.RawMessage `json:"interval"`
		MaxAttempts int             `json:"max_attempts"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	interval, err := decodeWorkflowDuration(wire.Interval, "wait_for_condition.interval")
	if err != nil {
		return err
	}
	*c = WorkflowConditionSpec{Run: wire.Run, Interval: interval, MaxAttempts: wire.MaxAttempts}
	return nil
}

func (c WorkflowConditionSpec) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Run         string `json:"run"`
		Interval    string `json:"interval"`
		MaxAttempts int    `json:"max_attempts"`
	}{c.Run, c.Interval.String(), c.MaxAttempts})
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
		"depends_on": {}, "wait_for_event": {}, "wait_for_callback": {}, "wait_for_duration": {}, "wait_for_condition": {}, "timeout": {},
		"on_timeout": {}, "on_failure": {}, "retry": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("workflow step: unknown field %q", key)
		}
	}

	type wire struct {
		Name             string                 `json:"name"`
		Run              string                 `json:"run"`
		Input            json.RawMessage        `json:"input"`
		Path             string                 `json:"path"`
		Method           string                 `json:"method"`
		DependsOn        []string               `json:"depends_on"`
		WaitForEvent     string                 `json:"wait_for_event"`
		WaitForCallback  bool                   `json:"wait_for_callback"`
		WaitForDuration  json.RawMessage        `json:"wait_for_duration"`
		WaitForCondition *WorkflowConditionSpec `json:"wait_for_condition"`
		Timeout          json.RawMessage        `json:"timeout"`
		OnTimeout        string                 `json:"on_timeout"`
		OnFailure        string                 `json:"on_failure"`
		Retry            *WorkflowRetrySpec     `json:"retry"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	timeout, err := decodeWorkflowTimeout(w.Timeout)
	if err != nil {
		return err
	}
	waitForDuration, err := decodeWorkflowDuration(w.WaitForDuration, "wait_for_duration")
	if err != nil {
		return err
	}
	*s = WorkflowStepSpec{
		Name: w.Name, Run: w.Run, Input: cloneRawJSON(w.Input), Path: w.Path,
		Method: w.Method, DependsOn: append([]string(nil), w.DependsOn...),
		WaitForEvent: w.WaitForEvent, WaitForCallback: w.WaitForCallback,
		WaitForDuration:  waitForDuration,
		WaitForCondition: w.WaitForCondition,
		Timeout:          timeout, OnTimeout: w.OnTimeout, OnFailure: w.OnFailure,
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
	var waitForDuration any
	if s.WaitForDuration != 0 {
		waitForDuration = s.WaitForDuration.String()
	}
	return json.Marshal(struct {
		Name             string                 `json:"name"`
		Run              string                 `json:"run,omitempty"`
		Input            json.RawMessage        `json:"input,omitempty"`
		Path             string                 `json:"path,omitempty"`
		Method           string                 `json:"method,omitempty"`
		DependsOn        []string               `json:"depends_on,omitempty"`
		WaitForEvent     string                 `json:"wait_for_event,omitempty"`
		WaitForCallback  bool                   `json:"wait_for_callback,omitempty"`
		WaitForDuration  any                    `json:"wait_for_duration,omitempty"`
		WaitForCondition *WorkflowConditionSpec `json:"wait_for_condition,omitempty"`
		Timeout          any                    `json:"timeout,omitempty"`
		OnTimeout        string                 `json:"on_timeout,omitempty"`
		OnFailure        string                 `json:"on_failure,omitempty"`
		Retry            *WorkflowRetrySpec     `json:"retry,omitempty"`
	}{
		Name: s.Name, Run: s.Run, Input: s.Input, Path: s.Path, Method: s.Method,
		DependsOn: s.DependsOn, WaitForEvent: s.WaitForEvent,
		WaitForCallback: s.WaitForCallback,
		WaitForDuration: waitForDuration, WaitForCondition: s.WaitForCondition, Timeout: timeout,
		OnTimeout: s.OnTimeout, OnFailure: s.OnFailure, Retry: s.Retry,
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
		"depends_on": {}, "wait_for_event": {}, "wait_for_callback": {}, "wait_for_duration": {}, "wait_for_condition": {}, "timeout": {},
		"on_timeout": {}, "on_failure": {}, "retry": {},
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
	return decodeWorkflowDuration(raw, "timeout")
}

func decodeWorkflowDuration(raw json.RawMessage, field string) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("workflow: invalid %s: %w", field, err)
		}
		d, err := parseWorkflowDuration(value)
		if err != nil {
			return 0, fmt.Errorf("workflow: invalid %s %q: %w", field, value, err)
		}
		return d, nil
	}
	var nanos int64
	if err := json.Unmarshal(raw, &nanos); err != nil {
		return 0, fmt.Errorf("workflow: %s must be a duration string: %w", field, err)
	}
	return time.Duration(nanos), nil
}

// parseWorkflowDuration follows time.ParseDuration and adds the day suffix
// used by ADR-081 examples (for example, "365d" for a Scale wait). Go's
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
	conditionMaxWaitDuration := 7 * 24 * time.Hour

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
		hasTimer := step.WaitForDuration != 0
		hasCondition := step.WaitForCondition != nil
		if boolCount(hasRun, hasPath, hasEvent, step.WaitForCallback, hasTimer, hasCondition) != 1 {
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
		if hasTimer {
			if step.WaitForDuration < time.Second || maxWaitDays <= 0 || step.WaitForDuration > maxWaitDuration {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowWaitDurationInvalid, step.Name)
			}
			if len(step.Input) > 0 || step.Method != "" || step.Timeout != 0 || step.OnTimeout != "" || step.Retry != nil {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowWaitOptionsInvalid, step.Name)
			}
		}
		if step.WaitForCallback && (len(step.Input) > 0 || step.Method != "" || step.Retry != nil) {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowCallbackOptionsInvalid, step.Name)
		}
		if hasCondition {
			condition := step.WaitForCondition
			if !validWorkflowRunName(condition.Run) || condition.Interval < time.Minute || condition.Interval > conditionMaxWaitDuration || condition.MaxAttempts < 1 || condition.MaxAttempts > 1000 {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowConditionInvalid, step.Name)
			}
			if len(step.Input) > 0 || step.Method != "" || step.Retry != nil {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowConditionOptionsInvalid, step.Name)
			}
		}
		if hasEvent && strings.HasPrefix(step.WaitForEvent, workflowCallbackEventPrefix) {
			return nil, fmt.Errorf("%w in step %q", ErrWorkflowReservedEventName, step.Name)
		}
		if hasEvent || step.WaitForCallback {
			if step.Timeout < time.Second || maxWaitDays <= 0 || step.Timeout > maxWaitDuration {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowWaitTimeoutInvalid, step.Name)
			}
		} else if hasCondition {
			if step.Timeout < time.Second || step.Timeout > conditionMaxWaitDuration {
				return nil, fmt.Errorf("%w in step %q", ErrWorkflowConditionTimeoutInvalid, step.Name)
			}
		} else if step.Timeout > 0 && (maxTimeout <= 0 || step.Timeout > maxTimeout) {
			return nil, fmt.Errorf("%w in step %q: %v > %v", ErrWorkflowTimeoutExceeded, step.Name, step.Timeout, maxTimeout)
		}
	}

	// Validate exception routes before input references so failure templates
	// can be restricted to the handler that owns that failure context.
	failureHandlers := make(map[string]string)
	timeoutHandlers := make(map[string]struct{})
	for _, step := range spec.Steps {
		if step.OnTimeout != "" {
			if _, ok := stepMap[step.OnTimeout]; !ok {
				return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowUnknownOnTimeout, step.Name, step.OnTimeout)
			}
			timeoutHandlers[step.OnTimeout] = struct{}{}
		}
		if step.OnFailure == "" {
			continue
		}
		sourceIsHandler := strings.TrimSpace(step.Run) != "" || strings.TrimSpace(step.Path) != ""
		target, targetExists := stepMap[step.OnFailure]
		if !targetExists {
			return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowUnknownOnFailure, step.Name, step.OnFailure)
		}
		targetIsHandler := strings.TrimSpace(target.Run) != "" || strings.TrimSpace(target.Path) != ""
		if !sourceIsHandler || !targetIsHandler || step.OnFailure == step.Name {
			return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowInvalidOnFailure, step.Name, step.OnFailure)
		}
		if _, duplicate := failureHandlers[step.OnFailure]; duplicate {
			return nil, fmt.Errorf("%w: %q", ErrWorkflowDuplicateFailureTarget, step.OnFailure)
		}
		failureHandlers[step.OnFailure] = step.Name
	}
	for targetName := range failureHandlers {
		target := stepMap[targetName]
		if target.OnFailure != "" {
			return nil, fmt.Errorf("%w: failure handler %q cannot route its own failure", ErrWorkflowInvalidOnFailure, targetName)
		}
		if _, isTimeoutHandler := timeoutHandlers[targetName]; isTimeoutHandler {
			return nil, fmt.Errorf("%w: %q is already an on_timeout handler", ErrWorkflowInvalidOnFailure, targetName)
		}
	}
	for _, step := range spec.Steps {
		for _, dependency := range step.DependsOn {
			if step.Name == dependency {
				continue // The DAG validation below reports self-dependencies.
			}
			if _, isFailureHandler := failureHandlers[dependency]; isFailureHandler {
				return nil, fmt.Errorf("%w: %q", ErrWorkflowFailureHandlerDependent, dependency)
			}
		}
	}

	// Validate dependencies and input references.
	stepNames := make([]string, 0, len(stepMap))
	for name := range stepMap {
		stepNames = append(stepNames, name)
	}
	for _, step := range spec.Steps {
		refs, err := workflowInputReferences(step.Input, stepNames)
		if err != nil {
			return nil, fmt.Errorf("%w in step %q: %v", ErrWorkflowInputTemplateInvalid, step.Name, err)
		}
		dependencies := make(map[string]struct{}, len(step.DependsOn))
		for _, dep := range step.DependsOn {
			dependencies[dep] = struct{}{}
		}
		for _, ref := range refs {
			if ref.Source == workflowInputStepOutput {
				if failureHandlers[step.Name] == ref.StepName {
					return nil, fmt.Errorf("%w: read failure details through {{failure...}} in step %q", ErrWorkflowInvalidFailureContext, step.Name)
				}
				if _, ok := dependencies[ref.StepName]; !ok {
					return nil, fmt.Errorf("%w in step %q: %q", ErrWorkflowInputOutputDependency, step.Name, ref.StepName)
				}
			}
			if ref.Source == workflowInputFailure {
				if _, isFailureHandler := failureHandlers[step.Name]; !isFailureHandler {
					return nil, fmt.Errorf("%w in step %q", ErrWorkflowInvalidFailureContext, step.Name)
				}
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

const workflowCallbackEventPrefix = "workflow.callback."

// WorkflowCallbackID is a stable, account-authenticated callback handle for
// one step of one run. It is an identifier, not a bearer credential.
func WorkflowCallbackID(runID, stepName string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale-workflow-callback\x00"+runID+"\x00"+stepName)).String()
}

// WorkflowCallbackEventName keeps callback completions separate from ordinary
// wait_for_event names, even when several steps wait within the same run.
func WorkflowCallbackEventName(runID, stepName string) string {
	return workflowCallbackEventPrefix + WorkflowCallbackID(runID, stepName)
}

func IsWorkflowCallbackEventName(eventName string) bool {
	return strings.HasPrefix(eventName, workflowCallbackEventPrefix)
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

// WorkflowRunResponse is the API wire representation of a workflow run.
type WorkflowRunResponse struct {
	ID           string          `json:"id"`
	AppID        string          `json:"app_id"`
	WorkflowName string          `json:"workflow_name"`
	Status       string          `json:"status"`
	CurrentStep  *string         `json:"current_step,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	ScheduledFor string          `json:"scheduled_for"`
	StartedAt    *string         `json:"started_at,omitempty"`
	FinishedAt   *string         `json:"finished_at,omitempty"`
	LastError    *string         `json:"last_error,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

// ListWorkflowRunsResponse is the payload returned by GET /v1/apps/{slug}/workflows/runs.
type ListWorkflowRunsResponse struct {
	Runs  []WorkflowRunResponse `json:"runs"`
	Total int                   `json:"total"`
}

// ValidWorkflowRunStatus reports the closed set accepted by the workflow-run
// list filter. Empty is intentionally valid and means no status filter.
func ValidWorkflowRunStatus(status string) bool {
	switch status {
	case "", "pending", "running", "awaiting_event", "succeeded", "failed", "dead":
		return true
	default:
		return false
	}
}

// WorkflowStepResponse is the API wire representation of an executed step.
type WorkflowStepResponse struct {
	StepName    string          `json:"step_name"`
	Status      string          `json:"status"`
	Attempt     int             `json:"attempt"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	StartedAt   *string         `json:"started_at,omitempty"`
	NextCheckAt *string         `json:"next_check_at,omitempty"`
	FinishedAt  *string         `json:"finished_at,omitempty"`
	Error       *string         `json:"error,omitempty"`
	CreatedAt   string          `json:"created_at"`
}

// ListWorkflowStepsResponse is returned by GET /v1/workflows/runs/{id}/steps.
type ListWorkflowStepsResponse struct {
	Steps []WorkflowStepResponse `json:"steps"`
}

// WorkflowStepAttemptResponse is one durable executor invocation for a step.
type WorkflowStepAttemptResponse struct {
	Attempt       int     `json:"attempt"`
	Status        string  `json:"status"`
	HTTPStatus    *int    `json:"http_status,omitempty"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    *string `json:"finished_at,omitempty"`
	NextAttemptAt *string `json:"next_attempt_at,omitempty"`
	Error         *string `json:"error,omitempty"`
}

// ListWorkflowStepAttemptsResponse is returned by
// GET /v1/workflows/runs/{id}/steps/{step}/attempts.
type ListWorkflowStepAttemptsResponse struct {
	Attempts []WorkflowStepAttemptResponse `json:"attempts"`
}

// InjectWorkflowEventRequest is the body for POST /v1/workflows/runs/{id}/events.
type InjectWorkflowEventRequest struct {
	EventName string          `json:"event_name"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// InjectWorkflowEventResponse is returned on successful event receipt.
type InjectWorkflowEventResponse struct {
	Status    string `json:"status"`
	EventName string `json:"event_name"`
}

// WorkflowCallbackResponse identifies an authenticated callback wait. The ID
// can be supplied to a trusted service but carries no authority by itself.
type WorkflowCallbackResponse struct {
	ID        string  `json:"id"`
	StepName  string  `json:"step_name"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

type ListWorkflowCallbacksResponse struct {
	Callbacks []WorkflowCallbackResponse `json:"callbacks"`
}

type CompleteWorkflowCallbackResponse struct {
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate"`
}
