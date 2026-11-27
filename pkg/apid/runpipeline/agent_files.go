package runpipeline

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
)

func (s Submitter) pinAgentFiles(ctx context.Context, account string, plan *Plan) error {
	if len(s.AgentFileGrants) == 0 {
		return nil
	}
	if s.Callback == nil || s.Rerun != nil {
		return api.ErrValidation("scoped file grants require a new Agent pipeline call")
	}
	plan.AgentFiles = make(map[string][]runfiles.AgentFilePin, len(s.AgentFileGrants))
	count := 0
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	for i := range plan.Request.Steps {
		step := &plan.Request.Steps[i]
		tokens := s.AgentFileGrants[step.Label]
		if len(tokens) == 0 {
			continue
		}
		task := plan.Policy.Steps[i].Task
		count += len(tokens)
		if task == nil || len(step.Request.InputFiles) != 0 || count > api.AgentExecutionFileGrantMaxCount {
			return api.ErrValidation("scoped file grants require bounded inputs on a delegated task step")
		}
		scope := runfiles.AgentFileScope{AccountID: account, EndpointID: s.Callback.EndpointID,
			ThreadID: s.Callback.ThreadID, ToolCallID: s.Callback.ToolCallID, TaskName: task.Name,
			Version: task.Version, StepLabel: step.Label}
		paths := make(map[string]bool, len(tokens)+len(step.ArtifactInputs))
		for _, artifact := range step.ArtifactInputs {
			paths[artifact.Path] = true
		}
		for _, token := range tokens {
			pin, err := s.AgentFiles.Pin(ctx, s.FileReader, scope, token, now)
			if err != nil {
				return err
			}
			if paths[pin.Input.Path] {
				return api.ErrValidation("pipeline file grants must have distinct input paths")
			}
			paths[pin.Input.Path] = true
			step.Request.InputFiles = append(step.Request.InputFiles, pin.Input)
			plan.AgentFiles[step.Label] = append(plan.AgentFiles[step.Label], pin)
		}
	}
	if len(plan.AgentFiles) != len(s.AgentFileGrants) {
		return api.ErrValidation("every pipeline file grant must name a declared step")
	}
	plan.Policy.ScopedFileInputs = true
	return nil
}
