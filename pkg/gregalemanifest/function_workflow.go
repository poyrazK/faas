package gregalemanifest

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// FunctionWorkflowPath is the entry path of a workflow function's code
// workflow. The adapters answer workflow-mode requests on any path; "/" is
// the function's own (ADR-20261113120824 §1).
const FunctionWorkflowPath = "/"

// FunctionWorkflow makes a function a code-first workflow
// (ADR-20261113120824 §1). It is written `workflow: true`, or as a mapping
// with the code workflow's timeout, retry, trigger and max_concurrent_runs.
type FunctionWorkflow struct {
	Enabled           bool
	Timeout           json.RawMessage
	Retry             *api.WorkflowRetrySpec
	Trigger           *api.WorkflowTriggerSpec
	MaxConcurrentRuns int
}

// UnmarshalYAML accepts a boolean or a mapping.
func (w *FunctionWorkflow) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return fmt.Errorf("workflow: want true, false or a mapping: %w", err)
		}
		*w = FunctionWorkflow{Enabled: enabled}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow: want true, false or a mapping")
	}
	var fields map[string]any
	if err := node.Decode(&fields); err != nil {
		return err
	}
	out := FunctionWorkflow{Enabled: true}
	for key, value := range fields {
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("workflow.%s: %w", key, err)
		}
		switch key {
		case "timeout":
			out.Timeout = raw
		case "retry":
			out.Retry = &api.WorkflowRetrySpec{}
			if err := json.Unmarshal(raw, out.Retry); err != nil {
				return fmt.Errorf("workflow.retry: %w", err)
			}
		case "trigger":
			out.Trigger = &api.WorkflowTriggerSpec{}
			if err := json.Unmarshal(raw, out.Trigger); err != nil {
				return fmt.Errorf("workflow.trigger: %w", err)
			}
		case "max_concurrent_runs":
			if err := json.Unmarshal(raw, &out.MaxConcurrentRuns); err != nil {
				return fmt.Errorf("workflow.max_concurrent_runs: %w", err)
			}
		default:
			return fmt.Errorf("workflow: unknown field %q (want timeout, retry, trigger or max_concurrent_runs)", key)
		}
	}
	*w = out
	return nil
}

// MarshalYAML writes `true` when only enabled, else the mapping.
func (w FunctionWorkflow) MarshalYAML() (any, error) {
	if !w.Enabled {
		return false, nil
	}
	if len(w.Timeout) == 0 && w.Retry == nil && w.Trigger == nil && w.MaxConcurrentRuns == 0 {
		return true, nil
	}
	out := map[string]any{}
	if len(w.Timeout) > 0 {
		var timeout any
		_ = json.Unmarshal(w.Timeout, &timeout)
		out["timeout"] = timeout
	}
	if w.Retry != nil {
		out["retry"] = w.Retry
	}
	if w.Trigger != nil {
		out["trigger"] = w.Trigger
	}
	if w.MaxConcurrentRuns != 0 {
		out["max_concurrent_runs"] = w.MaxConcurrentRuns
	}
	return out, nil
}

// IsWorkflow reports whether the function is a workflow function.
func (fn FunctionSpec) IsWorkflow() bool { return fn.Workflow != nil && fn.Workflow.Enabled }

// WorkflowSpec is the code workflow a workflow function declares on its own
// app: named after the function, entered at "/", manually triggered unless
// the entry names a trigger.
func (fn FunctionSpec) WorkflowSpec() (api.WorkflowSpec, bool, error) {
	if !fn.IsWorkflow() {
		return api.WorkflowSpec{}, false, nil
	}
	wf := fn.Workflow
	codeJSON := map[string]any{"path": FunctionWorkflowPath}
	if len(wf.Timeout) > 0 {
		codeJSON["timeout"] = wf.Timeout
	}
	if wf.Retry != nil {
		codeJSON["retry"] = wf.Retry
	}
	raw, err := json.Marshal(codeJSON)
	if err != nil {
		return api.WorkflowSpec{}, false, err
	}
	var code api.WorkflowCodeSpec
	if err := json.Unmarshal(raw, &code); err != nil {
		return api.WorkflowSpec{}, false, fmt.Errorf("function %q: workflow: %w", fn.Name, err)
	}
	trigger := wf.Trigger
	if trigger == nil {
		trigger = &api.WorkflowTriggerSpec{Type: "manual"}
	}
	return api.WorkflowSpec{Name: fn.Name, Trigger: trigger, MaxConcurrentRuns: wf.MaxConcurrentRuns, Code: &code}, true, nil
}

// validateFunctionWorkflow checks a workflow function's derived workflow
// against plan, or only its shape when plan is empty.
func validateFunctionWorkflow(fn FunctionSpec, plan api.Plan) error {
	spec, ok, err := fn.WorkflowSpec()
	if err != nil || !ok {
		return err
	}
	if plan == "" {
		return nil
	}
	if !plan.WorkflowsAllowed() {
		return fmt.Errorf("function %q: workflow: plan %q does not allow workflows", fn.Name, plan)
	}
	if _, err := api.ValidateWorkflowDAG(spec, plan); err != nil {
		return fmt.Errorf("function %q: workflow: %w", fn.Name, err)
	}
	return nil
}
