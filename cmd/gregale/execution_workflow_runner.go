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
	"github.com/onebox-faas/faas/pkg/executionworkflow"
)

const (
	executionWorkflowPlanMaxBytes          = 4 << 20
	executionWorkflowMaxSteps              = 16
	executionWorkflowMaxParallel           = 8
	executionWorkflowResultSchemaMaxBytes  = api.ExecutionWorkflowResultSchemaMaxBytes
	executionWorkflowResultSchemasMaxBytes = api.ExecutionWorkflowResultSchemasMaxBytes
	executionWorkflowPageSize              = 200
	executionWorkflowMaxPages              = 50
	executionWorkflowStepPrefix            = "gwf:"
)

const (
	executionWorkflowFailurePolicyFailFast            = "fail_fast"
	executionWorkflowFailurePolicyContinueIndependent = "continue_independent"
)

// executionWorkflowPlan is a client-resubmitted recipe. Durable progress and
// outputs live in the ordinary Runs receipts; the guest filesystem stays
// disposable and the plan itself is not stored by Gregale.
type executionWorkflowPlan struct {
	WorkflowID       string                  `json:"workflow_id"`
	Version          string                  `json:"version"`
	MaxParallelSteps int                     `json:"max_parallel_steps,omitempty"`
	FailurePolicy    string                  `json:"failure_policy,omitempty"`
	Steps            []executionWorkflowStep `json:"steps"`
}

type executionWorkflowStep struct {
	Label                    string                           `json:"label"`
	DependsOn                []string                         `json:"depends_on,omitempty"`
	IncludeDependencyResults bool                             `json:"include_dependency_results,omitempty"`
	ResultSchema             json.RawMessage                  `json:"result_schema,omitempty"`
	Request                  api.CreateExecutionRequest       `json:"request"`
	InputFromPreviousResult  bool                             `json:"input_from_previous_result,omitempty"`
	ArtifactInputs           []executionWorkflowArtifactInput `json:"artifact_inputs,omitempty"`
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
	WorkflowID             string                                         `json:"workflow_id"`
	PlanID                 string                                         `json:"plan_id"`
	Complete               bool                                           `json:"complete"`
	BlockedSteps           []string                                       `json:"blocked_steps,omitempty"`
	ResultContractFailures []executionWorkflowResultContractFailureResult `json:"result_contract_failures,omitempty"`
	Steps                  []executionWorkflowStepResult                  `json:"steps"`
}

type executionWorkflowPreviewStep struct {
	Label           string               `json:"label"`
	DependsOn       []string             `json:"depends_on,omitempty"`
	State           string               `json:"state"`
	RunID           string               `json:"run_id,omitempty"`
	RunStatus       api.ExecutionStatus  `json:"run_status,omitempty"`
	Runtime         api.ExecutionRuntime `json:"runtime"`
	Profile         api.ExecutionProfile `json:"profile"`
	HasResultSchema bool                 `json:"has_result_schema"`
	Detail          string               `json:"detail,omitempty"`
}

type executionWorkflowPreviewResult struct {
	DryRun                    bool                           `json:"dry_run"`
	WorkflowID                string                         `json:"workflow_id"`
	PlanID                    string                         `json:"plan_id"`
	FailurePolicy             string                         `json:"failure_policy"`
	RequestedMaxParallelSteps int                            `json:"requested_max_parallel_steps"`
	EffectiveMaxParallelSteps int                            `json:"effective_max_parallel_steps"`
	AccountMaxConcurrentRuns  *int                           `json:"account_max_concurrent_runs,omitempty"`
	InProgressRuns            int                            `json:"in_progress_runs"`
	AvailableParallelSlots    int                            `json:"available_parallel_slots"`
	Steps                     []executionWorkflowPreviewStep `json:"steps"`
}

type executionWorkflowDependencyInput struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
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

type executionWorkflowResultContractFailure struct {
	Label  string
	Detail string
}

type executionWorkflowResultContractFailureResult struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

func (e *executionWorkflowStepFailure) Error() string {
	return fmt.Sprintf("workflow step %q ended with Run status %s", e.Label, e.Run.Status)
}

func (e *executionWorkflowResultContractFailure) Error() string {
	return fmt.Sprintf("workflow step %q result contract failed: %s", e.Label, e.Detail)
}

func compileExecutionWorkflowResultSchemas(plan executionWorkflowPlan) (map[string]*executionworkflow.Schema, error) {
	compiled := make(map[string]*executionworkflow.Schema)
	var totalBytes int
	for _, step := range plan.Steps {
		if len(step.ResultSchema) == 0 {
			continue
		}
		if len(step.ResultSchema) > executionWorkflowResultSchemaMaxBytes {
			return nil, fmt.Errorf("step %q result_schema exceeds %d bytes", step.Label, executionWorkflowResultSchemaMaxBytes)
		}
		totalBytes += len(step.ResultSchema)
		if totalBytes > executionWorkflowResultSchemasMaxBytes {
			return nil, fmt.Errorf("workflow result schemas exceed %d bytes in total", executionWorkflowResultSchemasMaxBytes)
		}
		schema, err := executionworkflow.CompileResultSchema(step.ResultSchema)
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", step.Label, err)
		}
		compiled[step.Label] = schema
	}
	return compiled, nil
}

func validateExecutionWorkflowResult(label string, result json.RawMessage, schema *executionworkflow.Schema) error {
	if detail := executionworkflow.ValidateResult(result, schema); detail != "" {
		return &executionWorkflowResultContractFailure{Label: label, Detail: detail}
	}
	return nil
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
		} else if step.IncludeDependencyResults {
			request.Input = json.RawMessage("{}") // Dependency result sizes are checked when available.
		}
		resolved, problem := request.Resolve(planTier)
		if problem != nil {
			return fmt.Errorf("workflow preflight: step %q: %s", step.Label, problem.Detail)
		}
		if resolved.SourceBytes() > limits.MaxSourceBytes {
			return fmt.Errorf("workflow preflight: step %q source is %d bytes; account limit is %d", step.Label, resolved.SourceBytes(), limits.MaxSourceBytes)
		}
		if !step.InputFromPreviousResult && !step.IncludeDependencyResults && len(resolved.Input) > limits.MaxInputBytes {
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
	if plan.MaxParallelSteps < 0 || plan.MaxParallelSteps > executionWorkflowMaxParallel {
		return "", nil, fmt.Errorf("max_parallel_steps must be 0 (default) or between 1 and %d", executionWorkflowMaxParallel)
	}
	if plan.FailurePolicy == "" {
		plan.FailurePolicy = executionWorkflowFailurePolicyFailFast
	}
	if plan.FailurePolicy != executionWorkflowFailurePolicyFailFast && plan.FailurePolicy != executionWorkflowFailurePolicyContinueIndependent {
		return "", nil, fmt.Errorf("failure_policy must be %q or %q", executionWorkflowFailurePolicyFailFast, executionWorkflowFailurePolicyContinueIndependent)
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
	for _, step := range plan.Steps {
		if step.Request.WorkflowID != "" || step.Request.StepLabel != "" {
			return "", nil, fmt.Errorf("step %q must leave workflow_id and step_label to the runner", step.Label)
		}
		if len(step.Request.ArtifactInputs)+len(step.ArtifactInputs) > api.ExecutionArtifactInputMaxFiles {
			return "", nil, fmt.Errorf("step %q has more than %d artifact inputs", step.Label, api.ExecutionArtifactInputMaxFiles)
		}
		if step.Request.Network != nil && step.Request.Network.Mode != "" && step.Request.Network.Mode != api.ExecutionNetworkNone {
			return "", nil, fmt.Errorf("step %q requests an unsupported network mode", step.Label)
		}
	}
	if _, err := executionWorkflowStepDependencies(plan); err != nil {
		return "", nil, err
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

// executionWorkflowStepDependencies returns direct prerequisites in stable
// order. A manifest is topologically ordered: each dependency must appear
// earlier, which keeps review and resume behavior predictable while allowing
// independent steps to fan out concurrently.
func executionWorkflowStepDependencies(plan executionWorkflowPlan) (map[string][]string, error) {
	indices := make(map[string]int, len(plan.Steps))
	for i, step := range plan.Steps {
		indices[step.Label] = i
	}
	dependencies := make(map[string][]string, len(plan.Steps))
	for i, step := range plan.Steps {
		deps := make([]string, 0, len(step.DependsOn)+len(step.ArtifactInputs)+1)
		seen := make(map[string]struct{}, cap(deps))
		addDependency := func(label string) error {
			prior, ok := indices[label]
			if !ok || prior >= i {
				return fmt.Errorf("step %q dependency %q must name an earlier step", step.Label, label)
			}
			if _, ok := seen[label]; !ok {
				seen[label] = struct{}{}
				deps = append(deps, label)
			}
			return nil
		}
		explicit := make(map[string]struct{}, len(step.DependsOn))
		for _, label := range step.DependsOn {
			if strings.TrimSpace(label) != label || label == "" {
				return nil, fmt.Errorf("step %q has an invalid dependency label", step.Label)
			}
			if _, duplicate := explicit[label]; duplicate {
				return nil, fmt.Errorf("step %q repeats dependency %q", step.Label, label)
			}
			explicit[label] = struct{}{}
			if err := addDependency(label); err != nil {
				return nil, err
			}
		}
		if step.InputFromPreviousResult {
			if i == 0 {
				return nil, fmt.Errorf("step %q cannot use a previous result because it is first", step.Label)
			}
			if len(step.Request.Input) != 0 {
				return nil, fmt.Errorf("step %q cannot set input and input_from_previous_result", step.Label)
			}
			if err := addDependency(plan.Steps[i-1].Label); err != nil {
				return nil, err
			}
		}
		if step.IncludeDependencyResults && len(step.Request.Input) != 0 {
			return nil, fmt.Errorf("step %q cannot set input and include_dependency_results", step.Label)
		}
		if step.IncludeDependencyResults && step.InputFromPreviousResult {
			return nil, fmt.Errorf("step %q cannot combine include_dependency_results with input_from_previous_result", step.Label)
		}
		for _, input := range step.ArtifactInputs {
			if strings.TrimSpace(input.Name) != input.Name || input.Name == "" ||
				strings.TrimSpace(input.Path) != input.Path || input.Path == "" {
				return nil, fmt.Errorf("step %q has an invalid artifact input reference", step.Label)
			}
			prior, ok := indices[input.FromStep]
			if !ok || prior >= i {
				return nil, fmt.Errorf("step %q artifact input must name an earlier step", step.Label)
			}
			declared := false
			for _, output := range plan.Steps[prior].Request.OutputFiles {
				if output == input.Name {
					declared = true
					break
				}
			}
			if !declared {
				return nil, fmt.Errorf("step %q references artifact %q not declared by step %q", step.Label, input.Name, input.FromStep)
			}
			if err := addDependency(input.FromStep); err != nil {
				return nil, err
			}
		}
		if step.IncludeDependencyResults && len(deps) == 0 {
			return nil, fmt.Errorf("step %q includes dependency results but has no dependencies", step.Label)
		}
		dependencies[step.Label] = deps
	}
	return dependencies, nil
}

func loadExecutionWorkflowPlan(path string) (plan executionWorkflowPlan, retErr error) {
	file, err := openWorkflowPlanFile(path)
	if err != nil {
		return plan, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close workflow manifest: %w", closeErr))
		}
	}()
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

type executionWorkflowStepState struct {
	run       api.ExecutionResponse
	hasRun    bool
	succeeded bool
	blocked   bool
	started   bool
	err       error
}

type executionWorkflowStepCompletion struct {
	index int
	run   api.ExecutionResponse
	err   error
}

func reconcileExecutionWorkflowStates(
	plan executionWorkflowPlan,
	planID string,
	storageLabels map[string]string,
	resultSchemas map[string]*executionworkflow.Schema,
	continueIndependent bool,
	existing []api.ExecutionResponse,
) ([]executionWorkflowStepState, bool, []executionWorkflowStep, error) {
	byLabel := make(map[string]api.ExecutionResponse)
	for _, run := range existing {
		if !strings.HasPrefix(run.StepLabel, executionWorkflowStepPrefix) {
			continue
		}
		if !strings.HasPrefix(run.StepLabel, executionWorkflowStepPrefix+planID+":") {
			return nil, false, nil, fmt.Errorf("workflow %q already has a different workflow plan; use a new workflow_id", plan.WorkflowID)
		}
		if _, duplicate := byLabel[run.StepLabel]; duplicate {
			return nil, false, nil, fmt.Errorf("workflow %q has duplicate receipts for step %q", plan.WorkflowID, run.StepLabel)
		}
		byLabel[run.StepLabel] = run
	}

	states := make([]executionWorkflowStepState, len(plan.Steps))
	stopAdmissions := false
	pendingSteps := make([]executionWorkflowStep, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		if run, ok := byLabel[storageLabels[step.Label]]; ok {
			states[i].run = run
			states[i].hasRun = true
			if run.Status == api.ExecutionStatusSucceeded {
				if contractErr := validateExecutionWorkflowResult(step.Label, run.Result, resultSchemas[step.Label]); contractErr != nil {
					states[i].err = contractErr
					if !continueIndependent {
						stopAdmissions = true
					}
				} else {
					states[i].succeeded = true
				}
			} else if run.Status.Terminal() {
				states[i].err = &executionWorkflowStepFailure{Label: step.Label, Run: run}
				if !continueIndependent {
					stopAdmissions = true
				}
			}
		} else {
			pendingSteps = append(pendingSteps, step)
		}
	}
	return states, stopAdmissions, pendingSteps, nil
}

func previewExecutionWorkflow(ctx context.Context, client executionWorkflowClient, plan executionWorkflowPlan) (executionWorkflowPreviewResult, error) {
	out := executionWorkflowPreviewResult{DryRun: true, WorkflowID: plan.WorkflowID}
	if client == nil {
		return out, fmt.Errorf("workflow client is unavailable")
	}
	if plan.FailurePolicy == "" {
		plan.FailurePolicy = executionWorkflowFailurePolicyFailFast
	}
	planID, storageLabels, err := executionWorkflowPlanID(plan)
	if err != nil {
		return out, err
	}
	resultSchemas, err := compileExecutionWorkflowResultSchemas(plan)
	if err != nil {
		return out, err
	}
	dependencies, err := executionWorkflowStepDependencies(plan)
	if err != nil {
		return out, err
	}
	existing, err := listExecutionWorkflowRuns(ctx, client, plan.WorkflowID)
	if err != nil {
		return out, err
	}
	continueIndependent := plan.FailurePolicy == executionWorkflowFailurePolicyContinueIndependent
	states, stopAdmissions, pendingSteps, err := reconcileExecutionWorkflowStates(plan, planID, storageLabels, resultSchemas, continueIndependent, existing)
	if err != nil {
		return out, err
	}

	requestedParallel := plan.MaxParallelSteps
	if requestedParallel == 0 {
		requestedParallel = 1
	}
	out.PlanID = planID
	out.FailurePolicy = plan.FailurePolicy
	out.RequestedMaxParallelSteps = requestedParallel
	out.EffectiveMaxParallelSteps = requestedParallel
	out.Steps = make([]executionWorkflowPreviewStep, len(plan.Steps))
	for i, step := range plan.Steps {
		request := prepareExecutionWorkflowRequest(plan, storageLabels[step.Label], step)
		previewStep := executionWorkflowPreviewStep{
			Label: step.Label, DependsOn: append([]string(nil), dependencies[step.Label]...),
			Runtime: request.Runtime, Profile: request.Profile.Normalized(), HasResultSchema: len(step.ResultSchema) != 0,
		}
		state := states[i]
		switch {
		case state.hasRun:
			previewStep.RunID = state.run.ID
			previewStep.RunStatus = state.run.Status
			if state.succeeded {
				previewStep.State = "succeeded"
			} else if state.run.Status.Terminal() {
				var contractFailure *executionWorkflowResultContractFailure
				if errors.As(state.err, &contractFailure) {
					previewStep.State = "contract_failed"
					previewStep.Detail = contractFailure.Detail
				} else {
					previewStep.State = "failed"
				}
			} else {
				previewStep.State = "in_progress"
			}
		default:
			if continueIndependent {
				allTerminal, hasUnsuccessful := true, false
				for _, dependency := range dependencies[step.Label] {
					_, terminal := executionWorkflowDependencyStatus(states[workflowStepIndex(plan, dependency)])
					if !terminal {
						allTerminal = false
						break
					}
					status, _ := executionWorkflowDependencyStatus(states[workflowStepIndex(plan, dependency)])
					if status != string(api.ExecutionStatusSucceeded) {
						hasUnsuccessful = true
					}
				}
				if allTerminal && hasUnsuccessful && !executionWorkflowCanConsumeDependencyStatuses(step, dependencies[step.Label], states, plan) {
					states[i].blocked = true
					previewStep.State = "blocked"
				}
			}
			if previewStep.State == "" && stopAdmissions {
				previewStep.State = "not_admitted_fail_fast"
				break
			}
			if previewStep.State == "" {
				allSucceeded := true
				allTerminal := true
				for _, dependency := range dependencies[step.Label] {
					status, terminal := executionWorkflowDependencyStatus(states[workflowStepIndex(plan, dependency)])
					if !terminal {
						allTerminal = false
						allSucceeded = false
					} else if status != string(api.ExecutionStatusSucceeded) {
						allSucceeded = false
					}
				}
				switch {
				case allSucceeded:
					previewStep.State = "ready"
				case continueIndependent && step.IncludeDependencyResults && allTerminal && executionWorkflowCanConsumeDependencyStatuses(step, dependencies[step.Label], states, plan):
					previewStep.State = "ready"
				case states[i].blocked:
					previewStep.State = "blocked"
				default:
					previewStep.State = "waiting_for_dependencies"
				}
			}
		}
		out.Steps[i] = previewStep
	}
	for _, state := range states {
		if state.hasRun && !state.run.Status.Terminal() {
			out.InProgressRuns++
		}
	}

	if len(pendingSteps) != 0 && !stopAdmissions {
		capabilities, capErr := client.GetExecutionCapabilities(ctx)
		if capErr != nil {
			return out, fmt.Errorf("workflow preflight: get Runs capabilities: %w", capErr)
		}
		if err := preflightExecutionWorkflowSteps(plan, storageLabels, pendingSteps, capabilities); err != nil {
			return out, err
		}
		maxConcurrent := capabilities.Limits.MaxConcurrentRuns
		out.AccountMaxConcurrentRuns = &maxConcurrent
		if out.EffectiveMaxParallelSteps > maxConcurrent {
			out.EffectiveMaxParallelSteps = maxConcurrent
		}
		out.AvailableParallelSlots = out.EffectiveMaxParallelSteps - out.InProgressRuns
		if out.AvailableParallelSlots < 0 {
			out.AvailableParallelSlots = 0
		}
	}
	return out, nil
}

func workflowStepIndex(plan executionWorkflowPlan, label string) int {
	for i, step := range plan.Steps {
		if step.Label == label {
			return i
		}
	}
	return -1
}

func executionWorkflowDependencyStatus(state executionWorkflowStepState) (string, bool) {
	if state.blocked {
		return "blocked", true
	}
	var contractFailure *executionWorkflowResultContractFailure
	if errors.As(state.err, &contractFailure) {
		return "contract_failed", true
	}
	if state.succeeded {
		return string(api.ExecutionStatusSucceeded), true
	}
	if state.hasRun {
		return string(state.run.Status), state.run.Status.Terminal()
	}
	return "", false
}

func executionWorkflowCanConsumeDependencyStatuses(step executionWorkflowStep, dependencies []string, states []executionWorkflowStepState, plan executionWorkflowPlan) bool {
	if !step.IncludeDependencyResults {
		return false
	}
	for _, artifactInput := range step.ArtifactInputs {
		status, _ := executionWorkflowDependencyStatus(states[workflowStepIndex(plan, artifactInput.FromStep)])
		if status != string(api.ExecutionStatusSucceeded) {
			return false
		}
	}
	return len(dependencies) != 0
}

func runExecutionWorkflow(ctx context.Context, client executionWorkflowClient, plan executionWorkflowPlan, pollInterval time.Duration) (executionWorkflowRunResult, error) {
	var out executionWorkflowRunResult
	if client == nil {
		return out, fmt.Errorf("workflow client is unavailable")
	}
	if pollInterval <= 0 {
		return out, fmt.Errorf("workflow poll interval must be positive")
	}
	if plan.FailurePolicy == "" {
		plan.FailurePolicy = executionWorkflowFailurePolicyFailFast
	}
	continueIndependent := plan.FailurePolicy == executionWorkflowFailurePolicyContinueIndependent
	planID, storageLabels, err := executionWorkflowPlanID(plan)
	if err != nil {
		return out, err
	}
	resultSchemas, err := compileExecutionWorkflowResultSchemas(plan)
	if err != nil {
		return out, err
	}
	dependencies, err := executionWorkflowStepDependencies(plan)
	if err != nil {
		return out, err
	}
	stepIndices := make(map[string]int, len(plan.Steps))
	for i, step := range plan.Steps {
		stepIndices[step.Label] = i
	}
	out = executionWorkflowRunResult{WorkflowID: plan.WorkflowID, PlanID: planID, Steps: make([]executionWorkflowStepResult, 0, len(plan.Steps))}
	existing, err := listExecutionWorkflowRuns(ctx, client, plan.WorkflowID)
	if err != nil {
		return out, err
	}
	states, stopAdmissions, pendingSteps, err := reconcileExecutionWorkflowStates(plan, planID, storageLabels, resultSchemas, continueIndependent, existing)
	if err != nil {
		return out, err
	}

	var workflowLimits *api.ExecutionCapabilityLimits
	parallelLimit := plan.MaxParallelSteps
	if parallelLimit == 0 {
		parallelLimit = 1
	}
	if len(pendingSteps) != 0 && !stopAdmissions {
		capabilities, capErr := client.GetExecutionCapabilities(ctx)
		if capErr != nil {
			appendExecutionWorkflowStateResults(&out, plan, states)
			return out, fmt.Errorf("workflow preflight: get Runs capabilities: %w", capErr)
		}
		if err := preflightExecutionWorkflowSteps(plan, storageLabels, pendingSteps, capabilities); err != nil {
			appendExecutionWorkflowStateResults(&out, plan, states)
			return out, err
		}
		workflowLimits = capabilities.Limits
		if parallelLimit > workflowLimits.MaxConcurrentRuns {
			parallelLimit = workflowLimits.MaxConcurrentRuns
		}
	}
	if parallelLimit < 1 {
		parallelLimit = 1
	}

	completed := make(chan executionWorkflowStepCompletion, len(plan.Steps))
	active := 0
	activeRunSlots := 0
	remaining := len(plan.Steps)
	for _, state := range states {
		if state.succeeded || (state.hasRun && state.run.Status.Terminal()) {
			remaining--
		}
	}

	completedByName := func() map[string]api.ExecutionResponse {
		results := make(map[string]api.ExecutionResponse, len(plan.Steps))
		for i, state := range states {
			if state.succeeded {
				results[plan.Steps[i].Label] = state.run
			}
		}
		return results
	}
	dependencyStatus := func(index int) string {
		state := states[index]
		if state.blocked {
			return "blocked"
		}
		var contractFailure *executionWorkflowResultContractFailure
		if errors.As(state.err, &contractFailure) {
			return "contract_failed"
		}
		if state.succeeded {
			return string(api.ExecutionStatusSucceeded)
		}
		if state.hasRun && state.run.Status.Terminal() {
			return string(state.run.Status)
		}
		return ""
	}
	dependencyInputsFor := func(dependencyLabels []string) map[string]executionWorkflowDependencyInput {
		inputs := make(map[string]executionWorkflowDependencyInput, len(dependencyLabels))
		for _, label := range dependencyLabels {
			depIndex := stepIndices[label]
			state := states[depIndex]
			input := executionWorkflowDependencyInput{Status: dependencyStatus(depIndex)}
			if state.succeeded && len(state.run.Result) > 0 && json.Valid(state.run.Result) {
				input.Result = append(json.RawMessage(nil), state.run.Result...)
			}
			inputs[label] = input
		}
		return inputs
	}
	blockStepsWithFailedDependencies := func() {
		if !continueIndependent {
			return
		}
		for i, step := range plan.Steps {
			if states[i].hasRun || states[i].started || states[i].blocked {
				continue
			}
			allTerminal := true
			hasUnsuccessfulDependency := false
			for _, dependency := range dependencies[step.Label] {
				status := dependencyStatus(stepIndices[dependency])
				if status == "" {
					allTerminal = false
					break
				}
				if status != string(api.ExecutionStatusSucceeded) {
					hasUnsuccessfulDependency = true
				}
			}
			if !allTerminal || !hasUnsuccessfulDependency {
				continue
			}
			canConsumeStatuses := step.IncludeDependencyResults
			for _, artifactInput := range step.ArtifactInputs {
				if dependencyStatus(stepIndices[artifactInput.FromStep]) != string(api.ExecutionStatusSucceeded) {
					canConsumeStatuses = false
					break
				}
			}
			if canConsumeStatuses {
				continue
			}
			states[i].blocked = true
			remaining--
		}
	}
	processCompletion := func(completion executionWorkflowStepCompletion) {
		active--
		activeRunSlots--
		state := &states[completion.index]
		state.run = completion.run
		if completion.run.ID != "" {
			state.hasRun = true
		}
		if completion.err != nil {
			state.err = completion.err
			var runFailure *executionWorkflowStepFailure
			var contractFailure *executionWorkflowResultContractFailure
			recoverableStepFailure := errors.As(completion.err, &runFailure) || errors.As(completion.err, &contractFailure)
			if !continueIndependent || !recoverableStepFailure {
				stopAdmissions = true
			}
		} else if completion.run.Status == api.ExecutionStatusSucceeded {
			state.succeeded = true
		} else {
			state.err = &executionWorkflowStepFailure{Label: plan.Steps[completion.index].Label, Run: completion.run}
			if !continueIndependent {
				stopAdmissions = true
			}
		}
		remaining--
	}

	for remaining > 0 || active > 0 {
		// Process completions already waiting before admitting more work. If a
		// sibling has failed, this prevents a buffered success from opening a
		// slot that would submit another step first.
		select {
		case completion := <-completed:
			processCompletion(completion)
			continue
		default:
		}
		blockStepsWithFailedDependencies()

		// Existing receipts were admitted by an earlier invocation. Always
		// watch them, including after a sibling fails, so the result retains
		// every known receipt and no run is abandoned locally.
		for i, state := range states {
			if !state.hasRun || state.run.Status.Terminal() || state.started {
				continue
			}
			states[i].started = true
			active++
			activeRunSlots++
			go func(index int, run api.ExecutionResponse) {
				updated, waitErr := waitExecutionWorkflowRun(ctx, client, run, pollInterval)
				if waitErr != nil {
					waitErr = fmt.Errorf("wait for workflow step %q: %w", plan.Steps[index].Label, waitErr)
				} else if updated.Status != api.ExecutionStatusSucceeded {
					waitErr = &executionWorkflowStepFailure{Label: plan.Steps[index].Label, Run: updated}
				}
				completed <- executionWorkflowStepCompletion{index: index, run: updated, err: waitErr}
			}(i, state.run)
		}

		if !stopAdmissions && activeRunSlots < parallelLimit {
			results := completedByName()
			for i, step := range plan.Steps {
				if activeRunSlots >= parallelLimit {
					break
				}
				if states[i].hasRun || states[i].started {
					continue
				}
				ready := true
				allDependenciesTerminal := true
				for _, dependency := range dependencies[step.Label] {
					depIndex, ok := stepIndices[dependency]
					if !ok {
						ready = false
						allDependenciesTerminal = false
						break
					}
					status := dependencyStatus(depIndex)
					if status == "" {
						ready = false
						allDependenciesTerminal = false
					} else if status != string(api.ExecutionStatusSucceeded) {
						ready = false
					}
				}
				if !ready && continueIndependent && step.IncludeDependencyResults && allDependenciesTerminal {
					ready = true
					for _, artifactInput := range step.ArtifactInputs {
						if dependencyStatus(stepIndices[artifactInput.FromStep]) != string(api.ExecutionStatusSucceeded) {
							ready = false
							break
						}
					}
				}
				if !ready {
					continue
				}
				states[i].started = true
				active++
				activeRunSlots++
				dependencyInputs := dependencyInputsFor(dependencies[step.Label])
				go func(index int, step executionWorkflowStep, results map[string]api.ExecutionResponse, dependencyInputs map[string]executionWorkflowDependencyInput) {
					run, runErr := runExecutionWorkflowStep(ctx, client, plan, storageLabels[step.Label], step, index, results, dependencyInputs, resultSchemas[step.Label], workflowLimits, pollInterval)
					completed <- executionWorkflowStepCompletion{index: index, run: run, err: runErr}
				}(i, step, results, dependencyInputs)
			}
		}

		if active == 0 {
			break
		}
		processCompletion(<-completed)
	}

	appendExecutionWorkflowStateResults(&out, plan, states)
	for _, state := range states {
		if state.err != nil && err == nil {
			err = state.err
		}
	}
	if err != nil {
		return out, err
	}
	if remaining != 0 {
		return out, fmt.Errorf("workflow stopped with %d step(s) not completed", remaining)
	}
	out.Complete = true
	return out, nil
}

func appendExecutionWorkflowStateResults(out *executionWorkflowRunResult, plan executionWorkflowPlan, states []executionWorkflowStepState) {
	for i, state := range states {
		if state.hasRun {
			out.Steps = append(out.Steps, executionWorkflowStepResult{Label: plan.Steps[i].Label, Run: state.run})
		}
		if state.blocked {
			out.BlockedSteps = append(out.BlockedSteps, plan.Steps[i].Label)
		}
		var contractFailure *executionWorkflowResultContractFailure
		if errors.As(state.err, &contractFailure) {
			out.ResultContractFailures = append(out.ResultContractFailures, executionWorkflowResultContractFailureResult{
				Label: contractFailure.Label, Detail: contractFailure.Detail,
			})
		}
	}
}

func runExecutionWorkflowStep(
	ctx context.Context,
	client executionWorkflowClient,
	plan executionWorkflowPlan,
	storageLabel string,
	step executionWorkflowStep,
	index int,
	completedByName map[string]api.ExecutionResponse,
	dependencyInputs map[string]executionWorkflowDependencyInput,
	resultSchema *executionworkflow.Schema,
	workflowLimits *api.ExecutionCapabilityLimits,
	pollInterval time.Duration,
) (api.ExecutionResponse, error) {
	var run api.ExecutionResponse
	request := prepareExecutionWorkflowRequest(plan, storageLabel, step)
	if step.InputFromPreviousResult {
		previous := completedByName[plan.Steps[index-1].Label]
		if previous.Status != api.ExecutionStatusSucceeded || len(previous.Result) == 0 || !json.Valid(previous.Result) {
			return run, fmt.Errorf("step %q requires a JSON result from successful step %q", step.Label, plan.Steps[index-1].Label)
		}
		if workflowLimits != nil && len(previous.Result) > workflowLimits.MaxInputBytes {
			return run, fmt.Errorf("step %q previous result is %d bytes; account input limit is %d", step.Label, len(previous.Result), workflowLimits.MaxInputBytes)
		}
		request.Input = append(json.RawMessage(nil), previous.Result...)
	} else if step.IncludeDependencyResults {
		input, err := json.Marshal(dependencyInputs)
		if err != nil {
			return run, fmt.Errorf("encode dependency results for step %q: %w", step.Label, err)
		}
		if workflowLimits != nil && len(input) > workflowLimits.MaxInputBytes {
			return run, fmt.Errorf("step %q dependency results are %d bytes; account input limit is %d", step.Label, len(input), workflowLimits.MaxInputBytes)
		}
		request.Input = input
	}

	artifactBytes := 0
	for _, artifactInput := range step.ArtifactInputs {
		previous, ok := completedByName[artifactInput.FromStep]
		if !ok || previous.Status != api.ExecutionStatusSucceeded {
			return run, fmt.Errorf("step %q requires a successful artifact-producing step %q", step.Label, artifactInput.FromStep)
		}
		artifactFound := false
		for _, artifact := range previous.Artifacts {
			if artifact.Name == artifactInput.Name {
				artifactFound = true
				artifactBytes += len(artifact.Content)
				break
			}
		}
		if !artifactFound {
			return run, fmt.Errorf("step %q requires artifact %q which step %q did not produce", step.Label, artifactInput.Name, artifactInput.FromStep)
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
			return run, fmt.Errorf("step %q source and selected artifacts total %d bytes; account limit is %d", step.Label, sourceBytes, workflowLimits.MaxSourceBytes)
		}
	}

	keyHash := sha256.Sum256([]byte(plan.WorkflowID + "\x00" + storageLabel))
	stepCtx := api.ContextWithIdempotencyKey(ctx, "runwf:"+hex.EncodeToString(keyHash[:]))
	created, submitErr := client.CreateExecution(stepCtx, request)
	if submitErr == nil {
		run = created
	} else {
		// Another copy of this plan may have won admission, or the response
		// may have been lost after the Run was committed. Adopt its receipt.
		latest, listErr := listExecutionWorkflowRuns(ctx, client, plan.WorkflowID)
		if listErr != nil {
			return run, fmt.Errorf("submit workflow step %q: %w (checking for an existing receipt failed: %w)", step.Label, submitErr, listErr)
		}
		found := false
		for _, candidate := range latest {
			if candidate.StepLabel == storageLabel {
				run, found = candidate, true
				break
			}
		}
		if !found {
			return run, fmt.Errorf("submit workflow step %q: %w", step.Label, submitErr)
		}
	}

	if !run.Status.Terminal() {
		updated, waitErr := waitExecutionWorkflowRun(ctx, client, run, pollInterval)
		run = updated
		if waitErr != nil {
			return run, fmt.Errorf("wait for workflow step %q: %w", step.Label, waitErr)
		}
	}
	if run.Status != api.ExecutionStatusSucceeded {
		return run, &executionWorkflowStepFailure{Label: step.Label, Run: run}
	}
	if err := validateExecutionWorkflowResult(step.Label, run.Result, resultSchema); err != nil {
		return run, err
	}
	return run, nil
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
