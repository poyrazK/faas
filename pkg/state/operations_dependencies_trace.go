package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	operationTraceDepthLimit      = 8
	operationTraceWorkflowLimit   = 64
	operationTraceFindingLimit    = 128
	operationTraceDependencyLimit = 256
)

type operationDependencyLookup func([]api.OperationWorkflowDependency) (map[[4]string]api.OperationWorkflowState, error)

func traceOperationWorkflowDependencies(root api.OperationWorkflowDependency, initial *api.OperationWorkflowState, lookup operationDependencyLookup) (*api.OperationWorkflowDependencyTrace, error) {
	trace := &api.OperationWorkflowDependencyTrace{Findings: make([]api.OperationWorkflowDependencyFinding, 0), DepthLimit: operationTraceDepthLimit, WorkflowLimit: operationTraceWorkflowLimit, FindingLimit: operationTraceFindingLimit, DependencyLimit: operationTraceDependencyLimit}
	cache := map[[4]string]*api.OperationWorkflowState{dependencyKey(root): initial}
	expanded := map[[4]string]bool{}
	ancestors := map[[4]string]bool{}
	type findingKey struct {
		Target                       [4]string
		Kind, Limit, RequiredOutcome string
	}
	emitted := map[findingKey]bool{}
	markLimit := func(limit string) {
		trace.Truncated = true
		for _, v := range trace.LimitsReached {
			if v == limit {
				return
			}
		}
		trace.LimitsReached = append(trace.LimitsReached, limit)
	}
	emit := func(kind, explanation, limit string, path []api.OperationWorkflowDependency, state *api.OperationWorkflowState) {
		key := findingKey{Target: dependencyKey(path[len(path)-1]), Kind: kind, Limit: limit, RequiredOutcome: path[len(path)-1].RequiredOutcomeCode}
		if emitted[key] {
			return
		}
		if len(trace.Findings) >= operationTraceFindingLimit {
			markLimit("findings")
			return
		}
		emitted[key] = true
		trace.Findings = append(trace.Findings, api.OperationWorkflowDependencyFinding{Kind: kind, Explanation: explanation, Limit: limit, Path: append([]api.OperationWorkflowDependency(nil), path...), State: state})
	}
	var walk func([]api.OperationWorkflowDependency) error
	walk = func(path []api.OperationWorkflowDependency) error {
		if len(trace.Findings) >= operationTraceFindingLimit {
			markLimit("findings")
			return nil
		}
		reference := path[len(path)-1]
		key := dependencyKey(reference)
		state := cache[key]
		status := dependencyStatus(reference, state)
		if status == "terminal" || status == "satisfied" {
			return nil
		}
		if ancestors[key] {
			emit("cycle", "This reported dependency chain returns to an earlier workflow. Verify the business rows; the trace does not establish an execution deadlock.", "", path, state)
			return nil
		}
		if expanded[key] {
			return nil
		}
		if state == nil {
			emit("state_unknown", "No current retained report exists for this workflow; its business outcome is unknown.", "", path, nil)
			expanded[key] = true
			return nil
		}
		if state.Terminal {
			if status == "outcome_mismatch" {
				emit("outcome_mismatch", "The prerequisite reports a terminal outcome different from the required outcome.", "", path, state)
			} else {
				emit("outcome_unknown", "The prerequisite is terminal but has no reported outcome matching the requested requirement.", "", path, state)
			}
			// Different incoming outcome requirements must each be examined.
			return nil
		}
		observed := false
		if len(state.Blockers) > 0 {
			emit("reported_blockers", "The application reports blockers on this workflow.", "", path, state)
			observed = true
		}
		if state.Stale {
			emit("state_stale", "The reported workflow state has passed its declared stale threshold.", "", path, state)
			observed = true
		}
		if state.Overdue {
			emit("deadline_overdue", "The application-reported business deadline has passed.", "", path, state)
			observed = true
		}
		dependencies := append([]api.OperationWorkflowDependency(nil), state.DependsOn...)
		sort.Slice(dependencies, func(i, j int) bool {
			a, b := dependencyKey(dependencies[i]), dependencyKey(dependencies[j])
			for k := range a {
				if a[k] != b[k] {
					return a[k] < b[k]
				}
			}
			return false
		})
		if len(dependencies) > 0 && len(path)-1 >= operationTraceDepthLimit {
			markLimit("depth")
			emit("trace_limit", "Deeper prerequisites were not examined because the dependency-hop limit was reached.", "depth", path, state)
			return nil
		}
		expanded[key] = true
		ancestors[key] = true
		defer delete(ancestors, key)
		missing := make([]api.OperationWorkflowDependency, 0, len(dependencies))
		scheduled := map[[4]string]bool{}
		remaining := operationTraceWorkflowLimit - len(cache)
		// Prefetch one node's bounded references; cache states independently of the
		// incoming required outcome. Shared nodes are not mistaken for cycles.
		for _, dep := range dependencies {
			depKey := dependencyKey(dep)
			if _, ok := cache[depKey]; !ok && !scheduled[depKey] && len(missing) < remaining {
				missing = append(missing, dep)
				scheduled[depKey] = true
			}
		}
		if len(missing) > 0 {
			states, err := lookup(missing)
			if err != nil {
				return err
			}
			for _, dep := range missing {
				depKey := dependencyKey(dep)
				cache[depKey] = nil
				if state, ok := states[depKey]; ok {
					copy := state
					cache[depKey] = &copy
				}
			}
		}
		unmet := false
		complete := true
		for _, dep := range dependencies {
			if len(trace.Findings) >= operationTraceFindingLimit {
				markLimit("findings")
				break
			}
			if trace.ExaminedDependencyCount >= operationTraceDependencyLimit {
				complete = false
				markLimit("dependencies")
				emit("trace_limit", "Additional prerequisite edges were not examined because the edge limit was reached.", "dependencies", path, state)
				break
			}
			trace.ExaminedDependencyCount++
			next := append(append([]api.OperationWorkflowDependency(nil), path...), dep)
			target, ok := cache[dependencyKey(dep)]
			if !ok {
				markLimit("workflows")
				emit("trace_limit", "This prerequisite was not examined because the workflow lookup limit was reached.", "workflows", next, nil)
				unmet = true
				continue
			}
			status := dependencyStatus(dep, target)
			if status == "terminal" || status == "satisfied" {
				continue
			}
			unmet = true
			if err := walk(next); err != nil {
				return err
			}
		}
		if !observed && !unmet && complete {
			emit("awaiting_application", "The workflow reports an active state without a reported blocker or unmet prerequisite. Application progress is still required.", "", path, state)
		}
		return nil
	}
	if err := walk([]api.OperationWorkflowDependency{root}); err != nil {
		return nil, err
	}
	trace.VisitedWorkflowCount = len(cache)
	sort.Strings(trace.LimitsReached)
	return trace, nil
}

func (m *MemStore) projectDependencyTraceLocked(page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, operator bool, now time.Time) {
	instance := page.WorkflowInstance
	tenant = operationDependencyImpactTenant(instance, tenant, opts, operator)
	if tenant == "" {
		return
	}
	root := api.OperationWorkflowDependency{SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Workflow: instance.Workflow, InstanceID: instance.InstanceID}
	lookup := func(dependencies []api.OperationWorkflowDependency) (map[[4]string]api.OperationWorkflowState, error) {
		synthetic := api.OperationMilestonesResponse{WorkflowInstance: &api.OperationWorkflowInstanceSnapshot{State: &api.OperationWorkflowState{DependsOn: dependencies}}}
		m.projectRelatedWorkflowsLocked(&synthetic, account, tenant, opts, false, now)
		states := map[[4]string]api.OperationWorkflowState{}
		for _, related := range synthetic.WorkflowInstance.RelatedWorkflows {
			if related.State != nil {
				state := *related.State
				if operator {
					state.PlatformTenantID = tenant
				}
				states[dependencyKey(related.Dependency)] = state
			}
		}
		return states, nil
	}
	// The memory lookup cannot fail; both projection and traversal run under the store lock.
	instance.DependencyTrace, _ = traceOperationWorkflowDependencies(root, instance.State, lookup)
}
