// Package runpipeline implements version-pinned, bounded Run orchestration.
package runpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

// Plan is encrypted using the workflow envelope. Authority is captured at
// submission; workers never infer administrative authority from a principal ID.
type Plan struct {
	AgentFiles map[string][]runfiles.AgentFilePin        `json:"agent_files,omitempty"`
	Request    api.CreateManagedExecutionWorkflowRequest `json:"request"`
	Policy     state.ExecutionPipelinePolicy             `json:"policy"`
}
type Compiler struct {
	EndpointID string
	Lookup     func(context.Context, string, int) (state.ExecutionTaskVersion, error)
	Open       func(context.Context, state.ExecutionTaskVersion) (api.PublishExecutionTaskRequest, error)
}

func Requested(r api.CreateManagedExecutionWorkflowRequest) bool {
	if r.Pipeline != nil || r.Batch != nil {
		return true
	}
	for _, s := range r.Steps {
		if s.Task != nil || s.Retry != nil {
			return true
		}
	}
	return false
}
func Normalize(r api.CreateManagedExecutionWorkflowRequest) (api.CreateManagedExecutionWorkflowRequest, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return r, fmt.Errorf("invalid pipeline JSON")
	}
	var cloned api.CreateManagedExecutionWorkflowRequest
	if err = json.Unmarshal(raw, &cloned); err != nil {
		return r, err
	}
	r = cloned
	if r.Batch != nil {
		b := r.Batch
		if len(r.Steps) != 0 || len(b.Items) < 1 || len(b.Items) > api.ExecutionWorkflowManagedMaxSteps || !runtask.ValidRef(b.Name, b.Version) {
			return r, fmt.Errorf("batch requires a pinned task, 1..%d items, and no steps", api.ExecutionWorkflowManagedMaxSteps)
		}
		for _, item := range b.Items {
			r.Steps = append(r.Steps, api.CreateManagedExecutionWorkflowStep{Label: item.Label, Task: &api.InvokeExecutionTaskRequest{Name: b.Name, Version: b.Version, CustomerRef: b.CustomerRef, Input: item.Input, InputFiles: item.InputFiles}, Retry: b.Retry})
		}
		r.Batch = nil
	}
	if r.Pipeline == nil {
		r.Pipeline = &api.ExecutionPipelineOptions{}
	}
	options := *r.Pipeline
	r.Pipeline = &options
	if options.TotalTimeoutMS == 0 {
		r.Pipeline.TotalTimeoutMS = api.ExecutionPipelineDefaultTimeoutMS
	}
	if r.Pipeline.TotalTimeoutMS < 1 || r.Pipeline.TotalTimeoutMS > api.ExecutionPipelineMaxTimeoutMS {
		return r, fmt.Errorf("pipeline total_timeout_ms must be 1..%d", api.ExecutionPipelineMaxTimeoutMS)
	}
	for i := range r.Steps {
		s := &r.Steps[i]
		retry, err := NormalizeRetry(s.Retry)
		if err != nil {
			return r, fmt.Errorf("step %q: %w", s.Label, err)
		}
		s.Retry = &retry
		if s.Task != nil {
			t := s.Task
			if !reflect.ValueOf(s.Request).IsZero() || !runtask.ValidRef(t.Name, t.Version) || t.WorkflowID != "" || t.StepLabel != "" || len(t.ArtifactInputs) > 0 {
				return r, fmt.Errorf("step %q requires only a pinned task with input/input_files/customer_ref; workflow fields are server-owned", s.Label)
			}
			if (s.InputFromPreviousResult || s.IncludeDependencyResults) && len(t.Input) > 0 {
				return r, fmt.Errorf("step %q cannot combine static task input with dependency input", s.Label)
			}
		}
	}
	// Check dependency/output declarations after task definitions are opened.
	return r, nil
}
func NormalizeRetry(p *api.ExecutionRetryPolicy) (api.ExecutionRetryPolicy, error) {
	out := api.ExecutionRetryPolicy{MaxAttempts: 1, BackoffMS: api.ExecutionPipelineMinBackoffMS}
	if p != nil {
		out = *p
		if out.BackoffMS == 0 {
			out.BackoffMS = api.ExecutionPipelineMinBackoffMS
		}
	}
	if out.MaxAttempts < 1 || out.MaxAttempts > api.ExecutionPipelineMaxAttempts || out.BackoffMS < api.ExecutionPipelineMinBackoffMS || out.BackoffMS > api.ExecutionPipelineMaxBackoffMS || out.MaxAttempts > 1 && !out.SafeToRepeat {
		return out, fmt.Errorf("retry requires max_attempts 1..%d, backoff_ms %d..%d, and safe_to_repeat=true for multiple attempts", api.ExecutionPipelineMaxAttempts, api.ExecutionPipelineMinBackoffMS, api.ExecutionPipelineMaxBackoffMS)
	}
	return out, nil
}
func (c Compiler) Compile(ctx context.Context, r api.CreateManagedExecutionWorkflowRequest, wide, controlWide bool) (Plan, error) {
	r, err := Normalize(r)
	if err != nil {
		return Plan{}, err
	}
	policy := state.ExecutionPipelinePolicy{Wide: wide, ControlWide: controlWide, TotalTimeoutMS: r.Pipeline.TotalTimeoutMS}
	for i := range r.Steps {
		step := &r.Steps[i]
		p := state.ExecutionPipelineStep{Label: step.Label, Retry: *step.Retry}
		if c.EndpointID != "" && step.Task == nil {
			return Plan{}, api.ErrValidation("agent pipelines require named task steps")
		}
		if step.Task != nil {
			if c.Lookup == nil || c.Open == nil {
				return Plan{}, fmt.Errorf("task catalog unavailable")
			}
			t := *step.Task
			v, err := c.Lookup(ctx, t.Name, t.Version)
			if err != nil {
				return Plan{}, err
			}
			if c.EndpointID != "" && !slices.Contains(v.Metadata.AgentEndpointIDs, c.EndpointID) {
				return Plan{}, state.ErrExecutionTaskUnavailable
			}
			if v.Status != "active" {
				return Plan{}, state.ErrExecutionTaskUnavailable
			}
			task, err := c.Open(ctx, v)
			if err != nil {
				return Plan{}, err
			}
			if task.Name != t.Name || task.Version != t.Version {
				return Plan{}, fmt.Errorf("task identity mismatch")
			}
			p.Task = &api.ExecutionTaskRef{ID: v.ID, Name: v.Name, Version: v.Version, Digest: v.Digest}
			step.Request = task.Definition
			step.Request.CustomerRef = t.CustomerRef
			step.Request.Input = t.Input
			step.Request.InputFiles = t.InputFiles
			step.Task = nil
		}
		if len(step.ArtifactInputs) > 0 && step.Request.Source != "" {
			Bundle(&step.Request)
		}
		policy.Steps = append(policy.Steps, p)
	}
	if err := api.ValidateCreateManagedExecutionWorkflow(r); err != nil {
		return Plan{}, api.ErrValidation(err.Error())
	}
	return Plan{Request: r, Policy: policy}, nil
}

// Bundle preserves inline code byte-for-byte while making room for DAG files.
func Bundle(r *api.CreateExecutionRequest) {
	name := "main.mjs"
	if r.Runtime == api.ExecutionRuntimePython312 || r.Runtime == api.ExecutionRuntimePython313 {
		name = "main.py"
	}
	r.Entrypoint = name
	r.Files = append(r.Files, api.ExecutionFile{Path: name, Content: []byte(r.Source)})
	r.Source = ""
}
func Label(planID string, step, attempt int) string {
	return "gwf:p2:" + planID + ":p:" + strconv.Itoa(step) + ":a:" + strconv.Itoa(attempt)
}
func Retryable(r state.Execution) bool { return state.ExecutionPipelineRetryable(r) }
func RetryAt(r state.Execution, p api.ExecutionRetryPolicy, attempt int) time.Time {
	return state.ExecutionPipelineRetryAt(r, p, attempt)
}
