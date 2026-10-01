package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	executionWorkflowPlanMaxBytes = 4 << 20
	executionWorkflowMaxSteps     = 16
	executionWorkflowPageSize     = 200
	executionWorkflowMaxPages     = 50
	executionWorkflowStepPrefix   = "gwf:"
)

// executionWorkflowPlan is a client-resubmitted recipe. Durable progress and
// outputs live in the ordinary Runs receipts; the guest filesystem stays
// disposable and the plan itself is not stored by Gregale.
type executionWorkflowPlan struct {
	WorkflowID string                  `json:"workflow_id"`
	Version    string                  `json:"version"`
	Steps      []executionWorkflowStep `json:"steps"`
}

type executionWorkflowStep struct {
	Label                   string                           `json:"label"`
	Request                 api.CreateExecutionRequest       `json:"request"`
	InputFromPreviousResult bool                             `json:"input_from_previous_result,omitempty"`
	ArtifactInputs          []executionWorkflowArtifactInput `json:"artifact_inputs,omitempty"`
}

type executionWorkflowArtifactInput struct {
	FromStep string `json:"from_step"`
	Name     string `json:"name"`
	Path     string `json:"path"`
}

type executionWorkflowStepResult struct {
	Label string                `json:"label"`
	Run   api.ExecutionResponse `json:"run"`
}

type executionWorkflowRunResult struct {
	WorkflowID string                        `json:"workflow_id"`
	PlanID     string                        `json:"plan_id"`
	Complete   bool                          `json:"complete"`
	Steps      []executionWorkflowStepResult `json:"steps"`
}

type executionWorkflowClient interface {
	GetExecutionCapabilities(context.Context) (api.ExecutionCapabilitiesResponse, error)
	ListExecutionsForWorkflow(context.Context, string, int, int, api.ExecutionStatus) (api.ExecutionListResponse, error)
	CreateExecution(context.Context, api.CreateExecutionRequest) (api.ExecutionResponse, error)
	GetExecution(context.Context, string) (api.ExecutionResponse, error)
}

type executionWorkflowStepFailure struct {
	Label string
	Run   api.ExecutionResponse
}

func (e *executionWorkflowStepFailure) Error() string {
	return fmt.Sprintf("workflow step %q ended with Run status %s", e.Label, e.Run.Status)
}

func prepareExecutionWorkflowRequest(plan executionWorkflowPlan, storageLabel string, step executionWorkflowStep) api.CreateExecutionRequest {
	request := step.Request
	request.WorkflowID = plan.WorkflowID
	request.StepLabel = storageLabel
	if request.Network == nil {
		request.Network = &api.ExecutionNetworkPolicy{Mode: api.ExecutionNetworkNone}
	} else {
		network := *request.Network
		if network.Mode == "" {
			network.Mode = api.ExecutionNetworkNone
		}
		request.Network = &network
	}
	if len(step.ArtifactInputs)+len(request.ArtifactInputs) != 0 && strings.TrimSpace(request.Source) != "" &&
		len(request.Files) == 0 && strings.TrimSpace(request.Entrypoint) == "" {
		entrypoint := "main.mjs"
		if strings.HasPrefix(string(request.Runtime), "python") {
			entrypoint = "main.py"
		}
		request.Files = []api.ExecutionFile{{Path: entrypoint, Content: []byte(request.Source)}}
		request.Entrypoint = entrypoint
		request.Source = ""
	}
	return request
}

func preflightExecutionWorkflowSteps(plan executionWorkflowPlan, storageLabels map[string]string, steps []executionWorkflowStep, capabilities api.ExecutionCapabilitiesResponse) error {
	if !capabilities.AdmissionAvailable {
		reasons := strings.Join(capabilities.UnavailableReasons, ", ")
		if reasons == "" {
			reasons = "admission is unavailable"
		}
		return fmt.Errorf("workflow preflight: Runs admission is unavailable (%s)", reasons)
	}
	if capabilities.Limits == nil {
		return fmt.Errorf("workflow preflight: Runs capabilities did not include request limits")
	}
	limits := capabilities.Limits
	if limits.MaxConcurrentRuns <= 0 || limits.MaxSourceBytes <= 0 || limits.MaxInputBytes <= 0 ||
		limits.MaxOutputBytes < limits.DefaultOutputBytes || limits.MaxTimeoutMS < limits.DefaultTimeoutMS ||
		limits.MaxMemoryMB < limits.DefaultMemoryMB || limits.MaxCPUMillicores < limits.DefaultCPUMillicores ||
		limits.MaxEphemeralDiskMB < limits.DefaultEphemeralDiskMB || limits.MaxBundleFiles <= 0 ||
		limits.MaxArtifactInputs <= 0 || limits.MaxOutputFiles <= 0 || limits.MaxArtifactPathBytes <= 0 {
		return fmt.Errorf("workflow preflight: Runs capabilities contained invalid request limits")
	}
	planTier := api.Plan(capabilities.Plan)
	if _, ok := planTier.ExecutionLimits(); !ok {
		return fmt.Errorf("workflow preflight: unsupported Runs plan %q", capabilities.Plan)
	}
	for _, step := range steps {
		request := prepareExecutionWorkflowRequest(plan, storageLabels[step.Label], step)
		if !workflowCapabilitiesContainRuntime(capabilities.Runtimes, request.Runtime) {
			return fmt.Errorf("workflow preflight: step %q runtime %q is unavailable", step.Label, request.Runtime)
		}
		if !workflowCapabilitiesContainProfile(capabilities.Profiles, request.Profile.Normalized(), request.Runtime) {
			return fmt.Errorf("workflow preflight: step %q profile %q is unavailable for runtime %q", step.Label, request.Profile.Normalized(), request.Runtime)
		}
		if request.Network == nil || !workflowCapabilitiesContainNetwork(capabilities.NetworkModes, request.Network.Mode) {
			return fmt.Errorf("workflow preflight: step %q network mode is unavailable", step.Label)
		}
		artifactInputCount := len(step.ArtifactInputs) + len(request.ArtifactInputs)
		if artifactInputCount > limits.MaxArtifactInputs {
			return fmt.Errorf("workflow preflight: step %q has %d artifact inputs; account limit is %d", step.Label, artifactInputCount, limits.MaxArtifactInputs)
		}
		if len(request.Files)+artifactInputCount > limits.MaxBundleFiles {
			return fmt.Errorf("workflow preflight: step %q would contain %d bundle files; account limit is %d", step.Label, len(request.Files)+artifactInputCount, limits.MaxBundleFiles)
		}
		for _, input := range append(append([]api.ExecutionArtifactInput(nil), request.ArtifactInputs...), workflowArtifactInputs(step.ArtifactInputs)...) {
			if api.ValidateExecutionOutputFiles([]string{input.Path}) != nil || len(input.Path) > limits.MaxArtifactPathBytes {
				return fmt.Errorf("workflow preflight: step %q has an invalid artifact destination path", step.Label)
			}
		}
		for _, input := range request.ArtifactInputs {
			if input.GrantToken != "" {
				if input.ExecutionID != "" || input.Name != "" {
					return fmt.Errorf("workflow preflight: step %q combines a grant token with an execution artifact reference", step.Label)
				}
				continue
			}
			if _, err := uuid.Parse(input.ExecutionID); err != nil || strings.TrimSpace(input.Name) == "" || api.ValidateExecutionOutputFiles([]string{input.Name}) != nil {
				return fmt.Errorf("workflow preflight: step %q has an invalid execution artifact reference", step.Label)
			}
		}
		if artifactInputCount > 0 && (strings.TrimSpace(request.Source) != "" || strings.TrimSpace(request.Entrypoint) == "") {
			return fmt.Errorf("workflow preflight: step %q artifact inputs require a files bundle with an entrypoint", step.Label)
		}

		request.ArtifactInputs = nil // Source artifact bytes resolve only after their producer succeeds.
		if step.InputFromPreviousResult {
			request.Input = json.RawMessage("null") // The eventual result size is checked when available.
		}
		resolved, problem := request.Resolve(planTier)
		if problem != nil {
			return fmt.Errorf("workflow preflight: step %q: %s", step.Label, problem.Detail)
		}
		if resolved.SourceBytes() > limits.MaxSourceBytes {
			return fmt.Errorf("workflow preflight: step %q source is %d bytes; account limit is %d", step.Label, resolved.SourceBytes(), limits.MaxSourceBytes)
		}
		if !step.InputFromPreviousResult && len(resolved.Input) > limits.MaxInputBytes {
			return fmt.Errorf("workflow preflight: step %q input is %d bytes; account limit is %d", step.Label, len(resolved.Input), limits.MaxInputBytes)
		}
		if len(request.OutputFiles) > limits.MaxOutputFiles {
			return fmt.Errorf("workflow preflight: step %q declares %d output files; account limit is %d", step.Label, len(request.OutputFiles), limits.MaxOutputFiles)
		}
		for _, path := range request.OutputFiles {
			if len(path) > limits.MaxArtifactPathBytes {
				return fmt.Errorf("workflow preflight: step %q output path exceeds the %d-byte account limit", step.Label, limits.MaxArtifactPathBytes)
			}
		}
		if err := validateWorkflowResolvedLimits(resolved.Limits, limits); err != nil {
			return fmt.Errorf("workflow preflight: step %q: %w", step.Label, err)
		}
	}
	return nil
}

func workflowArtifactInputs(inputs []executionWorkflowArtifactInput) []api.ExecutionArtifactInput {
	converted := make([]api.ExecutionArtifactInput, len(inputs))
	for i, input := range inputs {
		converted[i] = api.ExecutionArtifactInput{Name: input.Name, Path: input.Path}
	}
	return converted
}

func workflowCapabilitiesContainRuntime(runtimes []api.ExecutionRuntime, requested api.ExecutionRuntime) bool {
	for _, runtime := range runtimes {
		if runtime == requested {
			return true
		}
	}
	return false
}

func workflowCapabilitiesContainProfile(profiles []api.ExecutionProfileCapability, requested api.ExecutionProfile, runtime api.ExecutionRuntime) bool {
	for _, profile := range profiles {
		if profile.Profile.Normalized() == requested && workflowCapabilitiesContainRuntime(profile.Runtimes, runtime) {
			return true
		}
	}
	return false
}

func workflowCapabilitiesContainNetwork(modes []api.ExecutionNetworkMode, requested api.ExecutionNetworkMode) bool {
	for _, mode := range modes {
		if mode == requested {
			return true
		}
	}
	return false
}

func validateWorkflowResolvedLimits(resolved api.ResolvedExecutionLimits, limits *api.ExecutionCapabilityLimits) error {
	checks := []struct {
		name string
		got  int
		max  int
	}{
		{name: "timeout_ms", got: resolved.TimeoutMS, max: limits.MaxTimeoutMS},
		{name: "memory_mb", got: resolved.MemoryMB, max: limits.MaxMemoryMB},
		{name: "cpu_millicores", got: resolved.CPUMillicores, max: limits.MaxCPUMillicores},
		{name: "ephemeral_disk_mb", got: resolved.EphemeralDiskMB, max: limits.MaxEphemeralDiskMB},
		{name: "max_output_bytes", got: resolved.MaxOutputBytes, max: limits.MaxOutputBytes},
	}
	for _, check := range checks {
		if check.got > check.max {
			return fmt.Errorf("%s is %d; account limit is %d", check.name, check.got, check.max)
		}
	}
	return nil
}

func executionWorkflowPlanID(plan executionWorkflowPlan) (string, map[string]string, error) {
	if err := api.ValidateExecutionWorkflowMetadata(plan.WorkflowID, ""); err != nil || plan.WorkflowID == "" {
		return "", nil, fmt.Errorf("workflow_id is invalid")
	}
	if len(plan.Version) > 24 || api.ValidateExecutionWorkflowMetadata(plan.Version, "") != nil || plan.Version == "" {
		return "", nil, fmt.Errorf("version must be a short identifier of at most 24 characters")
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > executionWorkflowMaxSteps {
		return "", nil, fmt.Errorf("workflow must contain between 1 and %d steps", executionWorkflowMaxSteps)
	}
	labels := make(map[string]string, len(plan.Steps))
	for _, step := range plan.Steps {
		if strings.TrimSpace(step.Label) != step.Label || step.Label == "" || len(step.Label) > 96 {
			return "", nil, fmt.Errorf("step labels must be trimmed text between 1 and 96 bytes")
		}
		if _, ok := labels[step.Label]; ok {
			return "", nil, fmt.Errorf("step label %q is repeated", step.Label)
		}
		labels[step.Label] = ""
	}
	for i, step := range plan.Steps {
		if step.Request.WorkflowID != "" || step.Request.StepLabel != "" {
			return "", nil, fmt.Errorf("step %q must leave workflow_id and step_label to the runner", step.Label)
		}
		if len(step.Request.ArtifactInputs)+len(step.ArtifactInputs) > api.ExecutionArtifactInputMaxFiles {
			return "", nil, fmt.Errorf("step %q has more than %d artifact inputs", step.Label, api.ExecutionArtifactInputMaxFiles)
		}
		if step.Request.Network != nil && step.Request.Network.Mode != "" && step.Request.Network.Mode != api.ExecutionNetworkNone {
			return "", nil, fmt.Errorf("step %q requests an unsupported network mode", step.Label)
		}
		if step.InputFromPreviousResult && i == 0 {
			return "", nil, fmt.Errorf("step %q cannot use a previous result because it is first", step.Label)
		}
		if step.InputFromPreviousResult && len(step.Request.Input) != 0 {
			return "", nil, fmt.Errorf("step %q cannot set input and input_from_previous_result", step.Label)
		}
		for _, input := range step.ArtifactInputs {
			prior := -1
			for j := 0; j < i; j++ {
				if plan.Steps[j].Label == input.FromStep {
					prior = j
					break
				}
			}
			if prior < 0 || strings.TrimSpace(input.Name) != input.Name || input.Name == "" ||
				strings.TrimSpace(input.Path) != input.Path || input.Path == "" {
				return "", nil, fmt.Errorf("step %q has an invalid artifact input reference", step.Label)
			}
			declared := false
			for _, output := range plan.Steps[prior].Request.OutputFiles {
				if output == input.Name {
					declared = true
					break
				}
			}
			if !declared {
				return "", nil, fmt.Errorf("step %q references artifact %q not declared by step %q", step.Label, input.Name, input.FromStep)
			}
		}
	}
	canonicalPlan, err := json.Marshal(plan)
	if err != nil {
		return "", nil, fmt.Errorf("encode workflow plan identity: %w", err)
	}
	planDigest := sha256.Sum256(canonicalPlan)
	// Use 96 bits of the digest in receipt labels. The full manifest, including
	// its version and every step input, contributes to this identity; a version
	// string alone is not a safe resume key when later steps can be edited.
	planID := hex.EncodeToString(planDigest[:12])
	for _, step := range plan.Steps {
		storageLabel := executionWorkflowStepPrefix + planID + ":" + step.Label
		if err := api.ValidateExecutionWorkflowMetadata(plan.WorkflowID, storageLabel); err != nil {
			return "", nil, fmt.Errorf("step label %q is invalid: %w", step.Label, err)
		}
		labels[step.Label] = storageLabel
	}
	return planID, labels, nil
}

func loadExecutionWorkflowPlan(path string) (executionWorkflowPlan, error) {
	var plan executionWorkflowPlan
	file, err := openWorkflowPlanFile(path)
	if err != nil {
		return plan, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, executionWorkflowPlanMaxBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return plan, fmt.Errorf("decode workflow manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return plan, fmt.Errorf("workflow manifest contains more than one JSON value")
		}
		return plan, fmt.Errorf("workflow manifest has trailing data: %w", err)
	}
	return plan, nil
}

func openWorkflowPlanFile(path string) (*os.File, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("workflow manifest must be a regular file")
	}
	if info.Size() > executionWorkflowPlanMaxBytes {
		_ = file.Close()
		return nil, fmt.Errorf("workflow manifest exceeds %d bytes", executionWorkflowPlanMaxBytes)
	}
	return file, nil
}

func runExecutionWorkflow(ctx context.Context, client executionWorkflowClient, plan executionWorkflowPlan, pollInterval time.Duration) (executionWorkflowRunResult, error) {
	var out executionWorkflowRunResult
	if client == nil {
		return out, fmt.Errorf("workflow client is unavailable")
	}
	if pollInterval <= 0 {
		return out, fmt.Errorf("workflow poll interval must be positive")
	}
	planID, storageLabels, err := executionWorkflowPlanID(plan)
	if err != nil {
		return out, err
	}
	out = executionWorkflowRunResult{WorkflowID: plan.WorkflowID, PlanID: planID, Steps: make([]executionWorkflowStepResult, 0, len(plan.Steps))}
	existing, err := listExecutionWorkflowRuns(ctx, client, plan.WorkflowID)
	if err != nil {
		return out, err
	}
	byLabel := make(map[string]api.ExecutionResponse)
	for _, run := range existing {
		if !strings.HasPrefix(run.StepLabel, executionWorkflowStepPrefix) {
			continue
		}
		if !strings.HasPrefix(run.StepLabel, executionWorkflowStepPrefix+planID+":") {
			return out, fmt.Errorf("workflow %q already has a different workflow plan; use a new workflow_id", plan.WorkflowID)
		}
		if _, duplicate := byLabel[run.StepLabel]; duplicate {
			return out, fmt.Errorf("workflow %q has duplicate receipts for step %q", plan.WorkflowID, run.StepLabel)
		}
		byLabel[run.StepLabel] = run
	}
	var workflowLimits *api.ExecutionCapabilityLimits
	pendingSteps := make([]executionWorkflowStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		run, exists := byLabel[storageLabels[step.Label]]
		if exists && run.Status.Terminal() && run.Status != api.ExecutionStatusSucceeded {
			break
		}
		if !exists {
			pendingSteps = append(pendingSteps, step)
		}
	}
	if len(pendingSteps) != 0 {
		capabilities, capErr := client.GetExecutionCapabilities(ctx)
		if capErr != nil {
			return out, fmt.Errorf("workflow preflight: get Runs capabilities: %w", capErr)
		}
		if err := preflightExecutionWorkflowSteps(plan, storageLabels, pendingSteps, capabilities); err != nil {
			return out, err
		}
		workflowLimits = capabilities.Limits
	}
	completedByName := make(map[string]api.ExecutionResponse, len(plan.Steps))
	for i, step := range plan.Steps {
		storageLabel := storageLabels[step.Label]
		run, exists := byLabel[storageLabel]
		resultAppended := false
		if exists {
			if !run.Status.Terminal() {
				out.Steps = append(out.Steps, executionWorkflowStepResult{Label: step.Label, Run: run})
				resultAppended = true
				run, err = waitExecutionWorkflowRun(ctx, client, run, pollInterval)
				out.Steps[len(out.Steps)-1].Run = run
				if err != nil {
					return out, fmt.Errorf("wait for workflow step %q: %w", step.Label, err)
				}
			}
		} else {
			request := prepareExecutionWorkflowRequest(plan, storageLabel, step)
			if step.InputFromPreviousResult {
				previous := completedByName[plan.Steps[i-1].Label]
				if previous.Status != api.ExecutionStatusSucceeded || len(previous.Result) == 0 {
					return out, fmt.Errorf("step %q requires a JSON result from successful step %q", step.Label, plan.Steps[i-1].Label)
				}
				if workflowLimits != nil && len(previous.Result) > workflowLimits.MaxInputBytes {
					return out, fmt.Errorf("step %q previous result is %d bytes; account input limit is %d", step.Label, len(previous.Result), workflowLimits.MaxInputBytes)
				}
				request.Input = append(json.RawMessage(nil), previous.Result...)
			}
			artifactBytes := 0
			for _, artifactInput := range step.ArtifactInputs {
				previous, ok := completedByName[artifactInput.FromStep]
				if !ok || previous.Status != api.ExecutionStatusSucceeded {
					return out, fmt.Errorf("step %q requires a successful artifact-producing step %q", step.Label, artifactInput.FromStep)
				}
				artifactFound := false
				for _, artifact := range previous.Artifacts {
					if artifact.Name == artifactInput.Name {
						artifactFound = true
						break
					}
				}
				if !artifactFound {
					return out, fmt.Errorf("step %q requires artifact %q which step %q did not produce", step.Label, artifactInput.Name, artifactInput.FromStep)
				}
				for _, artifact := range previous.Artifacts {
					if artifact.Name == artifactInput.Name {
						artifactBytes += len(artifact.Content)
						break
					}
				}
				request.ArtifactInputs = append(request.ArtifactInputs, api.ExecutionArtifactInput{
					ExecutionID: previous.ID, Name: artifactInput.Name, Path: artifactInput.Path,
				})
			}
			if workflowLimits != nil {
				sourceBytes := artifactBytes
				for _, file := range request.Files {
					sourceBytes += len(file.Content)
				}
				if sourceBytes > workflowLimits.MaxSourceBytes {
					return out, fmt.Errorf("step %q source and selected artifacts total %d bytes; account limit is %d", step.Label, sourceBytes, workflowLimits.MaxSourceBytes)
				}
			}
			keyHash := sha256.Sum256([]byte(plan.WorkflowID + "\x00" + storageLabel))
			stepCtx := api.ContextWithIdempotencyKey(ctx, "runwf:"+hex.EncodeToString(keyHash[:]))
			run, err = client.CreateExecution(stepCtx, request)
			if err != nil {
				// A parallel resume may have won the unique step admission
				// race, or the admission response may have been lost after commit.
				// Reload receipts before deciding whether this step needs a retry.
				submitErr := err
				latest, listErr := listExecutionWorkflowRuns(ctx, client, plan.WorkflowID)
				if listErr != nil {
					return out, fmt.Errorf("submit workflow step %q: %v (checking for an existing receipt failed: %w)", step.Label, submitErr, listErr)
				}
				for _, candidate := range latest {
					if candidate.StepLabel == storageLabel {
						run, exists, err = candidate, true, nil
						break
					}
				}
				if err != nil {
					return out, fmt.Errorf("submit workflow step %q: %w", step.Label, submitErr)
				}
			}
			if !exists {
				out.Steps = append(out.Steps, executionWorkflowStepResult{Label: step.Label, Run: run})
				resultAppended = true
			}
			if !run.Status.Terminal() {
				if resultAppended {
					run, err = waitExecutionWorkflowRun(ctx, client, run, pollInterval)
					out.Steps[len(out.Steps)-1].Run = run
				} else {
					out.Steps = append(out.Steps, executionWorkflowStepResult{Label: step.Label, Run: run})
					resultAppended = true
					run, err = waitExecutionWorkflowRun(ctx, client, run, pollInterval)
					out.Steps[len(out.Steps)-1].Run = run
				}
				if err != nil {
					return out, fmt.Errorf("wait for workflow step %q: %w", step.Label, err)
				}
			}
		}
		if !resultAppended {
			out.Steps = append(out.Steps, executionWorkflowStepResult{Label: step.Label, Run: run})
		} else {
			out.Steps[len(out.Steps)-1].Run = run
		}
		completedByName[step.Label] = run
		if run.Status != api.ExecutionStatusSucceeded {
			return out, &executionWorkflowStepFailure{Label: step.Label, Run: run}
		}
	}
	out.Complete = true
	return out, nil
}

func listExecutionWorkflowRuns(ctx context.Context, client executionWorkflowClient, workflowID string) ([]api.ExecutionResponse, error) {
	var runs []api.ExecutionResponse
	for page := 0; page < executionWorkflowMaxPages; page++ {
		offset := page * executionWorkflowPageSize
		response, err := client.ListExecutionsForWorkflow(ctx, workflowID, executionWorkflowPageSize, offset, "")
		if err != nil {
			return nil, fmt.Errorf("list workflow runs: %w", err)
		}
		runs = append(runs, response.Executions...)
		if response.NextOffset <= offset || len(response.Executions) == 0 {
			return runs, nil
		}
	}
	return nil, fmt.Errorf("workflow contains too many Run receipts to reconcile")
}

func waitExecutionWorkflowRun(ctx context.Context, client executionWorkflowClient, run api.ExecutionResponse, interval time.Duration) (api.ExecutionResponse, error) {
	for !run.Status.Terminal() {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return run, ctx.Err()
		case <-timer.C:
		}
		updated, err := client.GetExecution(ctx, run.ID)
		if err != nil {
			return run, err
		}
		run = updated
	}
	return run, nil
}
