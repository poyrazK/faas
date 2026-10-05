package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrAutomationSimulationInvalid = errors.New("invalid automation simulation")

type AutomationSimulationLimitError struct {
	Resource        string
	Limit, Observed int64
}

func (e *AutomationSimulationLimitError) Error() string {
	return fmt.Sprintf("simulation %s exceeds the limit of %d", e.Resource, e.Limit)
}

func simulationLimit(resource string, limit, observed int64) error {
	if observed <= limit {
		return nil
	}
	return &AutomationSimulationLimitError{Resource: resource, Limit: limit, Observed: observed}
}

// SimulateAutomation has no store, executor, clock, or credential dependency.
// Supplied results authorize only hypothetical data flow, never execution.
func SimulateAutomation(ctx context.Context, request api.SimulateAutomationRequest, plan api.Plan) (api.SimulateAutomationResponse, error) {
	response := api.SimulateAutomationResponse{Issues: []string{}, Warnings: []string{}, StepOrder: []string{}, Trace: []api.AutomationSimulationStep{}}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	snapshot, err := validateSimulationRequest(request)
	if err != nil {
		return response, err
	}
	hash := sha256.Sum256(snapshot)
	response.DefinitionHash = hex.EncodeToString(hash[:])
	order, err := api.ValidateWorkflowDAG(request.Definition, plan)
	if err != nil {
		response.Issues = append(response.Issues, err.Error())
		return response, nil
	}
	response.DefinitionValid, response.StepOrder = true, order
	s := automationSimulator{request: request, snapshot: snapshot, response: response, specs: map[string]api.WorkflowStepSpec{}, steps: map[string]WorkflowStep{}, rows: map[string][]api.AutomationSimulationStep{}, done: map[string]bool{}, usedMocks: map[string]bool{}, usedItemMocks: map[string]bool{}}
	if len(s.request.Input) == 0 {
		s.request.Input = json.RawMessage("null")
	}
	for _, step := range request.Definition.Steps {
		s.specs[step.Name] = step
		s.steps[step.Name] = WorkflowStep{StepName: step.Name, Status: WorkflowStepStatusPending}
	}
	if err := s.validateMocks(); err != nil {
		return response, err
	}
	if err := s.walk(ctx); err != nil {
		return response, err
	}
	return s.finish()
}

func validateSimulationRequest(request api.SimulateAutomationRequest) ([]byte, error) {
	if err := simulationLimit("definition steps", api.AutomationSimulationMaxSteps, int64(len(request.Definition.Steps))); err != nil {
		return nil, err
	}
	if err := simulationJSON(request.Input, "input"); err != nil {
		return nil, err
	}
	for name, output := range request.MockOutputs {
		if err := simulationJSON(output, "mock output for "+name); err != nil {
			return nil, err
		}
		if len(output) == 0 {
			return nil, fmt.Errorf("%w: mock outputs must contain JSON values", ErrAutomationSimulationInvalid)
		}
	}
	for name, outputs := range request.MockItemOutputs {
		if err := simulationLimit("item mocks", api.WorkflowForEachMaxItems, int64(len(outputs))); err != nil {
			return nil, err
		}
		for _, output := range outputs {
			if err := simulationJSON(output, "item mock for "+name); err != nil {
				return nil, err
			}
			if len(output) == 0 {
				return nil, fmt.Errorf("%w: item mocks must contain JSON values", ErrAutomationSimulationInvalid)
			}
		}
	}
	snapshot, err := json.Marshal(request.Definition)
	if err != nil {
		return nil, fmt.Errorf("%w: definition must contain valid JSON", ErrAutomationSimulationInvalid)
	}
	if err := simulationLimit("definition bytes", api.AutomationDefinitionMaxBytes, int64(len(snapshot))); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request must contain valid JSON", ErrAutomationSimulationInvalid)
	}
	if err := simulationLimit("request bytes", api.AutomationSimulationRequestMaxBytes, int64(len(raw))); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func simulationJSON(raw json.RawMessage, resource string) error {
	if err := simulationLimit(resource+" bytes", api.WorkflowRunInputMaxBytes, int64(len(raw))); err != nil {
		return err
	}
	if len(raw) != 0 && !json.Valid(raw) {
		return fmt.Errorf("%w: %s must be valid JSON", ErrAutomationSimulationInvalid, resource)
	}
	return nil
}

type automationSimulator struct {
	request                        api.SimulateAutomationRequest
	snapshot                       json.RawMessage
	response                       api.SimulateAutomationResponse
	specs                          map[string]api.WorkflowStepSpec
	steps                          map[string]WorkflowStep
	rows                           map[string][]api.AutomationSimulationStep
	done, usedMocks, usedItemMocks map[string]bool
	traceCount                     int
	traceBytes                     int64
}

func (s *automationSimulator) validateMocks() error {
	for name := range s.request.MockOutputs {
		step, ok := s.specs[name]
		if !ok || step.ForEach != nil || step.Join != nil || simulationWait(step) {
			return fmt.Errorf("%w: mock_outputs must name action steps", ErrAutomationSimulationInvalid)
		}
		if step.OnTimeout != "" && simulationTimeoutMock(s.request.MockOutputs[name]) {
			return fmt.Errorf("%w: timeout outcome mocks are unsupported", ErrAutomationSimulationInvalid)
		}
	}
	for name := range s.request.MockItemOutputs {
		if step, ok := s.specs[name]; !ok || step.ForEach == nil {
			return fmt.Errorf("%w: mock_item_outputs must name for_each steps", ErrAutomationSimulationInvalid)
		}
	}
	return nil
}

func simulationTimeoutMock(output json.RawMessage) bool {
	// The runtime reserves exactly this object for timeout routing, including
	// on action steps. Do not misrepresent it as an ordinary successful mock.
	var value map[string]json.RawMessage
	if json.Unmarshal(output, &value) != nil || len(value) != 1 {
		return false
	}
	var timedOut bool
	return json.Unmarshal(value["timeout"], &timedOut) == nil && timedOut
}

func (s *automationSimulator) walk(ctx context.Context) error {
	// Exception routing creates ordering constraints beyond the ordinary DAG.
	// Revisit blocked roots only; action rows with missing mocks stay pending.
	for range len(s.specs) {
		progress := false
		for _, name := range s.response.StepOrder {
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.done[name] {
				continue
			}
			row := simulationRow(s.specs[name])
			ready, err := s.resolve(s.specs[name], &row)
			if err != nil {
				return err
			}
			if !ready {
				continue
			}
			if err := s.record(name, row); err != nil {
				return err
			}
			s.done[name], progress = true, true
		}
		if !progress {
			break
		}
	}
	for _, name := range s.response.StepOrder {
		if s.done[name] {
			continue
		}
		row := simulationRow(s.specs[name])
		row.State, row.Reason, row.BlockedBy = "blocked", "dependency_output_missing", s.pendingDependencies(s.specs[name])
		if sources := s.pendingExceptionSources(name); len(sources) > 0 {
			row.Reason, row.BlockedBy = "exception_outcome_missing", sources
		}
		if err := s.record(name, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *automationSimulator) resolve(spec api.WorkflowStepSpec, row *api.AutomationSimulationStep) (bool, error) {
	if spec.Join != nil {
		result, err := evaluateWorkflowJoinDefinition(s.specs, spec.Name, s.steps)
		if err != nil {
			s.evaluationError(row, "join_evaluation_failed")
			return true, nil //nolint:nilerr // Evaluation failures are reported in the hypothetical trace.
		}
		if !result.ready {
			return false, nil
		}
		if result.skipReason != nil {
			s.skip(row, *result.skipReason)
		} else {
			row.State, row.Output = "resolved", result.output
			s.succeed(row)
		}
		return true, nil
	}
	if s.exceptionSkipped(spec.Name) {
		s.skip(row, WorkflowSkipRouteNotTaken)
		return true, nil
	}
	if len(s.pendingExceptionSources(spec.Name)) > 0 {
		return false, nil
	}
	failed, skipped := false, false
	for _, name := range spec.DependsOn {
		step := s.steps[name]
		failed = failed || step.Status == WorkflowStepStatusDead || step.Status == WorkflowStepStatusFailed
		skipped = skipped || step.Status == WorkflowStepStatusSkipped
	}
	if skipped {
		s.skip(row, WorkflowSkipDependencySkipped)
		return true, nil
	}
	if failed {
		s.skip(row, WorkflowSkipDependencyFailed)
		return true, nil
	}
	if len(s.pendingDependencies(spec)) != 0 {
		return false, nil
	}
	if spec.When != nil {
		matched, err := evaluateWorkflowStepGuard(s.snapshot, s.request.Input, spec.Name, s.steps)
		if err != nil {
			s.evaluationError(row, "guard_evaluation_failed")
			return true, nil //nolint:nilerr // Evaluation failures are reported in the hypothetical trace.
		}
		row.WhenMatched = &matched
		step := s.steps[spec.Name]
		step.WhenMatched = &matched
		s.steps[spec.Name] = step
		if !matched {
			s.skip(row, WorkflowSkipWhenFalse)
			return true, nil
		}
	}
	if spec.ForEach != nil {
		return true, s.expand(spec, row)
	}
	input, err := api.ResolveWorkflowStepInputBounded(spec.Input, s.request.Input, s.outputs(spec), nil, api.WorkflowRunInputMaxBytes)
	if errors.Is(err, api.ErrWorkflowInputLimit) {
		return false, simulationLimit("resolved input bytes", api.WorkflowRunInputMaxBytes, api.WorkflowRunInputMaxBytes+1)
	}
	if err != nil {
		s.evaluationError(row, "input_resolution_failed")
		return true, nil //nolint:nilerr // Evaluation failures are reported in the hypothetical trace.
	}
	row.Input = input
	if simulationWait(spec) {
		row.State, row.Reason = "would_wait", "wait_outcome_missing"
		return true, nil
	}
	if output, ok := s.request.MockOutputs[spec.Name]; ok {
		row.State, row.Output, s.usedMocks[spec.Name] = "mocked", cloneWorkflowJSON(output), true
		s.succeed(row)
	} else {
		row.State, row.Reason = "would_execute", "mock_output_missing"
	}
	return true, nil
}

func (s *automationSimulator) pendingDependencies(spec api.WorkflowStepSpec) []string {
	var pending []string
	for _, name := range spec.DependsOn {
		if s.steps[name].Status == WorkflowStepStatusPending {
			pending = append(pending, name)
		}
	}
	sort.Strings(pending)
	return pending
}

func (s *automationSimulator) pendingExceptionSources(name string) []string {
	var sources []string
	for _, source := range s.request.Definition.Steps {
		if source.OnFailure != name && source.OnTimeout != name {
			continue
		}
		status := s.steps[source.Name].Status
		if status != WorkflowStepStatusSucceeded && status != WorkflowStepStatusSkipped {
			sources = append(sources, source.Name)
		}
	}
	sort.Strings(sources)
	return sources
}

func (s *automationSimulator) exceptionSkipped(name string) bool {
	// The scheduler skips a target if either exceptional route is ineligible,
	// even while the other route is unresolved. Mocks model only success.
	for _, failureRoute := range []bool{false, true} {
		referenced, pending := false, false
		for _, source := range s.request.Definition.Steps {
			target := source.OnTimeout
			if failureRoute {
				target = source.OnFailure
			}
			if target != name {
				continue
			}
			referenced = true
			status := s.steps[source.Name].Status
			pending = pending || (status != WorkflowStepStatusSucceeded && status != WorkflowStepStatusSkipped)
		}
		if referenced && !pending {
			return true
		}
	}
	return false
}

func (s *automationSimulator) outputs(spec api.WorkflowStepSpec) map[string]json.RawMessage {
	outputs := make(map[string]json.RawMessage, len(spec.DependsOn))
	for _, name := range spec.DependsOn {
		outputs[name] = s.steps[name].Output
	}
	return outputs
}

func (s *automationSimulator) skip(row *api.AutomationSimulationStep, reason string) {
	row.State, row.Reason = "skipped", reason
	step := s.steps[row.StepName]
	step.Status, step.SkipReason = WorkflowStepStatusSkipped, &reason
	s.steps[row.StepName] = step
}

func (s *automationSimulator) succeed(row *api.AutomationSimulationStep) {
	step := s.steps[row.StepName]
	step.Status, step.Output = WorkflowStepStatusSucceeded, row.Output
	s.steps[row.StepName] = step
}

func (s *automationSimulator) evaluationError(row *api.AutomationSimulationStep, reason string) {
	row.State, row.Reason = "error", reason
	step := s.steps[row.StepName]
	step.Status = WorkflowStepStatusDead
	s.steps[row.StepName] = step
	s.response.Issues = append(s.response.Issues, fmt.Sprintf("step %q: %s", row.StepName, reason))
}

func (s *automationSimulator) expand(spec api.WorkflowStepSpec, row *api.AutomationSimulationStep) error {
	items, inputs, err := api.ResolveWorkflowForEachInputsBounded(spec, s.request.Input, s.outputs(spec))
	if errors.Is(err, api.ErrWorkflowForEachItemLimit) {
		return simulationLimit("loop items", api.WorkflowForEachMaxItems, api.WorkflowForEachMaxItems+1)
	}
	if errors.Is(err, api.ErrWorkflowInputLimit) {
		return simulationLimit("loop input bytes", api.WorkflowForEachMaxInputBytes, api.WorkflowForEachMaxInputBytes+1)
	}
	if err != nil {
		s.evaluationError(row, "for_each_evaluation_failed")
		return nil //nolint:nilerr // Evaluation failures are reported in the hypothetical trace.
	}
	if err := simulationLimit("trace entries", api.AutomationSimulationMaxTraceEntries, int64(s.traceCount+len(inputs)+1)); err != nil {
		return err
	}
	mocks := s.request.MockItemOutputs[spec.Name]
	if len(mocks) > len(inputs) {
		return fmt.Errorf("%w: item mocks exceed the materialized loop length", ErrAutomationSimulationInvalid)
	}
	if _, ok := s.request.MockItemOutputs[spec.Name]; ok {
		s.usedItemMocks[spec.Name] = true
	}
	count := len(inputs)
	row.State, row.Input, row.ItemCount = "expanded", items, &count
	for index, input := range inputs {
		name := api.WorkflowForEachItemName(spec.Name, index)
		item := simulationRow(spec.ForEach.Action.Step(name))
		item.ParentStep, item.ItemIndex, item.Input = spec.Name, &index, input
		s.steps[name] = WorkflowStep{StepName: name, Status: WorkflowStepStatusPending}
		if index < len(mocks) {
			item.State, item.Output = "mocked", cloneWorkflowJSON(mocks[index])
			s.succeed(&item)
		} else if index == len(mocks) {
			item.State, item.Reason = "would_execute", "mock_output_missing"
		} else {
			item.State, item.Reason, item.BlockedBy = "blocked", "previous_item_output_missing", []string{api.WorkflowForEachItemName(spec.Name, index-1)}
		}
		if err := s.record(spec.Name, item); err != nil {
			return err
		}
	}
	if count == len(mocks) {
		output, err := workflowForEachOutputs(s.steps, spec.Name, count, "", nil)
		if err != nil {
			return simulationLimit("loop output bytes", api.WorkflowForEachMaxOutputBytes, api.WorkflowForEachMaxOutputBytes+1)
		}
		row.State, row.Output = "resolved", output
		s.succeed(row)
	} else {
		row.Reason = "item_outputs_missing"
	}
	return nil
}

func (s *automationSimulator) record(parent string, row api.AutomationSimulationStep) error {
	if err := simulationLimit("trace entries", api.AutomationSimulationMaxTraceEntries, int64(s.traceCount+1)); err != nil {
		return err
	}
	raw, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("marshal simulation trace: %w", err)
	}
	if err := simulationLimit("response bytes", api.AutomationSimulationResponseMaxBytes, s.traceBytes+int64(len(raw))+1); err != nil {
		return err
	}
	s.traceCount++
	s.traceBytes += int64(len(raw)) + 1
	if row.ParentStep == "" {
		s.rows[parent] = append([]api.AutomationSimulationStep{row}, s.rows[parent]...)
	} else {
		s.rows[parent] = append(s.rows[parent], row)
	}
	return nil
}

func (s *automationSimulator) finish() (api.SimulateAutomationResponse, error) {
	s.response.Complete = true
	for _, name := range s.response.StepOrder {
		s.response.Trace = append(s.response.Trace, s.rows[name]...)
		status := s.steps[name].Status
		if status != WorkflowStepStatusSucceeded && status != WorkflowStepStatusSkipped {
			s.response.Complete = false
		}
	}
	for name := range s.request.MockOutputs {
		if !s.usedMocks[name] {
			s.response.Warnings = append(s.response.Warnings, fmt.Sprintf("mock output for %q was not used", name))
		}
	}
	for name := range s.request.MockItemOutputs {
		if !s.usedItemMocks[name] {
			s.response.Warnings = append(s.response.Warnings, fmt.Sprintf("item mocks for %q were not used", name))
		}
	}
	sort.Strings(s.response.Warnings)
	raw, err := json.Marshal(s.response)
	if err != nil {
		return s.response, fmt.Errorf("marshal simulation response: %w", err)
	}
	return s.response, simulationLimit("response bytes", api.AutomationSimulationResponseMaxBytes, int64(len(raw)))
}

func simulationWait(spec api.WorkflowStepSpec) bool {
	return spec.WaitForEvent != "" || spec.WaitForCallback || spec.WaitForDuration != 0 || spec.WaitForCondition != nil
}

func simulationRow(spec api.WorkflowStepSpec) api.AutomationSimulationStep {
	row := api.AutomationSimulationStep{StepName: spec.Name}
	switch {
	case spec.ForEach != nil:
		row.Kind = "for_each"
	case spec.Join != nil:
		row.Kind = "join"
	case spec.WaitForEvent != "":
		row.Kind, row.WaitFor = "event_wait", spec.WaitForEvent
	case spec.WaitForCallback:
		row.Kind = "callback_wait"
	case spec.WaitForDuration != 0:
		row.Kind, row.WaitFor = "duration_wait", spec.WaitForDuration.String()
	case spec.WaitForCondition != nil:
		row.Kind, row.Run = "condition_wait", spec.WaitForCondition.Run
	case spec.Outbound != nil:
		row.Kind, row.IntegrationID, row.Path, row.Method = "outbound", spec.Outbound.IntegrationID, spec.Outbound.Path, spec.Outbound.Method
	default:
		row.Kind, row.Run, row.Path, row.Method = "path", spec.Run, spec.Path, spec.Method
		if spec.Run != "" {
			row.Kind, row.Path = "run", "/"+strings.TrimSpace(spec.Run)
		}
		if row.Method == "" {
			row.Method = "POST"
		}
	}
	return row
}
