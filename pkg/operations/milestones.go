// ADR-715: immutable declarations validate public, transaction-backed milestones.
package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
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
	workflowTransitionsDeclared := make(map[string]bool)
	workflowVersions := make(map[string]int)
	for i := range spec.WorkflowSteps {
		if spec.WorkflowSteps[i].Version < 0 || spec.WorkflowSteps[i].Version > api.OperationWorkflowContractVersionMax {
			return fmt.Errorf("operation workflow contract version is invalid")
		}
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
		for j := range transitions {
			if transitions[j].RequiredDependencyWorkflows != nil {
				workflows := append([]string{}, (*transitions[j].RequiredDependencyWorkflows)...)
				if len(workflows) > 16 {
					return fmt.Errorf("transition prerequisite workflow list exceeds limit")
				}
				sort.Strings(workflows)
				for k, workflow := range workflows {
					if api.ValidateOperationWorkflowName(workflow) != nil || k > 0 && workflows[k-1] == workflow {
						return fmt.Errorf("transition prerequisite workflows must be valid and unique")
					}
				}
				transitions[j].RequiredDependencyWorkflows = &workflows
			}
			effects, err := api.CanonicalOperationWorkflowEffects(transitions[j].RequiredEffects)
			if err != nil {
				return err
			}
			transitions[j].RequiredEffects = effects
			for _, effect := range effects {
				if spec.Milestones[effect.Milestone] == nil {
					return fmt.Errorf("effect requires a declared milestone")
				}
			}
			invariants, err := api.CanonicalOperationWorkflowInvariants(transitions[j].RequiredInvariants)
			if err != nil {
				return err
			}
			transitions[j].RequiredInvariants = invariants
			for _, invariant := range invariants {
				if spec.Milestones[invariant.Milestone] == nil {
					return fmt.Errorf("invariant requires a declared milestone")
				}
			}
			policies, err := api.CanonicalOperationWorkflowPolicies(transitions[j].RequiredPolicies)
			if err != nil {
				return err
			}
			transitions[j].RequiredPolicies = policies
			for _, effect := range effects {
				for _, policy := range policies {
					if effect.Milestone == policy.Milestone {
						return fmt.Errorf("effect and policy evidence require distinct milestones")
					}
				}
				for _, invariant := range invariants {
					if effect.Milestone == invariant.Milestone {
						return fmt.Errorf("effect and invariant evidence require distinct milestones")
					}
				}
			}
			for _, policy := range policies {
				for _, invariant := range invariants {
					if policy.Milestone == invariant.Milestone {
						return fmt.Errorf("a milestone cannot supply both policy and invariant evidence")
					}
				}
			}
			for _, policy := range policies {
				if spec.Milestones[policy.Milestone] == nil {
					return fmt.Errorf("policy requires a declared milestone")
				}
			}
			required := append([]string(nil), transitions[j].RequiredMilestones...)
			if len(required) > api.OperationWorkflowTransitionEvidenceMax {
				return fmt.Errorf("operation workflow transition evidence list exceeds its limit")
			}
			sort.Strings(required)
			for k, name := range required {
				if !operationName.MatchString(name) || spec.Milestones[name] == nil || k > 0 && required[k-1] == name {
					return fmt.Errorf("operation workflow transition requires an invalid, duplicate, or undeclared milestone")
				}
			}
			if len(required) == 0 {
				required = nil
			}
			transitions[j].RequiredMilestones = required
		}
		sort.Slice(transitions, func(a, b int) bool {
			if transitions[a].From != transitions[b].From {
				return transitions[a].From < transitions[b].From
			}
			return transitions[a].To < transitions[b].To
		})
		for j, transition := range transitions {
			if api.ValidateOperationWorkflowStateName(transition.From) != nil || api.ValidateOperationWorkflowStateName(transition.To) != nil ||
				!stateSet[transition.From] || !stateSet[transition.To] || j > 0 && transitions[j-1].From == transition.From && transitions[j-1].To == transition.To {
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
		version := step.Version
		if version == 0 {
			version = 1
		}
		if prior, exists := workflowVersions[step.Workflow]; exists {
			if prior != version {
				return fmt.Errorf("operation workflow contract versions must match across steps")
			}
		} else {
			workflowVersions[step.Workflow] = version
		}
		transitionsDeclared := step.TransitionsDeclared || len(step.Transitions) > 0
		if prior, exists := workflowTransitionsDeclared[step.Workflow]; exists {
			if prior != transitionsDeclared {
				return fmt.Errorf("operation workflow transition declarations must match across steps")
			}
		} else {
			workflowTransitionsDeclared[step.Workflow] = transitionsDeclared
		}
		if prior, exists := workflowTransitions[step.Workflow]; exists {
			if !sameWorkflowTransitions(prior, step.Transitions) {
				return fmt.Errorf("operation workflow transition declarations must match across steps")
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

func sameWorkflowTransitions(left, right []api.OperationWorkflowTransition) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !reflect.DeepEqual(left[i].RequiredEffects, right[i].RequiredEffects) || !reflect.DeepEqual(left[i].RequiredInvariants, right[i].RequiredInvariants) || !reflect.DeepEqual(left[i].RequiredDependencyWorkflows, right[i].RequiredDependencyWorkflows) || !reflect.DeepEqual(left[i].RequiredPolicies, right[i].RequiredPolicies) {
			return false
		}
		if left[i].From != right[i].From || left[i].To != right[i].To || len(left[i].RequiredMilestones) != len(right[i].RequiredMilestones) {
			return false
		}
		for j := range left[i].RequiredMilestones {
			if left[i].RequiredMilestones[j] != right[i].RequiredMilestones[j] {
				return false
			}
		}
	}
	return true
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
	steps, err := WorkflowStepsForMilestone(c.Spec, report.Name, report.Payload)
	if err != nil {
		return report, fmt.Errorf("milestone payload must contain a valid workflow instance ID")
	}
	decision, err := api.ParseOperationBusinessDecision(report.Payload)
	if err != nil {
		return report, err
	}
	if decision != nil {
		matched := false
		for _, step := range steps {
			if step.Workflow == decision.Workflow && step.InstanceID == decision.InstanceID {
				matched = true
			}
		}
		if !matched {
			return report, fmt.Errorf("business decision must match a declared workflow step and instance")
		}
	}
	reconciliation, err := api.ParseOperationWorkflowReconciliation(report.Payload)
	if err != nil {
		return report, err
	}
	if reconciliation != nil {
		matched := false
		for _, step := range steps {
			if step.Workflow == reconciliation.Workflow && step.InstanceID == reconciliation.InstanceID && (step.Version == reconciliation.ContractVersion || step.Version == 0 && reconciliation.ContractVersion == 1) {
				matched = true
			}
		}
		if !matched {
			return report, fmt.Errorf("reconciliation must match a declared workflow, instance, and contract version")
		}
	}
	invariant, err := api.ParseOperationBusinessInvariant(report.Payload)
	if err != nil {
		return report, err
	}
	if invariant != nil {
		matched := false
		for _, step := range steps {
			if step.Workflow != invariant.Workflow || step.InstanceID != invariant.InstanceID {
				continue
			}
			for _, state := range step.States {
				if state == invariant.State {
					matched = true
				}
			}
		}
		if !matched {
			return report, fmt.Errorf("invariant report must match a declared workflow, instance, and state")
		}
	}
	effect, err := api.ParseOperationBusinessEffect(report.Payload)
	if err != nil {
		return report, err
	}
	if effect != nil {
		matched := false
		for _, step := range steps {
			if step.Workflow != effect.Workflow || step.InstanceID != effect.InstanceID {
				continue
			}
			for _, state := range step.States {
				if state == effect.State {
					matched = true
				}
			}
		}
		if !matched || effect.Operation != c.Spec.Name {
			return report, fmt.Errorf("effect must match its reporting Operation and declared workflow, instance, and state")
		}
	}
	compensation, err := api.ParseOperationBusinessCompensation(report.Payload)
	if err != nil {
		return report, err
	}
	if compensation != nil {
		matched := false
		for _, step := range steps {
			if step.Workflow != compensation.Workflow || step.InstanceID != compensation.InstanceID {
				continue
			}
			for _, state := range step.States {
				if state == compensation.State {
					matched = true
				}
			}
		}
		if !matched || compensation.Operation != c.Spec.Name {
			return report, fmt.Errorf("compensation must match its reporting Operation and declared workflow, instance, and state")
		}
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
	allowReconciliation := false
	contractVersion := 0
	var requiredMilestones []string
	for _, step := range c.Spec.WorkflowSteps {
		if step.Workflow != report.Workflow || step.InstanceIDFrom == "" {
			continue
		}
		allowReconciliation = allowReconciliation || step.AllowReconciliation
		for _, state := range step.States {
			if state == report.State {
				declared = true
				break
			}
		}
		if contractVersion == 0 {
			contractVersion = step.Version
			if contractVersion == 0 {
				contractVersion = 1
			}
		}
		if step.TransitionsDeclared || len(step.Transitions) > 0 {
			transitionsDeclared = true
			for _, transition := range step.Transitions {
				if transition.From == report.FromState && transition.To == report.State {
					transitionAllowed = true
					requiredMilestones = append([]string(nil), transition.RequiredMilestones...)
					for _, effect := range transition.RequiredEffects {
						requiredMilestones = append(requiredMilestones, effect.Milestone)
					}
					for _, invariant := range transition.RequiredInvariants {
						requiredMilestones = append(requiredMilestones, invariant.Milestone)
					}
					for _, policy := range transition.RequiredPolicies {
						requiredMilestones = append(requiredMilestones, policy.Milestone)
					}
					break
				}
			}
		}
	}
	if !declared {
		return report, "", fmt.Errorf("workflow state is not declared for this Operation")
	}
	if contractVersion < 1 || report.ContractVersion != 0 && report.ContractVersion != contractVersion {
		return report, "", fmt.Errorf("workflow state contract version does not match the pinned definition")
	}
	report.ContractVersion = contractVersion

	if len(report.DependsOn) > 16 {
		return report, "", fmt.Errorf("workflow dependencies exceed their limit")
	}
	dependencies := append([]api.OperationWorkflowDependency(nil), report.DependsOn...)
	seenDependencies := map[string]bool{}
	for _, dependency := range dependencies {
		key := dependency.SubjectType + "\x00" + dependency.SubjectID + "\x00" + dependency.Workflow + "\x00" + dependency.InstanceID
		if api.ValidateOperationSubject(api.OperationSubject{Type: dependency.SubjectType, ID: dependency.SubjectID}) != nil || api.ValidateOperationWorkflowName(dependency.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(dependency.InstanceID) != nil || dependency.RequiredOutcomeCode != "" && (len(dependency.RequiredOutcomeCode) > 64 || !operationName.MatchString(dependency.RequiredOutcomeCode)) || seenDependencies[key] {
			return report, "", fmt.Errorf("workflow dependency requires a valid unique business reference, workflow and instance")
		}
		seenDependencies[key] = true
	}
	sort.Slice(dependencies, func(i, j int) bool {
		a, b := dependencies[i], dependencies[j]
		if a.SubjectType != b.SubjectType {
			return a.SubjectType < b.SubjectType
		}
		if a.SubjectID != b.SubjectID {
			return a.SubjectID < b.SubjectID
		}
		if a.Workflow != b.Workflow {
			return a.Workflow < b.Workflow
		}
		return a.InstanceID < b.InstanceID
	})
	report.DependsOn = dependencies
	if report.DependenciesOnly && (report.BlockersOnly || report.DeadlineOnly || report.OutcomeOnly) {
		return report, "", fmt.Errorf("choose one metadata update kind")
	}
	if report.OutcomeCode != "" || report.OutcomeDescription != "" || report.OutcomeOnly {
		if report.OutcomeCode == "" || len(report.OutcomeCode) > 64 || !operationName.MatchString(report.OutcomeCode) || report.OutcomeDescription == "" || len(report.OutcomeDescription) > 512 || !utf8.ValidString(report.OutcomeDescription) || strings.ContainsFunc(report.OutcomeDescription, func(r rune) bool { return r < 0x20 || r == 0x7f }) || !OperationWorkflowStateIsTerminal(c.Spec, report.Workflow, report.State) {
			return report, "", fmt.Errorf("workflow outcome requires a valid public code/description and a declared terminal state")
		}
	}
	if report.OutcomeOnly && (report.BlockersOnly || report.DeadlineOnly) {
		return report, "", fmt.Errorf("choose one metadata update kind")
	}
	if report.DeadlineAt != "" {
		due, err := time.Parse(time.RFC3339Nano, report.DeadlineAt)
		if err != nil || due.UTC().Year() < 1 || due.UTC().Year() > 9999 {
			return report, "", fmt.Errorf("workflow deadline must be a finite RFC3339 timestamp")
		}
		report.DeadlineAt = due.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	}
	if report.DeadlineOnly && report.BlockersOnly {
		return report, "", fmt.Errorf("choose one metadata update kind")
	}
	if len(report.Blockers) > 16 {
		return report, "", fmt.Errorf("workflow blockers exceed their limit")
	}
	blockers := append([]api.OperationWorkflowBlocker(nil), report.Blockers...)
	seenBlockers := make(map[string]bool, len(blockers))
	for i, blocker := range blockers {
		if blocker.FirstObservedAt != "" {
			first, err := time.Parse(time.RFC3339Nano, blocker.FirstObservedAt)
			if err != nil || first.UTC().Year() < 1 || first.UTC().Year() > 9999 || first.After(report.OccurredAt) {
				return report, "", fmt.Errorf("blocker first observation must be a valid timestamp at or before the report")
			}
			blockers[i].FirstObservedAt = first.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		}
		key := blocker.Operation + "\x00" + blocker.Code
		if len(blocker.Code) == 0 || len(blocker.Code) > 64 || !operationName.MatchString(blocker.Code) ||
			len(blocker.Operation) == 0 || len(blocker.Operation) > api.OperationNameMaxBytes || !operationName.MatchString(blocker.Operation) ||
			len(blocker.Description) == 0 || len(blocker.Description) > 512 || !utf8.ValidString(blocker.Description) ||
			strings.ContainsFunc(blocker.Description, func(r rune) bool { return r < 0x20 || r == 0x7f }) || seenBlockers[key] {
			return report, "", fmt.Errorf("workflow blocker has invalid public fields or duplicate target/code")
		}
		seenBlockers[key] = true
	}
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].Operation != blockers[j].Operation {
			return blockers[i].Operation < blockers[j].Operation
		}
		return blockers[i].Code < blockers[j].Code
	})
	report.Blockers = blockers

	if len(report.BlockerResolutions) > 16 {
		return report, "", fmt.Errorf("workflow blocker resolutions exceed their limit")
	}
	resolutions := append([]api.OperationWorkflowBlockerResolution(nil), report.BlockerResolutions...)
	seenResolutions := make(map[string]bool, len(resolutions))
	for _, resolution := range resolutions {
		sourceOperation, opErr := uuid.Parse(resolution.BlockerOperationID)
		sourceReport, reportErr := uuid.Parse(resolution.BlockerReportID)
		key := resolution.Operation + "\x00" + resolution.Code
		if opErr != nil || reportErr != nil || sourceOperation == uuid.Nil || sourceReport == uuid.Nil || sourceOperation.String() != resolution.BlockerOperationID || sourceReport.String() != resolution.BlockerReportID ||
			resolution.BlockerRevision < 1 || resolution.BlockerRevision > 9007199254740991 || resolution.BlockerRevision >= report.Revision ||
			resolution.Code == "" || len(resolution.Code) > 64 || !operationName.MatchString(resolution.Code) || resolution.Operation == "" || len(resolution.Operation) > api.OperationNameMaxBytes || !operationName.MatchString(resolution.Operation) ||
			resolution.Description == "" || len(resolution.Description) > 512 || !utf8.ValidString(resolution.Description) || strings.ContainsFunc(resolution.Description, func(r rune) bool { return r < 0x20 || r == 0x7f }) || seenResolutions[key] || seenBlockers[key] {
			return report, "", fmt.Errorf("workflow resolution requires valid public fields, a prior report identity/revision, and a cleared unique blocker target/code")
		}
		seenResolutions[key] = true
	}
	sort.Slice(resolutions, func(i, j int) bool {
		if resolutions[i].Operation != resolutions[j].Operation {
			return resolutions[i].Operation < resolutions[j].Operation
		}
		return resolutions[i].Code < resolutions[j].Code
	})
	report.BlockerResolutions = resolutions
	if report.BlockersOnly || report.DeadlineOnly || report.OutcomeOnly || report.DependenciesOnly {
		if report.FromState != report.State || len(report.EvidenceMilestones) > 0 {
			return report, "", fmt.Errorf("metadata-only updates must preserve state and contain no transition evidence")
		}
		requiredMilestones = nil
	}
	reconciliationSnapshot := allowReconciliation && report.FromState == "" && len(report.EvidenceMilestones) == 1
	if !reconciliationSnapshot && !report.BlockersOnly && !report.DeadlineOnly && !report.OutcomeOnly && !report.DependenciesOnly && transitionsDeclared && (!transitionAllowed || report.FromState == "") {
		return report, "", fmt.Errorf("workflow state transition is not declared for this Operation")
	}
	if !report.BlockersOnly && !report.DeadlineOnly && !report.OutcomeOnly && !report.DependenciesOnly && !transitionsDeclared && report.FromState != "" {
		return report, "", fmt.Errorf("workflow transition is not enabled for this Operation")
	}
	if len(report.EvidenceMilestones) > api.OperationWorkflowStateEvidenceMax {
		return report, "", fmt.Errorf("workflow state evidence list exceeds its limit")
	}
	evidence := append([]api.OperationWorkflowEvidenceMilestone(nil), report.EvidenceMilestones...)
	seenEvidenceIDs := make(map[string]bool, len(evidence))
	seenEvidenceNames := make(map[string]bool, len(evidence))
	for _, milestone := range evidence {
		id, parseErr := uuid.Parse(milestone.ID)
		if parseErr != nil || id == uuid.Nil || id.String() != milestone.ID || len(milestone.Name) == 0 || len(milestone.Name) > api.OperationNameMaxBytes || !operationName.MatchString(milestone.Name) ||
			seenEvidenceIDs[milestone.ID] || seenEvidenceNames[milestone.Name] {
			return report, "", fmt.Errorf("workflow state evidence contains an invalid or duplicate milestone reference")
		}
		seenEvidenceIDs[milestone.ID], seenEvidenceNames[milestone.Name] = true, true
	}
	if report.FromState == "" && len(evidence) > 0 && !reconciliationSnapshot {
		return report, "", fmt.Errorf("workflow state evidence is only valid for a declared transition")
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Name != evidence[j].Name {
			return evidence[i].Name < evidence[j].Name
		}
		return evidence[i].ID < evidence[j].ID
	})
	report.EvidenceMilestones = evidence
	for _, required := range requiredMilestones {
		if !seenEvidenceNames[required] {
			return report, "", fmt.Errorf("workflow transition is missing required milestone evidence %q", required)
		}
	}
	report.OccurredAt = report.OccurredAt.Truncate(time.Microsecond)
	// The resolved contract version is server-derived and must not invalidate
	// idempotent replays created before this field existed.
	fingerprintReport := report
	fingerprintReport.ContractVersion = 0
	raw, _ := json.Marshal(fingerprintReport)
	fingerprint, err := InputFingerprint(raw)
	return report, fingerprint, err
}
