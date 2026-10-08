// ADR-715: immutable declarations validate public, transaction-backed milestones.
package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func compileMilestones(spec *api.OperationDefinitionSpec, limits api.OperationPlanLimits) (map[string]*jsonschema.Schema, error) {
	if len(spec.Milestones) == 0 {
		spec.Milestones = nil
		return nil, nil
	}
	if spec.HTTPTransactionVersion != api.OperationHTTPTransactionVersion || len(spec.Milestones) > api.OperationMilestoneDeclarationsMax {
		return nil, fmt.Errorf("milestones require HTTP transactions and a bounded declaration set")
	}
	compiled := make(map[string]*jsonschema.Schema, len(spec.Milestones))
	canonical := make(map[string]json.RawMessage, len(spec.Milestones))
	total := 0
	for _, name := range MilestoneNames(spec.Milestones) {
		if len(name) == 0 || len(name) > api.OperationNameMaxBytes || !operationName.MatchString(name) {
			return nil, fmt.Errorf("milestone name must be a bounded lowercase slug")
		}
		schema, data, err := compileSchema(spec.Milestones[name], limits.SchemaBytes)
		if err != nil {
			return nil, fmt.Errorf("milestone schema %s: %w", name, err)
		}
		total += len(data)
		if total > api.OperationMilestoneSchemasMaxBytes {
			return nil, fmt.Errorf("milestone schemas exceed their aggregate byte limit")
		}
		compiled[name], canonical[name] = schema, data
	}
	spec.Milestones = canonical
	return compiled, nil
}

func MilestoneNames(declarations map[string]json.RawMessage) []string {
	names := make([]string, 0, len(declarations))
	for name := range declarations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func compileWorkflowSteps(spec *api.OperationDefinitionSpec) error {
	if len(spec.WorkflowSteps) == 0 {
		spec.WorkflowSteps = nil
		return nil
	}
	if len(spec.WorkflowSteps) > api.OperationWorkflowStepsMaxPerOperation {
		return fmt.Errorf("operation workflow steps exceed their per-operation limit")
	}
	seen := make(map[string]bool, len(spec.WorkflowSteps))
	milestones := make(map[string]bool, len(spec.WorkflowSteps))
	positions := make(map[string]map[int]bool)
	workflowStates := make(map[string][]string)
	workflowTerminalStates := make(map[string][]string)
	workflowStateStaleAfter := make(map[string]map[string]int64)
	workflowTransitions := make(map[string][]api.OperationWorkflowTransition)
	for i := range spec.WorkflowSteps {
		states := append([]string(nil), spec.WorkflowSteps[i].States...)
		if len(states) > api.OperationWorkflowStatesMax {
			return fmt.Errorf("operation workflow state list exceeds its limit")
		}
		sort.Strings(states)
		for j, state := range states {
			if api.ValidateOperationWorkflowStateName(state) != nil || j > 0 && states[j-1] == state {
				return fmt.Errorf("operation workflow state list contains an invalid or duplicate state")
			}
		}
		spec.WorkflowSteps[i].States = states
		stateSet := make(map[string]bool, len(states))
		for _, state := range states {
			stateSet[state] = true
		}
		terminalStates := append([]string(nil), spec.WorkflowSteps[i].TerminalStates...)
		if len(terminalStates) > api.OperationWorkflowStatesMax {
			return fmt.Errorf("operation workflow terminal state list exceeds its limit")
		}
		sort.Strings(terminalStates)
		terminalSet := make(map[string]bool, len(terminalStates))
		for j, state := range terminalStates {
			if api.ValidateOperationWorkflowStateName(state) != nil || !stateSet[state] || j > 0 && terminalStates[j-1] == state {
				return fmt.Errorf("operation workflow terminal state list contains an invalid, duplicate, or undeclared state")
			}
			terminalSet[state] = true
		}
		spec.WorkflowSteps[i].TerminalStates = terminalStates
		staleAfter := spec.WorkflowSteps[i].StateStaleAfterSeconds
		if len(staleAfter) > api.OperationWorkflowStatesMax {
			return fmt.Errorf("operation workflow state staleness threshold list exceeds its limit")
		}
		canonicalStaleAfter := make(map[string]int64, len(staleAfter))
		for state, seconds := range staleAfter {
			if api.ValidateOperationWorkflowStateName(state) != nil || !stateSet[state] || terminalSet[state] || seconds < 1 || seconds > api.OperationWorkflowStateStaleAfterMaxSeconds {
				return fmt.Errorf("operation workflow state staleness threshold is invalid or references a terminal or undeclared state")
			}
			canonicalStaleAfter[state] = seconds
		}
		if len(canonicalStaleAfter) == 0 {
			canonicalStaleAfter = nil
		}
		spec.WorkflowSteps[i].StateStaleAfterSeconds = canonicalStaleAfter
		transitions := append([]api.OperationWorkflowTransition(nil), spec.WorkflowSteps[i].Transitions...)
		if len(transitions) > api.OperationWorkflowTransitionsMax {
			return fmt.Errorf("operation workflow transition list exceeds its limit")
		}
		sort.Slice(transitions, func(a, b int) bool {
			if transitions[a].From != transitions[b].From {
				return transitions[a].From < transitions[b].From
			}
			return transitions[a].To < transitions[b].To
		})
		for j, transition := range transitions {
			if api.ValidateOperationWorkflowStateName(transition.From) != nil || api.ValidateOperationWorkflowStateName(transition.To) != nil ||
				!stateSet[transition.From] || !stateSet[transition.To] || j > 0 && transitions[j-1] == transition {
				return fmt.Errorf("operation workflow transition is invalid, duplicated, or references an undeclared state")
			}
			if terminalSet[transition.From] {
				return fmt.Errorf("operation workflow terminal state cannot have an outgoing transition")
			}
		}
		spec.WorkflowSteps[i].Transitions = transitions
	}
	for _, step := range spec.WorkflowSteps {
		if !operationName.MatchString(step.Workflow) || len(step.Title) == 0 || len(step.Title) > api.OperationWorkflowTitleMaxBytes ||
			!utf8.ValidString(step.Title) || strings.TrimSpace(step.Title) != step.Title || strings.ContainsAny(step.Title, "\x00\r\n\t") ||
			!operationName.MatchString(step.Step) || len(step.Label) == 0 || len(step.Label) > api.OperationWorkflowLabelMaxBytes ||
			!utf8.ValidString(step.Label) || strings.TrimSpace(step.Label) != step.Label || strings.ContainsAny(step.Label, "\x00\r\n\t") ||
			!operationName.MatchString(step.Milestone) || step.Position < 1 || step.Position > api.OperationWorkflowStepsMax || step.InstanceID != "" {
			return fmt.Errorf("operation workflow step has invalid names, labels, milestone or position")
		}
		if step.InstanceIDFrom != "" && ValidateOperationWorkflowInstancePointer(step.InstanceIDFrom) != nil {
			return fmt.Errorf("operation workflow step has an invalid instance ID pointer")
		}
		if _, declared := spec.Milestones[step.Milestone]; !declared || spec.HTTPTransactionVersion != api.OperationHTTPTransactionVersion {
			return fmt.Errorf("operation workflow step must map to a declared transaction-backed milestone")
		}
		key := step.Workflow + "/" + step.Step
		milestoneKey := step.Workflow + "/" + step.Milestone
		if prior, exists := workflowStates[step.Workflow]; exists {
			if len(prior) != len(step.States) {
				return fmt.Errorf("operation workflow state declarations must match across steps")
			}
			for i := range prior {
				if prior[i] != step.States[i] {
					return fmt.Errorf("operation workflow state declarations must match across steps")
				}
			}
		} else {
			workflowStates[step.Workflow] = append([]string(nil), step.States...)
		}
		if prior, exists := workflowTransitions[step.Workflow]; exists {
			if len(prior) != len(step.Transitions) {
				return fmt.Errorf("operation workflow transition declarations must match across steps")
			}
			for i := range prior {
				if prior[i] != step.Transitions[i] {
					return fmt.Errorf("operation workflow transition declarations must match across steps")
				}
			}
		} else {
			workflowTransitions[step.Workflow] = append([]api.OperationWorkflowTransition(nil), step.Transitions...)
		}
		if prior, exists := workflowTerminalStates[step.Workflow]; exists {
			if len(prior) != len(step.TerminalStates) {
				return fmt.Errorf("operation workflow terminal state declarations must match across steps")
			}
			for i := range prior {
				if prior[i] != step.TerminalStates[i] {
					return fmt.Errorf("operation workflow terminal state declarations must match across steps")
				}
			}
		} else {
			workflowTerminalStates[step.Workflow] = append([]string(nil), step.TerminalStates...)
		}
		if prior, exists := workflowStateStaleAfter[step.Workflow]; exists {
			if !sameWorkflowStateStaleAfter(prior, step.StateStaleAfterSeconds) {
				return fmt.Errorf("operation workflow state staleness declarations must match across steps")
			}
		} else {
			workflowStateStaleAfter[step.Workflow] = cloneWorkflowStateStaleAfter(step.StateStaleAfterSeconds)
		}
		if seen[key] || milestones[milestoneKey] {
			return fmt.Errorf("operation workflow step is duplicated")
		}
		if positions[step.Workflow] == nil {
			positions[step.Workflow] = make(map[int]bool)
		}
		if positions[step.Workflow][step.Position] {
			return fmt.Errorf("operation workflow positions must be unique within a workflow")
		}
		seen[key], milestones[milestoneKey], positions[step.Workflow][step.Position] = true, true, true
	}
	sort.Slice(spec.WorkflowSteps, func(i, j int) bool {
		left, right := spec.WorkflowSteps[i], spec.WorkflowSteps[j]
		if left.Workflow != right.Workflow {
			return left.Workflow < right.Workflow
		}
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return left.Step < right.Step
	})
	return nil
}

func cloneWorkflowStateStaleAfter(values map[string]int64) map[string]int64 {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]int64, len(values))
	for state, seconds := range values {
		cloned[state] = seconds
	}
	return cloned
}

func sameWorkflowStateStaleAfter(left, right map[string]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for state, seconds := range left {
		if right[state] != seconds {
			return false
		}
	}
	return true
}

// OperationWorkflowStateIsTerminal reports whether the pinned declaration
// marks this app-reported workflow state as terminal.
func OperationWorkflowStateIsTerminal(spec api.OperationDefinitionSpec, workflow, state string) bool {
	for _, step := range spec.WorkflowSteps {
		if step.Workflow != workflow {
			continue
		}
		for _, terminalState := range step.TerminalStates {
			if terminalState == state {
				return true
			}
		}
	}
	return false
}

// OperationWorkflowStateStaleAfterSeconds returns the app-declared age
// threshold for a state, or zero when the state has no threshold.
func OperationWorkflowStateStaleAfterSeconds(spec api.OperationDefinitionSpec, workflow, state string) int64 {
	for _, step := range spec.WorkflowSteps {
		if step.Workflow == workflow {
			return step.StateStaleAfterSeconds[state]
		}
	}
	return 0
}

func WorkflowStepsForMilestone(spec api.OperationDefinitionSpec, name string, payload []byte) ([]api.OperationWorkflowSpec, error) {
	steps := make([]api.OperationWorkflowSpec, 0, len(spec.WorkflowSteps))
	for _, step := range spec.WorkflowSteps {
		if step.Milestone == name {
			steps = append(steps, step)
		}
	}
	return ResolveWorkflowInstanceIDs(steps, payload)
}

func ResolveWorkflowInstanceIDs(steps []api.OperationWorkflowSpec, payload []byte) ([]api.OperationWorkflowSpec, error) {
	resolved := append([]api.OperationWorkflowSpec(nil), steps...)
	for i := range resolved {
		// Previously pinned definitions had no instance selector. Preserve their
		// workflow grouping while new source declarations provide an explicit ID.
		if resolved[i].InstanceIDFrom == "" {
			continue
		}
		instanceID, err := ExtractOperationWorkflowInstanceID(resolved[i].InstanceIDFrom, payload)
		if err != nil {
			return nil, err
		}
		resolved[i].InstanceID = instanceID
	}
	return resolved, nil
}

// CanonicalMilestone makes replay identity independent of JSON spelling and zones.
func (c *Contract) CanonicalMilestone(report api.OperationMilestoneRequest) (api.OperationMilestoneRequest, error) {
	report.OccurredAt = report.OccurredAt.UTC()
	id, err := uuid.Parse(report.ID)
	if err != nil || id == uuid.Nil || id.String() != report.ID || report.OccurredAt.IsZero() || report.OccurredAt.Year() < 1 || report.OccurredAt.Year() > 9999 {
		return report, fmt.Errorf("milestone requires a canonical UUID and a finite timestamp")
	}
	schema, exists := c.Milestones[report.Name]
	if !exists {
		return report, fmt.Errorf("milestone name is not declared")
	}
	if len(report.Payload) == 0 || len(report.Payload) > api.OperationMilestonePayloadMaxBytes {
		return report, fmt.Errorf("milestone payload exceeds its byte limit")
	}
	data, err := CanonicalJSON(report.Payload)
	if err != nil {
		return report, err
	}
	if len(data) > api.OperationMilestonePayloadMaxBytes {
		return report, fmt.Errorf("canonical milestone payload exceeds its byte limit")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return report, err
	}
	if err := schema.Validate(value); err != nil {
		return report, fmt.Errorf("milestone payload does not match its declared schema")
	}
	report.Payload = data
	report.OccurredAt = report.OccurredAt.UTC().Truncate(time.Microsecond)
	if _, err := WorkflowStepsForMilestone(c.Spec, report.Name, report.Payload); err != nil {
		return report, fmt.Errorf("milestone payload must contain a valid workflow instance ID")
	}
	return report, nil
}

// CanonicalWorkflowState validates an explicit state update against the
// workflow vocabulary pinned to the Operation definition.
func (c *Contract) CanonicalWorkflowState(opHasSubject bool, report api.OperationWorkflowStateReport) (api.OperationWorkflowStateReport, string, error) {
	report.OccurredAt = report.OccurredAt.UTC()
	id, err := uuid.Parse(report.ID)
	if err != nil || id == uuid.Nil || id.String() != report.ID || report.Revision < 1 || report.OccurredAt.IsZero() || report.OccurredAt.Year() < 1 || report.OccurredAt.Year() > 9999 {
		return report, "", fmt.Errorf("workflow state requires a canonical UUID, positive revision and finite timestamp")
	}
	if !opHasSubject || api.ValidateOperationWorkflowName(report.Workflow) != nil ||
		api.ValidateOperationWorkflowInstanceID(report.InstanceID) != nil || api.ValidateOperationWorkflowStateName(report.State) != nil ||
		report.FromState != "" && api.ValidateOperationWorkflowStateName(report.FromState) != nil {
		return report, "", fmt.Errorf("workflow state requires a business reference and valid workflow, instance and state")
	}
	declared, transitionsDeclared, transitionAllowed := false, false, false
	for _, step := range c.Spec.WorkflowSteps {
		if step.Workflow != report.Workflow || step.InstanceIDFrom == "" {
			continue
		}
		for _, state := range step.States {
			if state == report.State {
				declared = true
				break
			}
		}
		if len(step.Transitions) > 0 {
			transitionsDeclared = true
			for _, transition := range step.Transitions {
				if transition.From == report.FromState && transition.To == report.State {
					transitionAllowed = true
					break
				}
			}
		}
	}
	if !declared {
		return report, "", fmt.Errorf("workflow state is not declared for this Operation")
	}
	if transitionsDeclared && (!transitionAllowed || report.FromState == "") {
		return report, "", fmt.Errorf("workflow state transition is not declared for this Operation")
	}
	if !transitionsDeclared && report.FromState != "" {
		return report, "", fmt.Errorf("workflow transition is not enabled for this Operation")
	}
	report.OccurredAt = report.OccurredAt.Truncate(time.Microsecond)
	raw, _ := json.Marshal(report)
	fingerprint, err := InputFingerprint(raw)
	return report, fingerprint, err
}
