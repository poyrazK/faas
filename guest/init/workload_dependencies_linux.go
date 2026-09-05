//go:build linux

package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// workloadDependencyState is the in-guest lifecycle surface exposed to the
// roster scheduler. The channels close once and are safe for many dependent
// workloads to await concurrently.
type workloadDependencyState struct {
	started               chan struct{}
	healthy               chan struct{}
	completedSuccessfully chan struct{}
	done                  chan struct{}

	mu  sync.Mutex
	err error
}

func newWorkloadDependencyState() *workloadDependencyState {
	return &workloadDependencyState{
		started:               make(chan struct{}),
		healthy:               make(chan struct{}),
		completedSuccessfully: make(chan struct{}),
		done:                  make(chan struct{}),
	}
}

func (s *workloadDependencyState) setResult(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	if err == nil {
		close(s.completedSuccessfully)
	}
	close(s.done)
}

func (s *workloadDependencyState) result() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// normalizeWorkloadDependencies returns a stable dependency list for every
// workload. Init workloads are implicit prerequisites of main and long-running
// sidecars, preserving the pre-dependency roster semantics.
func normalizeWorkloadDependencies(roster workloadRoster) (map[string][]api.WorkloadDependency, error) {
	if len(roster.Main.DependsOn) > api.WorkloadDependencyCapMax {
		return nil, fmt.Errorf("workload roster: main has %d dependencies; max is %d", len(roster.Main.DependsOn), api.WorkloadDependencyCapMax)
	}
	deps := map[string][]api.WorkloadDependency{"main": append([]api.WorkloadDependency(nil), roster.Main.DependsOn...)}
	known := map[string]bool{"main": true}
	for _, sc := range roster.Sidecars {
		if sc.Name == "" || sc.Name == "main" || known[sc.Name] {
			return nil, fmt.Errorf("workload roster: duplicate or reserved workload name %q", sc.Name)
		}
		known[sc.Name] = true
		if len(sc.DependsOn) > api.WorkloadDependencyCapMax {
			return nil, fmt.Errorf("workload roster: %q has %d dependencies; max is %d", sc.Name, len(sc.DependsOn), api.WorkloadDependencyCapMax)
		}
		deps[sc.Name] = append([]api.WorkloadDependency(nil), sc.DependsOn...)
	}
	for name, entries := range deps {
		seen := make(map[string]struct{}, len(entries))
		for i := range entries {
			if entries[i].Condition == "" {
				entries[i].Condition = api.WorkloadDependencyStarted
			}
			if !known[entries[i].Name] {
				return nil, fmt.Errorf("workload roster: %q depends on unknown workload %q", name, entries[i].Name)
			}
			if entries[i].Name == name {
				return nil, fmt.Errorf("workload roster: %q depends on itself", name)
			}
			if _, ok := seen[entries[i].Name]; ok {
				return nil, fmt.Errorf("workload roster: %q depends on %q more than once", name, entries[i].Name)
			}
			seen[entries[i].Name] = struct{}{}
			switch entries[i].Condition {
			case api.WorkloadDependencyStarted, api.WorkloadDependencyHealthy, api.WorkloadDependencyCompletedSuccessfully:
			default:
				return nil, fmt.Errorf("workload roster: %q has invalid dependency condition %q", name, entries[i].Condition)
			}
		}
		deps[name] = entries
	}
	for _, sc := range roster.Sidecars {
		if sc.Type != "init" {
			continue
		}
		implicit := api.WorkloadDependency{Name: sc.Name, Condition: api.WorkloadDependencyCompletedSuccessfully}
		deps["main"] = appendDependencyIfMissing(deps["main"], implicit)
		for _, candidate := range roster.Sidecars {
			if candidate.Type == "sidecar" {
				deps[candidate.Name] = appendDependencyIfMissing(deps[candidate.Name], implicit)
			}
		}
	}
	for name, entries := range deps {
		if len(entries) > api.WorkloadDependencyCapMax {
			return nil, fmt.Errorf("workload roster: %q has %d dependencies after implicit prerequisites; max is %d", name, len(entries), api.WorkloadDependencyCapMax)
		}
	}
	if err := validateDependencyCycles(deps); err != nil {
		return nil, err
	}
	return deps, nil
}

func appendDependencyIfMissing(entries []api.WorkloadDependency, dep api.WorkloadDependency) []api.WorkloadDependency {
	for _, existing := range entries {
		if existing.Name == dep.Name {
			return entries
		}
	}
	return append(entries, dep)
}

func validateDependencyCycles(deps map[string][]api.WorkloadDependency) error {
	state := make(map[string]uint8, len(deps))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("workload roster: dependency cycle involving %q", name)
		case 2:
			return nil
		}
		state[name] = 1
		for _, dep := range deps[name] {
			if err := visit(dep.Name); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

// workloadStartOrder is a stable topological order used to dispatch the
// runtime goroutines. Independent workloads can still run concurrently; the
// stable order makes logs and lifecycle events reproducible and ensures a
// newly added dependency cannot silently change declaration-order tie breaks.
func workloadStartOrder(roster workloadRoster, deps map[string][]api.WorkloadDependency) ([]string, error) {
	declaration := make([]string, 0, 1+len(roster.Sidecars))
	declaration = append(declaration, "main")
	for _, sc := range roster.Sidecars {
		declaration = append(declaration, sc.Name)
	}
	indegree := make(map[string]int, len(declaration))
	dependents := make(map[string][]string, len(declaration))
	for _, name := range declaration {
		indegree[name] = len(deps[name])
		for _, dep := range deps[name] {
			dependents[dep.Name] = append(dependents[dep.Name], name)
		}
	}
	order := make([]string, 0, len(declaration))
	used := make(map[string]bool, len(declaration))
	for len(order) < len(declaration) {
		picked := ""
		for _, name := range declaration {
			if !used[name] && indegree[name] == 0 {
				picked = name
				break
			}
		}
		if picked == "" {
			return nil, fmt.Errorf("workload roster: dependency graph has no stable start order")
		}
		used[picked] = true
		order = append(order, picked)
		for _, dependent := range dependents[picked] {
			indegree[dependent]--
		}
	}
	return order, nil
}

func waitForWorkloadDependency(ctx context.Context, dep api.WorkloadDependency, state *workloadDependencyState) error {
	var ready <-chan struct{}
	switch dep.Condition {
	case "", api.WorkloadDependencyStarted:
		ready = state.started
	case api.WorkloadDependencyHealthy:
		ready = state.healthy
	case api.WorkloadDependencyCompletedSuccessfully:
		ready = state.completedSuccessfully
	default:
		return fmt.Errorf("invalid dependency condition %q", dep.Condition)
	}
	select {
	case <-ready:
		return nil
	case <-state.done:
		if err := state.result(); err != nil {
			return fmt.Errorf("dependency failed: %w", err)
		}
		if dep.Condition == api.WorkloadDependencyCompletedSuccessfully {
			return nil
		}
		return fmt.Errorf("dependency completed before condition %q", dep.Condition)
	case <-ctx.Done():
		return ctx.Err()
	}
}
