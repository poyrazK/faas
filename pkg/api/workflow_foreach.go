package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const WorkflowForEachInternalPrefix = "_foreach."

var ErrWorkflowForEachInvalid = errors.New("workflow: invalid for_each")

var ErrWorkflowForEachItemLimit = errors.New("workflow: for_each item limit exceeded")

// WorkflowForEachSpec applies one action to a snapshotted JSON array.
// Action templates read input.item, input.index and input.input.
type WorkflowForEachSpec struct {
	Items         string                    `json:"items" yaml:"items" toml:"items"`
	Action        WorkflowForEachActionSpec `json:"action" yaml:"action" toml:"action"`
	MaxParallel   int                       `json:"max_parallel,omitempty" yaml:"max_parallel,omitempty" toml:"max_parallel,omitempty"`
	OnItemFailure string                    `json:"on_item_failure,omitempty" yaml:"on_item_failure,omitempty" toml:"on_item_failure,omitempty"`
}

type WorkflowForEachActionSpec struct {
	Run      string                `json:"run,omitempty" yaml:"run,omitempty" toml:"run,omitempty"`
	Path     string                `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Outbound *WorkflowOutboundSpec `json:"outbound,omitempty" yaml:"outbound,omitempty" toml:"outbound,omitempty"`
	Method   string                `json:"method,omitempty" yaml:"method,omitempty" toml:"method,omitempty"`
	Input    json.RawMessage       `json:"input,omitempty" yaml:"input,omitempty" toml:"input,omitempty"`
	Timeout  time.Duration         `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	Retry    *WorkflowRetrySpec    `json:"retry,omitempty" yaml:"retry,omitempty" toml:"retry,omitempty"`
	When     *WorkflowGuardSpec    `json:"when,omitempty" yaml:"when,omitempty" toml:"when,omitempty"`
}

func (a WorkflowForEachActionSpec) Step(name string) WorkflowStepSpec {
	return WorkflowStepSpec{Name: name, Run: a.Run, Path: a.Path, Outbound: a.Outbound, Method: a.Method, Input: a.Input, Timeout: a.Timeout, Retry: a.Retry, When: a.When}
}

func (a *WorkflowForEachActionSpec) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for field := range fields {
		switch field {
		case "run", "path", "outbound", "method", "input", "timeout", "retry", "when":
		default:
			return fmt.Errorf("%w: unsupported action field %q", ErrWorkflowForEachInvalid, field)
		}
	}
	var step WorkflowStepSpec
	if err := json.Unmarshal(raw, &step); err != nil {
		return err
	}
	*a = WorkflowForEachActionSpec{Run: step.Run, Path: step.Path, Outbound: step.Outbound, Method: step.Method, Input: step.Input, Timeout: step.Timeout, Retry: step.Retry, When: step.When}
	return nil
}

func (a WorkflowForEachActionSpec) MarshalJSON() ([]byte, error) {
	type plain WorkflowForEachActionSpec
	var timeout any
	if a.Timeout != 0 {
		timeout = a.Timeout.String()
	}
	return json.Marshal(struct {
		plain
		Timeout any `json:"timeout,omitempty"`
	}{plain(a), timeout})
}

func (f *WorkflowForEachSpec) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for field := range fields {
		if field != "items" && field != "action" && field != "max_parallel" && field != "on_item_failure" {
			return fmt.Errorf("%w: unknown field %q", ErrWorkflowForEachInvalid, field)
		}
	}
	type plain WorkflowForEachSpec
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*f = WorkflowForEachSpec(value)
	return nil
}

func ValidateWorkflowForEach(step WorkflowStepSpec, names []string, plan Plan) error {
	f := step.ForEach
	if f == nil {
		return nil
	}
	if len(step.Name) > WorkflowForEachNameMaxBytes || len(step.Input) != 0 || step.Method != "" || step.Timeout != 0 || step.Retry != nil || step.OnFailure != "" || step.OnTimeout != "" {
		return ErrWorkflowForEachInvalid
	}
	if f.OnItemFailure != "" && f.OnItemFailure != "continue" {
		return fmt.Errorf("%w: on_item_failure must be continue when set", ErrWorkflowForEachInvalid)
	}
	if f.MaxParallel < 0 || f.MaxParallel > WorkflowForEachMaxParallelLimit {
		return fmt.Errorf("%w: max_parallel must be 0 through %d", ErrWorkflowForEachInvalid, WorkflowForEachMaxParallelLimit)
	}
	// References use the ordinary path grammar, without expression delimiters.
	if strings.ContainsAny(f.Items, "{}") || strings.TrimSpace(f.Items) != f.Items || f.Items == "" {
		return ErrWorkflowForEachInvalid
	}
	template, _ := json.Marshal("{{" + f.Items + "}}")
	refs, err := workflowInputReferences(template, names)
	if err != nil || len(refs) != 1 || refs[0].Source == workflowInputFailure {
		return ErrWorkflowForEachInvalid
	}
	if refs[0].Source == workflowInputStepOutput && !workflowForEachDependency(step.DependsOn, refs[0].StepName) {
		return ErrWorkflowForEachInvalid
	}
	action := f.Action.Step(step.Name)
	// Validate the target/options via the ordinary DAG. Check action template
	// references separately against the parent's declared dependencies.
	action.Input = nil
	guard := action.When
	action.When = nil
	if boolCount(action.Run != "", action.Path != "", action.Outbound != nil) != 1 {
		return ErrWorkflowForEachInvalid
	}
	if _, err := ValidateWorkflowDAG(WorkflowSpec{Name: "for_each_action", Steps: []WorkflowStepSpec{action}}, plan); err != nil {
		return fmt.Errorf("%w: %w", ErrWorkflowForEachInvalid, err)
	}
	if f.Action.Outbound != nil {
		if err := ValidateWorkflowOutboundStep(f.Action.Step(step.Name)); err != nil {
			return fmt.Errorf("%w: %w", ErrWorkflowForEachInvalid, err)
		}
	}
	if err := ValidateWorkflowGuard(guard, step.DependsOn); err != nil {
		return fmt.Errorf("%w: %w", ErrWorkflowForEachInvalid, err)
	}
	refs, err = workflowInputReferences(f.Action.Input, names)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWorkflowForEachInvalid, err)
	}
	outboundRefs, err := workflowOutboundInputReferences(f.Action.Outbound, names)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWorkflowForEachInvalid, err)
	}
	refs = append(refs, outboundRefs...)
	for _, ref := range refs {
		if ref.Source == workflowInputFailure || (ref.Source == workflowInputStepOutput && !workflowForEachDependency(step.DependsOn, ref.StepName)) {
			return ErrWorkflowForEachInvalid
		}
	}
	return nil
}

func workflowForEachDependency(dependencies []string, name string) bool {
	for _, dependency := range dependencies {
		if dependency == name {
			return true
		}
	}
	return false
}

func WorkflowForEachItemName(parent string, index int) string {
	return WorkflowForEachInternalPrefix + base64.RawURLEncoding.EncodeToString([]byte(parent)) + "." + strconv.Itoa(index)
}

func WorkflowForEachItemIdentity(name string) (parent string, index int, ok bool) {
	if !strings.HasPrefix(name, WorkflowForEachInternalPrefix) {
		return "", 0, false
	}
	encoded, rawIndex, found := strings.Cut(strings.TrimPrefix(name, WorkflowForEachInternalPrefix), ".")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	index, indexErr := strconv.Atoi(rawIndex)
	if !found || err != nil || indexErr != nil || index < 0 || index >= WorkflowForEachMaxItems || strconv.Itoa(index) != rawIndex || len(decoded) == 0 || len(decoded) > WorkflowForEachNameMaxBytes {
		return "", 0, false
	}
	parent = string(decoded)
	return parent, index, WorkflowForEachItemName(parent, index) == name
}

// WorkflowRuntimeStep resolves only statically defined steps or canonical item
// identities whose action comes from the immutable parent definition.
func WorkflowRuntimeStep(snapshot json.RawMessage, name string) *WorkflowStepSpec {
	var spec WorkflowSpec
	if json.Unmarshal(snapshot, &spec) != nil {
		return nil
	}
	for _, step := range spec.Steps {
		if step.Name == name {
			return &step
		}
	}
	parent, _, ok := WorkflowForEachItemIdentity(name)
	if !ok {
		return nil
	}
	for _, step := range spec.Steps {
		if step.Name == parent && step.ForEach != nil {
			action := step.ForEach.Action.Step(name)
			return &action
		}
	}
	return nil
}

// ResolveWorkflowForEachInputs materializes the entire batch before any action
// starts. Referenced values inserted into templates are never interpreted again.
func ResolveWorkflowForEachInputs(step WorkflowStepSpec, input json.RawMessage, outputs map[string]json.RawMessage) (json.RawMessage, []json.RawMessage, error) {
	items, inputs, _, err := resolveWorkflowForEachInputsWithGuards(step, input, outputs, false)
	return items, inputs, err
}

// ResolveWorkflowForEachInputsWithGuards snapshots each action input and its
// per-item guard decision before any item is dispatched.
func ResolveWorkflowForEachInputsWithGuards(step WorkflowStepSpec, input json.RawMessage, outputs map[string]json.RawMessage) (json.RawMessage, []json.RawMessage, []*bool, error) {
	return resolveWorkflowForEachInputsWithGuards(step, input, outputs, false)
}

// ResolveWorkflowForEachInputsWithGuardsBounded checks each expansion before
// serialization while returning the snapshotted per-item guard decisions.
func ResolveWorkflowForEachInputsWithGuardsBounded(step WorkflowStepSpec, input json.RawMessage, outputs map[string]json.RawMessage) (json.RawMessage, []json.RawMessage, []*bool, error) {
	return resolveWorkflowForEachInputsWithGuards(step, input, outputs, true)
}

// ResolveWorkflowForEachInputsBounded checks each expansion before serialization.
func ResolveWorkflowForEachInputsBounded(step WorkflowStepSpec, input json.RawMessage, outputs map[string]json.RawMessage) (json.RawMessage, []json.RawMessage, error) {
	items, inputs, _, err := resolveWorkflowForEachInputsWithGuards(step, input, outputs, true)
	return items, inputs, err
}

func resolveWorkflowForEachInputsWithGuards(step WorkflowStepSpec, input json.RawMessage, outputs map[string]json.RawMessage, bounded bool) (json.RawMessage, []json.RawMessage, []*bool, error) {
	resolve := func(template, input json.RawMessage, maxBytes int64) (json.RawMessage, error) {
		if bounded {
			return ResolveWorkflowStepInputBounded(template, input, outputs, nil, maxBytes)
		}
		return ResolveWorkflowStepInput(template, input, outputs, nil)
	}
	template, _ := json.Marshal("{{" + step.ForEach.Items + "}}")
	itemsRaw, err := resolve(template, input, WorkflowForEachMaxInputBytes)
	if bounded && errors.Is(err, ErrWorkflowInputLimit) {
		return nil, nil, nil, err
	}
	if err != nil || int64(len(itemsRaw)) > WorkflowForEachMaxInputBytes || len(itemsRaw) == 0 || itemsRaw[0] != '[' {
		return nil, nil, nil, ErrWorkflowForEachInvalid
	}
	var items []json.RawMessage
	if json.Unmarshal(itemsRaw, &items) != nil {
		return nil, nil, nil, ErrWorkflowForEachInvalid
	}
	if len(items) > WorkflowForEachMaxItems {
		if bounded {
			return nil, nil, nil, ErrWorkflowForEachItemLimit
		}
		return nil, nil, nil, ErrWorkflowForEachInvalid
	}
	inputs := make([]json.RawMessage, len(items))
	var matches []*bool
	if step.ForEach.Action.When != nil {
		matches = make([]*bool, len(items))
	}
	var total int64
	for index, item := range items {
		resolved := cloneRawJSON(item)
		var context json.RawMessage
		if len(step.ForEach.Action.Input) != 0 || step.ForEach.Action.When != nil {
			encoded, marshalErr := json.Marshal(struct {
				Item  json.RawMessage `json:"item"`
				Index int             `json:"index"`
				Input json.RawMessage `json:"input"`
			}{item, index, input})
			if marshalErr != nil {
				return nil, nil, nil, ErrWorkflowForEachInvalid
			}
			context = encoded
		}
		if len(step.ForEach.Action.Input) != 0 {
			resolved, err = resolve(step.ForEach.Action.Input, context, WorkflowForEachMaxInputBytes-total)
			if bounded && errors.Is(err, ErrWorkflowInputLimit) {
				return nil, nil, nil, err
			}
			if err != nil {
				return nil, nil, nil, ErrWorkflowForEachInvalid
			}
		}
		if step.ForEach.Action.When != nil {
			matched, guardErr := EvaluateWorkflowGuard(step.ForEach.Action.When, context, outputs)
			if guardErr != nil {
				return nil, nil, nil, ErrWorkflowForEachInvalid
			}
			matches[index] = &matched
		}
		total += int64(len(resolved))
		if total > WorkflowForEachMaxInputBytes {
			return nil, nil, nil, ErrWorkflowForEachInvalid
		}
		inputs[index] = resolved
	}
	return itemsRaw, inputs, matches, nil
}
