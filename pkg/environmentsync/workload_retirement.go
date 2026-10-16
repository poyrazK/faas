package environmentsync

import "strings"

// Removing membership retires the environment's execution lane, not the
// stored configuration of its app. A presence owner supplies the boundary;
// field-only managers cannot retire a workload they did not adopt.
func planWorkloadRetirements(plan *Plan, desired DesiredState, owners map[string]Ownership, overrides map[string]Override, options PlanOptions) {
	retiring := map[string]bool{}
	for _, owner := range owners {
		if owner.Manager != options.Manager || owner.Path != "presence" || !strings.HasPrefix(owner.Resource, "workload/") {
			continue
		}
		if _, wanted := desired.Definition.Workloads[strings.TrimPrefix(owner.Resource, "workload/")]; wanted {
			continue
		}
		retiring[owner.Resource] = true
		for _, other := range owners {
			if other.Resource == owner.Resource && other.Manager != options.Manager {
				plan.BlockingReasons = append(plan.BlockingReasons, owner.Resource+": retirement conflicts with another field manager")
				break
			}
		}

		if desired.Definition.WorkloadPruningPolicy != "retain" {
			plan.BlockingReasons = append(plan.BlockingReasons, owner.Key()+": workload retirement requires workload_pruning_policy retain")
		}
		// An active field override blocks retirement of the whole workload;
		// preserving only that field cannot preserve its running workload.
		for _, override := range overrides {
			if override.Resource == owner.Resource {
				plan.BlockingReasons = append(plan.BlockingReasons, owner.Resource+": workload retirement conflicts with an active override")
			}
		}
	}
	for i := range plan.Changes {
		change := &plan.Changes[i]
		if !retiring[change.Resource] {
			continue
		}
		owner, owned := owners[(Field{Resource: change.Resource, Path: change.Path}).Key()]
		if !owned || owner.Manager != options.Manager {
			continue
		}
		if change.Action == "overridden" {
			continue // retain the expiry wakeup and the reviewed override detail
		}
		if change.Path == "presence" {
			change.Reason = "retire this environment's workload; retain its identity, configuration and accepted work"
			continue
		}
		change.Action, change.After, change.Reason = "keep", change.Before, "retained with the retired workload"
	}
}
