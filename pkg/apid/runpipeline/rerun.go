package runpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type RerunPlan struct {
	RunSteps    []string `json:"run_steps"`
	ReuseSteps  []string `json:"reuse_steps"`
	RepeatSteps []string `json:"repeat_steps"`
}

// PreviewRerun describes the branches before approval. Admission rechecks
// version pins, output availability and the exact acknowledgements.
func PreviewRerun(parent state.ExecutionWorkflowJob, p state.ExecutionPipeline, original api.CreateManagedExecutionWorkflowRequest) (RerunPlan, error) {
	normalized, err := matchRerunManifest(parent, p, original)
	if err != nil {
		return RerunPlan{}, err
	}
	return planRerun(normalized, p.Completion)
}

func rerunConflict(detail string) *api.Problem {
	return api.NewProblem(409, api.CodeConflict, "Pipeline rerun unavailable", detail)
}

func matchRerunManifest(parent state.ExecutionWorkflowJob, p state.ExecutionPipeline, original api.CreateManagedExecutionWorkflowRequest) (api.CreateManagedExecutionWorkflowRequest, error) {
	if p.Policy.Callback != nil {
		return original, rerunConflict("Agent callback pipelines require a new delegated tool invocation")
	}
	if parent.Status != api.ManagedExecutionWorkflowFailed || p.Completion == nil {
		return original, rerunConflict("only terminal unsuccessful pipelines can be rerun")
	}
	original.WorkflowID = parent.WorkflowID
	normalized, err := Normalize(original)
	if err != nil {
		return original, api.ErrValidation(err.Error())
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return original, err
	}
	expected := p.Policy.ManifestDigest
	if expected == "" {
		expected = p.Policy.SubmissionDigest
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expected {
		return original, rerunConflict("original manifest does not match the submitted pipeline")
	}
	return normalized, nil
}

func planRerun(original api.CreateManagedExecutionWorkflowRequest, completion *api.ExecutionPipelineCompletion) (RerunPlan, error) {
	dirty := map[string]bool{}
	for _, step := range completion.Steps {
		dirty[step.Label] = step.Outcome != "succeeded"
	}
	deps, err := api.ManagedExecutionWorkflowStepDependencies(original)
	if err != nil {
		return RerunPlan{}, err
	}
	for pass := 0; pass < len(original.Steps); pass++ {
		for label, parents := range deps {
			for _, dep := range parents {
				if dirty[dep] {
					dirty[label] = true
				}
			}
		}
	}
	plan := RerunPlan{RunSteps: []string{}, ReuseSteps: []string{}, RepeatSteps: []string{}}
	for _, step := range completion.Steps {
		if !dirty[step.Label] {
			plan.ReuseSteps = append(plan.ReuseSteps, step.Label)
			continue
		}
		plan.RunSteps = append(plan.RunSteps, step.Label)
		if len(step.Attempts) > 0 || step.ReusedRunID != "" {
			plan.RepeatSteps = append(plan.RepeatSteps, step.Label)
		}
	}
	slices.Sort(plan.RepeatSteps)
	return plan, nil
}

// Rerun leaves the original immutable. No original source/input retention is added.
func (s Submitter) RerunPipeline(ctx context.Context, parent state.ExecutionWorkflowJob, request api.RerunExecutionPipelineRequest, principal *string, wide, controlWide bool, runs state.ExecutionStore) (state.ExecutionWorkflowJob, bool, error) {
	if s.Jobs == nil || s.Receipts == nil || runs == nil {
		return state.ExecutionWorkflowJob{}, false, api.ErrCapacity("pipeline recovery storage unavailable")
	}
	conflict := func(detail string) (state.ExecutionWorkflowJob, bool, error) {
		return state.ExecutionWorkflowJob{}, false, rerunConflict(detail)
	}
	if principal == nil || parent.RunsPrincipalID == nil || *principal != *parent.RunsPrincipalID {
		return conflict("reruns require the original key family")
	}
	p, e := s.Jobs.ExecutionPipelineByID(ctx, parent.ID)
	if e != nil {
		return state.ExecutionWorkflowJob{}, false, e
	}
	normalized, e := matchRerunManifest(parent, p, request.Original)
	if e != nil {
		return state.ExecutionWorkflowJob{}, false, e
	}
	if request.WorkflowID == parent.WorkflowID || request.WorkflowID == "" || api.ValidateExecutionWorkflowMetadata(request.WorkflowID, "") != nil {
		return conflict("provide a distinct valid workflow_id")
	}
	if len(request.RepeatSteps) > len(p.Policy.Steps) {
		return conflict("repeat_steps exceeds pipeline step count")
	}
	repeat := append([]string(nil), request.RepeatSteps...)
	slices.Sort(repeat)
	for i, label := range repeat {
		if label == "" || i > 0 && repeat[i-1] == label {
			return conflict("repeat_steps must contain unique labels")
		}
	}
	raw, _ := json.Marshal(struct {
		Parent string
		Repeat []string
	}{parent.ID, repeat})
	intent := sha256.Sum256(raw)
	s.RerunIntent = hex.EncodeToString(intent[:])
	normalized.WorkflowID = request.WorkflowID
	// Exact child replay precedes catalog/output availability checks, even with writers off.
	if old, err := s.Receipts.ExecutionWorkflowJobByKey(ctx, parent.AccountID, request.WorkflowID, principal); err == nil {
		meta, err := s.Jobs.ExecutionPipelineByID(ctx, old.ID)
		if err != nil {
			return state.ExecutionWorkflowJob{}, false, err
		}
		s.Rerun = meta.Policy.Rerun
		if s.Rerun == nil || s.Rerun.ParentID != parent.ID {
			return conflict("workflow id is already in use")
		}
		return s.Submit(ctx, parent.AccountID, principal, wide, controlWide, normalized)
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.ExecutionWorkflowJob{}, false, err
	}
	if !s.Enabled {
		return state.ExecutionWorkflowJob{}, false, api.ErrCapacity("new Run pipelines are disabled")
	}
	compiled, e := s.Compiler.Compile(ctx, normalized, wide, controlWide)
	if e != nil {
		return state.ExecutionWorkflowJob{}, false, e
	}
	if len(compiled.Policy.Steps) != len(p.Policy.Steps) {
		return conflict("pipeline step identity differs")
	}
	for i, step := range compiled.Policy.Steps {
		if step.Label != p.Policy.Steps[i].Label || !reflect.DeepEqual(step.Task, p.Policy.Steps[i].Task) {
			return conflict("immutable task identity differs")
		}
	}
	rerun := &api.ExecutionPipelineRerun{ParentID: parent.ID, ParentWorkflowID: parent.WorkflowID, ParentPlanID: parent.PlanID, Reused: []api.ExecutionPipelineReusedStep{}}
	plan, e := planRerun(compiled.Request, p.Completion)
	if e != nil {
		return state.ExecutionWorkflowJob{}, false, e
	}
	worker := Worker{Runs: runs, Now: s.Now}
	for _, step := range p.Completion.Steps {
		if !slices.Contains(plan.ReuseSteps, step.Label) {
			continue
		}
		id := step.ReusedRunID
		if len(step.Attempts) > 0 {
			id = step.Attempts[len(step.Attempts)-1].RunID
		}
		if id == "" {
			return conflict("successful output receipt is missing")
		}
		r, e := runs.ExecutionByID(ctx, parent.AccountID, id)
		if e != nil {
			return conflict("successful output receipt is unavailable")
		}
		ref := api.ExecutionPipelineReusedStep{Label: step.Label, RunID: id, OutputSHA256: step.OutputSHA256}
		if outputDigest(r) != step.OutputSHA256 {
			return conflict("successful output fingerprint is unavailable or no longer matches")
		}
		if _, e = worker.reusedRun(ctx, parent, ref); e != nil {
			return conflict("successful output is unavailable, expired, revoked or outside the key family")
		}
		rerun.Reused = append(rerun.Reused, ref)
	}
	if !slices.Equal(repeat, plan.RepeatSteps) {
		return conflict("repeat_steps must explicitly acknowledge all previously started affected steps: " + joinLabels(plan.RepeatSteps))
	}
	s.Rerun = rerun
	return s.Submit(ctx, parent.AccountID, principal, wide, controlWide, normalized)
}
func joinLabels(labels []string) string { raw, _ := json.Marshal(labels); return string(raw) }
