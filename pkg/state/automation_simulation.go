package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	s := automationSimulator{request: request, snapshot: snapshot, response: response, specs: map[string]api.WorkflowStepSpec{}, steps: map[string]WorkflowStep{}, rows: map[string][]api.AutomationSimulationStep{}, done: map[string]bool{}, usedMocks: map[string]bool{}, usedItemMocks: map[string]bool{}, usedAttemptMocks: map[string]bool{}, usedItemAttemptMocks: map[string]map[string]bool{}}
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
	for name, attempts := range request.MockAttempts {
		if err := validateSimulationAttempts(name, attempts); err != nil {
			return nil, err
		}
	}
	for name, items := range request.MockItemAttempts {
		if len(items) == 0 {
			return nil, fmt.Errorf("%w: item attempt maps must not be empty", ErrAutomationSimulationInvalid)
		}
		if err := simulationLimit("item attempt mocks", api.WorkflowForEachMaxItems, int64(len(items))); err != nil {
			return nil, err
		}
		for index, attempts := range items {
			if _, err := simulationItemIndex(index); err != nil {
				return nil, err
			}
			if err := validateSimulationAttempts(name+"/"+index, attempts); err != nil {
				return nil, err
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
	request                                          api.SimulateAutomationRequest
	snapshot                                         json.RawMessage
	response                                         api.SimulateAutomationResponse
	specs                                            map[string]api.WorkflowStepSpec
	steps                                            map[string]WorkflowStep
	rows                                             map[string][]api.AutomationSimulationStep
	done, usedMocks, usedItemMocks, usedAttemptMocks map[string]bool
	usedItemAttemptMocks                             map[string]map[string]bool
	traceCount                                       int
	traceBytes                                       int64
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
	for name, attempts := range s.request.MockAttempts {
		step, ok := s.specs[name]
		if !ok || step.ForEach != nil || step.Join != nil {
			return fmt.Errorf("%w: mock_attempts must name action or wait steps", ErrAutomationSimulationInvalid)
		}
		if _, duplicate := s.request.MockOutputs[name]; duplicate {
			return fmt.Errorf("%w: a step cannot use both mock_outputs and mock_attempts", ErrAutomationSimulationInvalid)
		}
		if simulationWait(step) {
			if len(attempts) != 1 {
				return fmt.Errorf("%w: waits require exactly one outcome in mock_attempts", ErrAutomationSimulationInvalid)
			}
			switch attempts[0].Outcome {
			case "success":
				if step.WaitForEvent == "" && !step.WaitForCallback {
					return fmt.Errorf("%w: successful wait mocks require an event or callback wait", ErrAutomationSimulationInvalid)
				}
				if simulationTimeoutMock(attempts[0].Output) {
					return fmt.Errorf("%w: successful wait payload cannot be the reserved timeout sentinel", ErrAutomationSimulationInvalid)
				}
			case "timeout":
				if step.Timeout <= 0 || step.OnTimeout == "" {
					return fmt.Errorf("%w: wait timeout mocks require a timeout and on_timeout route", ErrAutomationSimulationInvalid)
				}
			default:
				return fmt.Errorf("%w: waits support only success or timeout outcomes", ErrAutomationSimulationInvalid)
			}
		} else {
			for _, attempt := range attempts {
				if attempt.Outcome == "timeout" && step.OnTimeout == "" {
					return fmt.Errorf("%w: timeout outcomes require an on_timeout route", ErrAutomationSimulationInvalid)
				}
			}
		}
	}
	for name := range s.request.MockItemOutputs {
		if step, ok := s.specs[name]; !ok || step.ForEach == nil {
			return fmt.Errorf("%w: mock_item_outputs must name for_each steps", ErrAutomationSimulationInvalid)
		}
	}
	for name, items := range s.request.MockItemAttempts {
		step, exists := s.specs[name]
		if !exists || step.ForEach == nil {
			return fmt.Errorf("%w: mock_item_attempts must name for_each steps", ErrAutomationSimulationInvalid)
		}
		if _, duplicate := s.request.MockItemOutputs[name]; duplicate {
			return fmt.Errorf("%w: a loop cannot use both mock_item_outputs and mock_item_attempts", ErrAutomationSimulationInvalid)
		}
		for _, attempts := range items {
			for _, attempt := range attempts {
				if attempt.Outcome == "timeout" && step.ForEach.Action.Timeout <= 0 {
					return fmt.Errorf("%w: item timeout mocks require an action timeout", ErrAutomationSimulationInvalid)
				}
			}
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
	failureContext := s.failureContext(spec.Name)
	var input json.RawMessage
	var err error
	if len(spec.Input) == 0 && len(failureContext) > 0 {
		input, err = json.Marshal(struct {
			Input   json.RawMessage `json:"input"`
			Failure json.RawMessage `json:"failure"`
		}{s.request.Input, failureContext})
		if err == nil && int64(len(input)) > api.WorkflowRunInputMaxBytes {
			return false, simulationLimit("resolved input bytes", api.WorkflowRunInputMaxBytes, int64(len(input)))
		}
	} else {
		input, err = api.ResolveWorkflowStepInputBounded(spec.Input, s.request.Input, s.outputs(spec), failureContext, api.WorkflowRunInputMaxBytes)
	}
	if errors.Is(err, api.ErrWorkflowInputLimit) {
		return false, simulationLimit("resolved input bytes", api.WorkflowRunInputMaxBytes, api.WorkflowRunInputMaxBytes+1)
	}
	if err != nil {
		s.evaluationError(row, "input_resolution_failed")
		return true, nil //nolint:nilerr // Evaluation failures are reported in the hypothetical trace.
	}
	row.Input = input
	if !s.resolveOutbound(spec.Outbound, s.request.Input, s.outputs(spec), failureContext, row) {
		return true, nil
	}
	if attempts, ok := s.request.MockAttempts[spec.Name]; ok {
		s.usedAttemptMocks[spec.Name] = true
		return true, s.resolveMockAttempts(spec, row, attempts)
	}
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

func (s *automationSimulator) resolveMockAttempts(spec api.WorkflowStepSpec, row *api.AutomationSimulationStep, attempts []api.AutomationSimulationMockAttempt) error {
	if simulationWait(spec) {
		mock := attempts[0]
		if mock.Outcome == "success" {
			row.State, row.Reason = "mocked", "event_received_mocked"
			if spec.WaitForCallback {
				row.Reason = "callback_received_mocked"
			}
			row.Output = cloneWorkflowJSON(mock.Output)
		} else {
			row.State, row.Reason = "timed_out", "timeout_mocked"
			row.Output = json.RawMessage(`{"timeout":true}`)
		}
		row.Attempts = []api.AutomationSimulationAttempt{{Attempt: 1, Outcome: mock.Outcome}}
		s.setAttempt(row.StepName, 1, WorkflowStepStatusSucceeded, row.Output, nil)
		return nil
	}
	for index, mock := range attempts {
		attemptNumber := index + 1
		summary := api.AutomationSimulationAttempt{Attempt: attemptNumber, Outcome: mock.Outcome, HTTPStatus: mock.HTTPStatus}
		row.Attempts = append(row.Attempts, summary)
		if mock.Outcome == "timeout" && row.ParentStep != "" {
			mock.Outcome, mock.Error = "failure", "item action timed out"
		}
		switch mock.Outcome {
		case "success":
			if index != len(attempts)-1 {
				return fmt.Errorf("%w: mock_attempts for %q include outcomes after success", ErrAutomationSimulationInvalid, spec.Name)
			}
			row.State, row.Output = "mocked", cloneWorkflowJSON(mock.Output)
			s.setAttempt(row.StepName, attemptNumber, WorkflowStepStatusSucceeded, row.Output, nil)
			return nil
		case "timeout":
			if index != len(attempts)-1 {
				return fmt.Errorf("%w: mock_attempts for %q include outcomes after timeout", ErrAutomationSimulationInvalid, spec.Name)
			}
			row.State, row.Reason = "timed_out", "timeout_mocked"
			row.Output = json.RawMessage(`{"timeout":true}`)
			s.setAttempt(row.StepName, attemptNumber, WorkflowStepStatusSucceeded, row.Output, nil)
			return nil
		case "failure":
			message := simulationFailureMessage(spec, mock)
			httpStatus := 0
			if mock.HTTPStatus != nil {
				httpStatus = *mock.HTTPStatus
			}
			retryDecision := api.EvaluateWorkflowRetry(spec, httpStatus, mock.Error != "", attemptNumber)
			if retryDecision.ShouldRetry {
				if index == len(attempts)-1 {
					row.State, row.Reason = "would_retry", "retry_outcome_missing"
					s.setAttempt(row.StepName, attemptNumber, WorkflowStepStatusPending, nil, &message)
					return nil
				}
				continue
			}
			if index != len(attempts)-1 {
				return fmt.Errorf("%w: mock_attempts for %q include outcomes after a terminal failure", ErrAutomationSimulationInvalid, spec.Name)
			}
			status := WorkflowStepStatusFailed
			if retryDecision.IsDead {
				status = WorkflowStepStatusDead
			}
			row.State, row.Reason = status, "non_retryable_failure"
			if retryDecision.Retryable {
				row.Reason = "retry_limit_reached"
			}
			s.setAttempt(row.StepName, attemptNumber, status, nil, &message)
			return nil
		}
	}
	return fmt.Errorf("%w: mock_attempts for %q contain no outcome", ErrAutomationSimulationInvalid, spec.Name)
}

func simulationFailureMessage(spec api.WorkflowStepSpec, mock api.AutomationSimulationMockAttempt) string {
	if mock.Error != "" {
		return mock.Error
	}
	if spec.Outbound != nil {
		return fmt.Sprintf("Outbound HTTP %d", *mock.HTTPStatus)
	}
	return fmt.Sprintf("HTTP %d", *mock.HTTPStatus)
}

func (s *automationSimulator) setAttempt(name string, attempt int, status string, output json.RawMessage, message *string) {
	step := s.steps[name]
	step.Attempt, step.Status, step.Output, step.Error = attempt, status, cloneWorkflowJSON(output), message
	s.steps[name] = step
}

func (s *automationSimulator) failureContext(handlerName string) json.RawMessage {
	for _, source := range s.request.Definition.Steps {
		if source.OnFailure != handlerName {
			continue
		}
		step := s.steps[source.Name]
		if (step.Status != WorkflowStepStatusFailed && step.Status != WorkflowStepStatusDead) || step.Error == nil {
			continue
		}
		context, err := json.Marshal(struct {
			Step    string `json:"step"`
			Status  string `json:"status"`
			Attempt int    `json:"attempt"`
			Message string `json:"message"`
		}{Step: step.StepName, Status: step.Status, Attempt: step.Attempt, Message: *step.Error})
		if err == nil {
			return context
		}
	}
	return nil
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
		if !s.simulationSourceOutcomeKnown(source.Name) {
			sources = append(sources, source.Name)
		}
	}
	sort.Strings(sources)
	return sources
}

func (s *automationSimulator) exceptionSkipped(name string) bool {
	for _, failureRoute := range []bool{false, true} {
		referenced, pending, triggered := false, false, false
		for _, source := range s.request.Definition.Steps {
			target := source.OnTimeout
			if failureRoute {
				target = source.OnFailure
			}
			if target != name {
				continue
			}
			referenced = true
			step := s.steps[source.Name]
			outcomeKnown := s.simulationSourceOutcomeKnown(source.Name)
			pending = pending || !outcomeKnown
			if failureRoute {
				triggered = triggered || (outcomeKnown && (step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead))
			} else {
				triggered = triggered || (outcomeKnown && step.Status == WorkflowStepStatusSucceeded && simulationTimeoutMock(step.Output))
			}
		}
		if referenced && !pending && !triggered {
			return true
		}
	}
	return false
}

func (s *automationSimulator) simulationSourceOutcomeKnown(name string) bool {
	if s.steps[name].Status == WorkflowStepStatusPending {
		return false
	}
	rows := s.rows[name]
	return len(rows) == 0 || rows[0].State != "error"
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

// Resolve URL values before accepting mocks using the executor's resolver.
func (s *automationSimulator) resolveOutbound(target *api.WorkflowOutboundSpec, input json.RawMessage, outputs map[string]json.RawMessage, failure json.RawMessage, row *api.AutomationSimulationStep) bool {
	if target == nil {
		return true
	}
	resolved, err := api.ResolveWorkflowOutboundTarget(*target, input, outputs, failure)
	if err != nil {
		s.evaluationError(row, "outbound_target_resolution_failed")
		return false
	}
	row.Path, row.RawQuery = resolved.Path, resolved.RawQuery
	if target.Method == "GET" || target.Method == "HEAD" {
		row.Input = nil
	}
	return true
}

func (s *automationSimulator) expand(spec api.WorkflowStepSpec, row *api.AutomationSimulationStep) error {
	items, inputs, matches, err := api.ResolveWorkflowForEachInputsWithGuardsBounded(spec, s.request.Input, s.outputs(spec))
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
	itemAttempts := s.request.MockItemAttempts[spec.Name]
	for value := range itemAttempts {
		index, _ := simulationItemIndex(value)
		if index >= len(inputs) {
			return fmt.Errorf("%w: item attempt index exceeds the materialized loop length", ErrAutomationSimulationInvalid)
		}
	}
	count := len(inputs)
	row.State, row.Input, row.ItemCount = "expanded", items, &count
	var sourceItems []json.RawMessage
	if spec.ForEach.Action.Outbound != nil {
		if err := json.Unmarshal(items, &sourceItems); err != nil {
			return err
		}
	}
	blockedBy := ""
	allResolved := true
	failureStatus := ""
	stopAfterFailure := ""
	for index, input := range inputs {
		name := api.WorkflowForEachItemName(spec.Name, index)
		item := simulationRow(spec.ForEach.Action.Step(name))
		item.ParentStep, item.ItemIndex, item.Input = spec.Name, &index, input
		step := WorkflowStep{StepName: name, Status: WorkflowStepStatusPending}
		if matches != nil {
			item.WhenMatched = matches[index]
			step.WhenMatched = matches[index]
		}
		s.steps[name] = step
		if matches != nil && !*matches[index] {
			s.skip(&item, WorkflowSkipWhenFalse)
			step := s.steps[name]
			step.WhenMatched = matches[index]
			s.steps[name] = step
		} else if stopAfterFailure != "" {
			s.skip(&item, "previous_item_failed")
		} else if blockedBy != "" {
			item.State, item.Reason, item.BlockedBy = "blocked", "previous_item_output_missing", []string{blockedBy}
			allResolved = false
		} else {
			var contextInput json.RawMessage
			if spec.ForEach.Action.Outbound != nil {
				contextInput, err = json.Marshal(struct {
					Item  json.RawMessage `json:"item"`
					Index int             `json:"index"`
					Input json.RawMessage `json:"input"`
				}{sourceItems[index], index, s.request.Input})
				if err != nil {
					return err
				}
			}
			if !s.resolveOutbound(spec.ForEach.Action.Outbound, contextInput, s.outputs(spec), nil, &item) {
				blockedBy, allResolved = name, false
			} else if attempts, supplied := itemAttempts[strconv.Itoa(index)]; supplied {
				if s.usedItemAttemptMocks[spec.Name] == nil {
					s.usedItemAttemptMocks[spec.Name] = map[string]bool{}
				}
				s.usedItemAttemptMocks[spec.Name][strconv.Itoa(index)] = true
				if err := s.resolveMockAttempts(spec.ForEach.Action.Step(name), &item, attempts); err != nil {
					return err
				}
				status := s.steps[name].Status
				if status == WorkflowStepStatusPending {
					blockedBy, allResolved = name, false
				}
				if status == WorkflowStepStatusFailed || status == WorkflowStepStatusDead {
					if failureStatus == "" || status == WorkflowStepStatusDead {
						failureStatus = status
					}
					if spec.ForEach.OnItemFailure != "continue" {
						stopAfterFailure = name
					}
				}
			} else if index < len(mocks) {
				item.State, item.Output = "mocked", cloneWorkflowJSON(mocks[index])
				s.succeed(&item)
			} else {
				item.State, item.Reason = "would_execute", "mock_output_missing"
				blockedBy, allResolved = name, false
			}
		}
		if err := s.record(spec.Name, item); err != nil {
			return err
		}
	}
	if allResolved {
		output, err := workflowForEachOutputs(s.steps, spec.Name, count, "", nil, spec.ForEach.OnItemFailure == "continue")
		if err != nil {
			return simulationLimit("loop output bytes", api.WorkflowForEachMaxOutputBytes, api.WorkflowForEachMaxOutputBytes+1)
		}
		row.State, row.Output = "resolved", output
		if failureStatus != "" {
			row.State, row.Reason = failureStatus, "item_failure_mocked"
			parent := s.steps[spec.Name]
			parent.Status, parent.Output = failureStatus, output
			s.steps[spec.Name] = parent
		} else {
			s.succeed(row)
		}
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
		if status != WorkflowStepStatusSucceeded && status != WorkflowStepStatusSkipped && status != WorkflowStepStatusFailed && status != WorkflowStepStatusDead {
			s.response.Complete = false
		}
		if s.rows[name][0].State == "error" {
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
	for name := range s.request.MockAttempts {
		if !s.usedAttemptMocks[name] {
			s.response.Warnings = append(s.response.Warnings, fmt.Sprintf("attempt mocks for %q were not used", name))
		}
	}
	for name, items := range s.request.MockItemAttempts {
		for index := range items {
			if !s.usedItemAttemptMocks[name][index] {
				s.response.Warnings = append(s.response.Warnings, fmt.Sprintf("item attempt mocks for %q index %s were not used", name, index))
			}
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

func validateSimulationAttempts(name string, attempts []api.AutomationSimulationMockAttempt) error {
	if err := simulationLimit("attempt mocks", api.WorkflowRetryMaxAttempts, int64(len(attempts))); err != nil {
		return err
	}
	if len(attempts) == 0 {
		return fmt.Errorf("%w: attempt mocks must contain at least one outcome", ErrAutomationSimulationInvalid)
	}
	for index, attempt := range attempts {
		resource := fmt.Sprintf("attempt mock for %s, attempt %d", name, index+1)
		switch attempt.Outcome {
		case "success":
			if len(attempt.Output) == 0 || attempt.Error != "" || attempt.HTTPStatus != nil {
				return fmt.Errorf("%w: %s success requires only an output", ErrAutomationSimulationInvalid, resource)
			}
			if err := simulationJSON(attempt.Output, resource+" output"); err != nil {
				return err
			}
		case "failure":
			if len(attempt.Output) != 0 || (attempt.Error == "") == (attempt.HTTPStatus == nil) {
				return fmt.Errorf("%w: %s failure requires exactly one of error or http_status", ErrAutomationSimulationInvalid, resource)
			}
			if int64(len(attempt.Error)) > api.WorkflowRunInputMaxBytes {
				return simulationLimit(resource+" error bytes", api.WorkflowRunInputMaxBytes, int64(len(attempt.Error)))
			}
			if attempt.HTTPStatus != nil && (*attempt.HTTPStatus < 100 || *attempt.HTTPStatus > 599 || (*attempt.HTTPStatus >= 200 && *attempt.HTTPStatus < 300)) {
				return fmt.Errorf("%w: %s http_status must be a non-2xx status from 100 to 599", ErrAutomationSimulationInvalid, resource)
			}
		case "timeout":
			if len(attempt.Output) != 0 || attempt.Error != "" || attempt.HTTPStatus != nil {
				return fmt.Errorf("%w: %s timeout cannot include other outcome fields", ErrAutomationSimulationInvalid, resource)
			}
		default:
			return fmt.Errorf("%w: %s outcome must be success, failure, or timeout", ErrAutomationSimulationInvalid, resource)
		}
	}
	return nil
}

func simulationItemIndex(value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index >= api.WorkflowForEachMaxItems || strconv.Itoa(index) != value {
		return 0, fmt.Errorf("%w: item attempt indexes must be canonical zero-based indexes below %d", ErrAutomationSimulationInvalid, api.WorkflowForEachMaxItems)
	}
	return index, nil
}
