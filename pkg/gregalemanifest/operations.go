package gregalemanifest

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// Operation schemas are JSON files relative to the selected source manifest.
// Deploy bundles their contents; serving never reads source files or URLs.
type Operation struct {
	Milestones             map[string]string         `yaml:"milestones,omitempty" toml:"milestones"`
	Subject                *api.OperationSubjectSpec `yaml:"subject,omitempty" toml:"subject"`
	App                    string                    `yaml:"app,omitempty" toml:"app"`
	Name                   string                    `yaml:"name" toml:"name"`
	Method                 string                    `yaml:"method" toml:"method"`
	Path                   string                    `yaml:"path" toml:"path"`
	Owner                  string                    `yaml:"owner" toml:"owner"`
	InputSchema            string                    `yaml:"input_schema" toml:"input_schema"`
	OutputSchema           string                    `yaml:"output_schema" toml:"output_schema"`
	ProgressStages         []string                  `yaml:"progress_stages" toml:"progress_stages"`
	CompletionWebhookID    string                    `yaml:"completion_webhook_id,omitempty" toml:"completion_webhook_id"`
	Recovery               string                    `yaml:"recovery,omitempty" toml:"recovery"`
	HTTPTransactionVersion int                       `yaml:"http_transaction_version,omitempty" toml:"http_transaction_version"`
}

// OperationWorkflow declares a read-only business process assembled from
// milestone facts reported by one or more customer Operations.
type OperationWorkflow struct {
	App             string                              `yaml:"app,omitempty" toml:"app"`
	Name            string                              `yaml:"name" toml:"name"`
	Title           string                              `yaml:"title" toml:"title"`
	Version         int                                 `yaml:"version,omitempty" toml:"version"`
	States          []string                            `yaml:"states,omitempty" toml:"states"`
	TerminalStates  []string                            `yaml:"terminal_states,omitempty" toml:"terminal_states"`
	StateStaleAfter map[string]string                   `yaml:"state_stale_after,omitempty" toml:"state_stale_after"`
	Transitions     []OperationWorkflowTransitionSource `yaml:"transitions,omitempty" toml:"transitions"`
	Steps           []OperationWorkflowStepSource       `yaml:"steps" toml:"steps"`
}

type OperationWorkflowTransitionSource struct {
	From                 string                                      `yaml:"from" toml:"from"`
	To                   string                                      `yaml:"to" toml:"to"`
	Operation            string                                      `yaml:"operation,omitempty" toml:"operation"`
	RequiresDependencies *[]string                                   `yaml:"requires_dependencies,omitempty" toml:"requires_dependencies"`
	RequiresEffects      []api.OperationWorkflowEffectRequirement    `yaml:"requires_effects,omitempty" toml:"requires_effects"`
	RequiresInvariants   []api.OperationWorkflowInvariantRequirement `yaml:"requires_invariants,omitempty" toml:"requires_invariants"`
	RequiresPolicies     []api.OperationWorkflowPolicyRequirement    `yaml:"requires_policies,omitempty" toml:"requires_policies"`
	RequiresMilestones   []string                                    `yaml:"requires_milestones,omitempty" toml:"requires_milestones"`
}

type OperationWorkflowStepSource struct {
	Reconciliation bool   `yaml:"reconciliation,omitempty" toml:"reconciliation"`
	Name           string `yaml:"name" toml:"name"`
	Label          string `yaml:"label" toml:"label"`
	Operation      string `yaml:"operation" toml:"operation"`
	Milestone      string `yaml:"milestone" toml:"milestone"`
	InstanceIDFrom string `yaml:"instance_id_from" toml:"instance_id_from"`
	Position       int    `yaml:"position" toml:"position"`
}

func (o Operation) specification() api.OperationDefinitionSpec {
	return api.OperationDefinitionSpec{Subject: o.Subject, Name: o.Name, Method: o.Method, Path: o.Path, Owner: o.Owner,
		ProgressStages: append([]string(nil), o.ProgressStages...), CompletionWebhookID: o.CompletionWebhookID, Recovery: o.Recovery, HTTPTransactionVersion: o.HTTPTransactionVersion}
}

func (m *Manifest) validateOperations(plan api.Plan) error {
	limits := api.MustLimitsFor(plan).Operations
	if len(m.Operations) == 0 {
		return nil
	}
	if !limits.Allowed {
		return fmt.Errorf("operations exceed the %s plan definition limit", plan)
	}
	names, routes := map[string]bool{}, map[string]bool{}
	counts := map[string]int{}
	for i, o := range m.Operations {
		if o.App != "" && !isDNSSafeSlug(o.App) {
			return fmt.Errorf("operations[%d].app must be an app slug", i)
		}
		files := []string{o.InputSchema, o.OutputSchema}
		for _, file := range o.Milestones {
			files = append(files, file)
		}
		for _, file := range files {
			if file == "" || len(file) > api.OperationPathMaxBytes || path.IsAbs(file) || path.Clean(file) != file || file == "." || strings.ContainsAny(file, "\\:\x00\r\n") || strings.HasPrefix(file, "../") {
				return fmt.Errorf("operations[%d]: schemas must be source-local relative JSON files", i)
			}
		}
		spec := o.specification()
		spec.InputSchema, spec.OutputSchema = []byte(`true`), []byte(`true`)
		spec.Milestones = make(map[string]json.RawMessage, len(o.Milestones))
		for name := range o.Milestones {
			spec.Milestones[name] = []byte(`true`)
		}
		contract, err := operations.Compile(spec, limits)
		if err != nil {
			return fmt.Errorf("operations[%d]: %w", i, err)
		}
		name, route := o.App+"/"+o.Name, o.App+"/"+contract.Spec.Method+"/"+contract.Spec.Path
		if names[name] || routes[route] {
			return fmt.Errorf("operations[%d]: names and HTTP targets must be unique within an app", i)
		}
		names[name] = true
		routes[route] = true
		counts[o.App]++
	}
	for app, count := range counts {
		if app != "" {
			count += counts[""]
		}
		if count > limits.DefinitionsPerApp {
			return fmt.Errorf("operations exceed the %s plan definition limit for app %q", plan, app)
		}
	}
	return nil
}

// ResolveOperations uses an archive-owned reader. It resolves only declarations
// for this app, so another workload cannot supply its handler's schema bundle.
func (m *Manifest) ResolveOperations(slug string, plan api.Plan, read func(string, int) ([]byte, error)) error {
	m.ResolvedOperations = nil
	if err := m.validateOperations(plan); err != nil {
		return err
	}
	workflowSteps, err := m.resolveOperationWorkflowSteps(slug)
	if err != nil {
		return err
	}
	limits := api.MustLimitsFor(plan).Operations
	var resolved []api.OperationDefinitionSpec
	names, routes := map[string]bool{}, map[string]bool{}
	for _, o := range m.Operations {
		if o.App != "" && o.App != slug {
			continue
		}
		spec := o.specification()
		var err error
		spec.InputSchema, err = read(o.InputSchema, limits.SchemaBytes)
		if err != nil {
			return fmt.Errorf("operation %q input schema: %w", o.Name, err)
		}
		spec.OutputSchema, err = read(o.OutputSchema, limits.SchemaBytes)
		if err != nil {
			return fmt.Errorf("operation %q output schema: %w", o.Name, err)
		}
		spec.Milestones = make(map[string]json.RawMessage, len(o.Milestones))
		for name, file := range o.Milestones {
			schema, readErr := read(file, limits.SchemaBytes)
			if readErr != nil {
				return fmt.Errorf("operation %q milestone %q schema: %w", o.Name, name, readErr)
			}
			spec.Milestones[name] = schema
		}
		contract, err := operations.Compile(spec, limits)
		if err != nil {
			return fmt.Errorf("operation %q: %w", o.Name, err)
		}
		if steps := workflowSteps[o.Name]; len(steps) > 0 {
			contract.Spec.WorkflowSteps = steps
			contract, err = operations.Compile(contract.Spec, limits)
			if err != nil {
				return fmt.Errorf("operation %q workflow steps: %w", o.Name, err)
			}
		}
		route := contract.Spec.Method + " " + contract.Spec.Path
		if names[contract.Spec.Name] || routes[route] {
			return fmt.Errorf("operation names and HTTP targets must be unique in app %q", slug)
		}
		names[contract.Spec.Name] = true
		routes[route] = true
		resolved = append(resolved, contract.Spec)
	}
	m.ResolvedOperations = resolved
	return nil
}

func validWorkflowDisplayText(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t")
}

func (m *Manifest) resolveOperationWorkflowSteps(slug string) (map[string][]api.OperationWorkflowSpec, error) {
	result := make(map[string][]api.OperationWorkflowSpec)
	if len(m.OperationWorkflows) == 0 {
		return result, nil
	}
	operationsByName := make(map[string]Operation)
	for _, operation := range m.Operations {
		if operation.App != "" && operation.App != slug {
			continue
		}
		if _, exists := operationsByName[operation.Name]; exists {
			return nil, fmt.Errorf("operation workflows require unique operation names for app %q", slug)
		}
		operationsByName[operation.Name] = operation
	}
	seenWorkflows := make(map[string]bool)
	workflowCount := 0
	for _, workflow := range m.OperationWorkflows {
		if workflow.App != "" && !isDNSSafeSlug(workflow.App) {
			return nil, fmt.Errorf("operation workflow app must be an app slug")
		}
		if workflow.App != "" && workflow.App != slug {
			continue
		}
		workflowCount++
		if workflowCount > api.OperationWorkflowsMaxPerApp {
			return nil, fmt.Errorf("operation workflows exceed the per-app limit")
		}
		if !isDNSSafeSlug(workflow.Name) || !validWorkflowDisplayText(workflow.Title, api.OperationWorkflowTitleMaxBytes) || seenWorkflows[workflow.Name] {
			return nil, fmt.Errorf("operation workflow name and title must be valid and unique within app %q", slug)
		}
		version := workflow.Version
		if version == 0 {
			// Existing manifests without a contract version are version 1.
			version = 1
		}
		if version < 1 || version > api.OperationWorkflowContractVersionMax {
			return nil, fmt.Errorf("operation workflow %q has an invalid contract version", workflow.Name)
		}
		seenWorkflows[workflow.Name] = true
		if len(workflow.Steps) == 0 || len(workflow.Steps) > api.OperationWorkflowStepsMax {
			return nil, fmt.Errorf("operation workflow %q must declare 1 to %d steps", workflow.Name, api.OperationWorkflowStepsMax)
		}
		if len(workflow.States) > api.OperationWorkflowStatesMax {
			return nil, fmt.Errorf("operation workflow %q declares too many states", workflow.Name)
		}
		states := append([]string(nil), workflow.States...)
		stateSet := make(map[string]bool, len(states))
		for _, state := range states {
			if api.ValidateOperationWorkflowStateName(state) != nil || stateSet[state] {
				return nil, fmt.Errorf("operation workflow %q has an invalid or duplicate state", workflow.Name)
			}
			stateSet[state] = true
		}
		sort.Strings(states)
		if len(workflow.TerminalStates) > api.OperationWorkflowStatesMax {
			return nil, fmt.Errorf("operation workflow %q declares too many terminal states", workflow.Name)
		}
		terminalStates := append([]string(nil), workflow.TerminalStates...)
		terminalSet := make(map[string]bool, len(terminalStates))
		for _, state := range terminalStates {
			if api.ValidateOperationWorkflowStateName(state) != nil || !stateSet[state] || terminalSet[state] {
				return nil, fmt.Errorf("operation workflow %q has an invalid, duplicate, or undeclared terminal state", workflow.Name)
			}
			terminalSet[state] = true
		}
		sort.Strings(terminalStates)
		if len(workflow.StateStaleAfter) > api.OperationWorkflowStatesMax {
			return nil, fmt.Errorf("operation workflow %q declares too many state staleness thresholds", workflow.Name)
		}
		stateStaleAfter := make(map[string]int64, len(workflow.StateStaleAfter))
		for state, durationText := range workflow.StateStaleAfter {
			duration, err := time.ParseDuration(durationText)
			seconds := int64(duration / time.Second)
			if err != nil || !stateSet[state] || terminalSet[state] || duration < time.Second || duration%time.Second != 0 || seconds > api.OperationWorkflowStateStaleAfterMaxSeconds {
				return nil, fmt.Errorf("operation workflow %q has an invalid state_stale_after entry for %q", workflow.Name, state)
			}
			stateStaleAfter[state] = seconds
		}
		if len(workflow.Transitions) > api.OperationWorkflowTransitionsMax {
			return nil, fmt.Errorf("operation workflow %q declares too many transitions", workflow.Name)
		}
		stepOperations := make(map[string]bool)
		for _, step := range workflow.Steps {
			stepOperations[step.Operation] = true
		}
		transitionTargets := make(map[string][]api.OperationWorkflowTransition)
		for transitionIndex, transition := range workflow.Transitions {
			if api.ValidateOperationWorkflowStateName(transition.From) != nil || api.ValidateOperationWorkflowStateName(transition.To) != nil ||
				!stateSet[transition.From] || !stateSet[transition.To] || terminalSet[transition.From] {
				return nil, fmt.Errorf("operation workflow %q has an invalid transition", workflow.Name)
			}
			if transition.Operation != "" {
				if !isDNSSafeSlug(transition.Operation) || !stepOperations[transition.Operation] {
					return nil, fmt.Errorf("operation workflow %q transition references an operation outside its steps", workflow.Name)
				}
				if _, exists := operationsByName[transition.Operation]; !exists {
					return nil, fmt.Errorf("operation workflow %q transition references unknown operation %q", workflow.Name, transition.Operation)
				}
			} else if len(transition.RequiresMilestones) > 0 || len(transition.RequiresPolicies) > 0 || transition.RequiresDependencies != nil || len(transition.RequiresInvariants) > 0 || len(transition.RequiresEffects) > 0 {
				return nil, fmt.Errorf("operation workflow %q transition evidence requires an operation target", workflow.Name)
			}
			if len(transition.RequiresMilestones) > api.OperationWorkflowTransitionEvidenceMax {
				return nil, fmt.Errorf("operation workflow %q transition evidence list exceeds its limit", workflow.Name)
			}
			required := append([]string(nil), transition.RequiresMilestones...)
			sort.Strings(required)
			for i, name := range required {
				if !isDNSSafeSlug(name) || i > 0 && required[i-1] == name {
					return nil, fmt.Errorf("operation workflow %q transition has invalid or duplicate required milestones", workflow.Name)
				}
			}
			if transition.Operation != "" {
				operation := operationsByName[transition.Operation]
				for _, name := range required {
					if operation.HTTPTransactionVersion != api.OperationHTTPTransactionVersion || operation.Milestones[name] == "" {
						return nil, fmt.Errorf("operation workflow %q transition requires an undeclared transaction-backed milestone", workflow.Name)
					}
				}
			}
			// A global edge applies to every participating Operation, so it may not
			// overlap a scoped edge for the same pair.
			for _, prior := range workflow.Transitions[:transitionIndex] {
				if prior.From == transition.From && prior.To == transition.To &&
					(prior.Operation == "" || transition.Operation == "" || prior.Operation == transition.Operation) {
					return nil, fmt.Errorf("operation workflow %q has a duplicate transition", workflow.Name)
				}
			}
			apiTransition := api.OperationWorkflowTransition{From: transition.From, To: transition.To, RequiredMilestones: required, RequiredPolicies: transition.RequiresPolicies, RequiredInvariants: transition.RequiresInvariants, RequiredEffects: transition.RequiresEffects, RequiredDependencyWorkflows: transition.RequiresDependencies}
			if transition.Operation == "" {
				for operationName := range stepOperations {
					transitionTargets[operationName] = append(transitionTargets[operationName], apiTransition)
				}
			} else {
				transitionTargets[transition.Operation] = append(transitionTargets[transition.Operation], apiTransition)
			}
		}
		for _, transitions := range transitionTargets {
			sort.Slice(transitions, func(i, j int) bool {
				if transitions[i].From != transitions[j].From {
					return transitions[i].From < transitions[j].From
				}
				return transitions[i].To < transitions[j].To
			})
		}
		seenNames, seenPositions, seenBindings := map[string]bool{}, map[int]bool{}, map[string]bool{}
		for _, step := range workflow.Steps {
			if !isDNSSafeSlug(step.Name) || !validWorkflowDisplayText(step.Label, api.OperationWorkflowLabelMaxBytes) ||
				!isDNSSafeSlug(step.Operation) || !isDNSSafeSlug(step.Milestone) || step.Position < 1 || step.Position > api.OperationWorkflowStepsMax ||
				seenNames[step.Name] || seenPositions[step.Position] {
				return nil, fmt.Errorf("operation workflow %q has an invalid or duplicate step", workflow.Name)
			}
			if err := operations.ValidateOperationWorkflowInstancePointer(step.InstanceIDFrom); err != nil {
				return nil, fmt.Errorf("operation workflow %q step %q must declare a valid instance_id_from JSON Pointer", workflow.Name, step.Name)
			}
			operation, exists := operationsByName[step.Operation]
			if !exists {
				return nil, fmt.Errorf("operation workflow %q references unknown operation %q for app %q", workflow.Name, step.Operation, slug)
			}
			if operation.HTTPTransactionVersion != api.OperationHTTPTransactionVersion || operation.Milestones[step.Milestone] == "" {
				return nil, fmt.Errorf("operation workflow %q step %q must reference a declared transaction-backed milestone", workflow.Name, step.Name)
			}
			binding := step.Operation + "/" + step.Milestone
			if seenBindings[binding] {
				return nil, fmt.Errorf("operation workflow %q maps one milestone to multiple steps", workflow.Name)
			}
			seenNames[step.Name], seenPositions[step.Position], seenBindings[binding] = true, true, true
			result[step.Operation] = append(result[step.Operation], api.OperationWorkflowSpec{
				Workflow: workflow.Name, Title: workflow.Title, Version: workflow.Version, States: states, TerminalStates: terminalStates, StateStaleAfterSeconds: stateStaleAfter,
				Transitions: transitionTargets[step.Operation], TransitionsDeclared: len(workflow.Transitions) > 0 && len(transitionTargets[step.Operation]) == 0, Step: step.Name, Label: step.Label,
				AllowReconciliation: step.Reconciliation, Milestone: step.Milestone, InstanceIDFrom: step.InstanceIDFrom, Position: step.Position,
			})
		}
	}
	for operationName := range result {
		if len(result[operationName]) > api.OperationWorkflowStepsMaxPerOperation {
			return nil, fmt.Errorf("operation %q exceeds the workflow-step limit", operationName)
		}
		// Stable ordering makes the immutable revision independent of YAML/TOML order.
		steps := result[operationName]
		for i := 1; i < len(steps); i++ {
			for j := i; j > 0 && (steps[j].Workflow < steps[j-1].Workflow || steps[j].Workflow == steps[j-1].Workflow && steps[j].Position < steps[j-1].Position); j-- {
				steps[j], steps[j-1] = steps[j-1], steps[j]
			}
		}
		result[operationName] = steps
	}
	return result, nil
}
