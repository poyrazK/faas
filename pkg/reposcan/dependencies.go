package reposcan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// DependencyValidationReasons returns stable, user-facing graph errors for a
// project plan. Managed services are accepted as external dependencies: they
// are surfaced by the scanner but Gregale does not provision them, so there
// is no local workload edge to order.
func DependencyValidationReasons(workloads []Workload, managed []Managed) []string {
	managedNames := make(map[string]struct{}, len(managed))
	for _, m := range managed {
		managedNames[strings.ToLower(m.Name)] = struct{}{}
	}
	byName := make(map[string]Workload, len(workloads))
	var reasons []string
	for _, w := range workloads {
		key := strings.ToLower(strings.TrimSpace(w.Name))
		if _, exists := byName[key]; exists {
			reasons = append(reasons, fmt.Sprintf("workload %q produces an ambiguous dependency name", w.Name))
			continue
		}
		byName[key] = w
	}
	for _, w := range workloads {
		if err := api.ValidateComposeDependencyConditions(w.DependsOnConditions, w.DependsOn); err != nil {
			reasons = append(reasons, fmt.Sprintf("workload %q: %s", w.Name, err))
		}
		for name, condition := range w.DependsOnConditions {
			if condition == api.ComposeDependencyHealthy {
				if target, exists := byName[name]; exists && target.Class == ClassJob {
					reasons = append(reasons, fmt.Sprintf("workload %q: service_healthy cannot target job %q; job completion gates are unsupported", w.Name, name))
				}
				if _, managed := managedNames[name]; managed {
					reasons = append(reasons, fmt.Sprintf("workload %q: service_healthy requires a deployable project dependency; managed service %q is unsupported", w.Name, name))
				}
			}
		}
		for _, dep := range w.DependsOn {
			depKey := strings.ToLower(strings.TrimSpace(dep))
			if depKey == "" {
				continue
			}
			if depKey == strings.ToLower(w.Name) {
				reasons = append(reasons, fmt.Sprintf("workload %q cannot depend on itself", w.Name))
				continue
			}
			if _, ok := byName[depKey]; ok {
				continue
			}
			if _, ok := managedNames[depKey]; ok {
				continue
			}
			reasons = append(reasons, fmt.Sprintf("workload %q depends on unknown service %q", w.Name, dep))
		}
	}
	if len(reasons) == 0 {
		if _, err := DependencyOrder(workloads, managed); err != nil {
			reasons = append(reasons, err.Error())
		}
	}
	sort.Strings(reasons)
	return reasons
}

// DependencyOrder returns a deterministic topological order. Ties are broken
// case-insensitively by workload name, so a plan is reproducible regardless of
// map iteration order. Managed services are external nodes and do not appear
// in the returned order.
func DependencyOrder(workloads []Workload, managed []Managed) ([]string, error) {
	if reasons := dependencyReferenceReasons(workloads, managed); len(reasons) > 0 {
		return nil, fmt.Errorf("invalid workload dependencies: %s", strings.Join(reasons, "; "))
	}
	byName := make(map[string]Workload, len(workloads))
	indegree := make(map[string]int, len(workloads))
	dependents := make(map[string][]string, len(workloads))
	for _, w := range workloads {
		name := strings.ToLower(w.Name)
		byName[name] = w
		indegree[name] = 0
	}
	for _, w := range workloads {
		name := strings.ToLower(w.Name)
		seenDeps := make(map[string]struct{}, len(w.DependsOn))
		for _, dep := range w.DependsOn {
			dep = strings.ToLower(strings.TrimSpace(dep))
			if _, duplicate := seenDeps[dep]; duplicate {
				continue
			}
			seenDeps[dep] = struct{}{}
			if _, ok := byName[dep]; !ok {
				continue
			}
			indegree[name]++
			dependents[dep] = append(dependents[dep], name)
		}
	}
	ready := make([]string, 0, len(workloads))
	for name, degree := range indegree {
		if degree == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(workloads))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		order = append(order, byName[name].Name)
		for _, dependent := range dependents[name] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(workloads) {
		return nil, fmt.Errorf("invalid workload dependencies: dependency cycle detected")
	}
	return order, nil
}

// dependencyReferenceReasons contains the local reference checks shared by the
// public validator and the topological sorter.
func dependencyReferenceReasons(workloads []Workload, managed []Managed) []string {
	managedNames := make(map[string]struct{}, len(managed))
	for _, m := range managed {
		managedNames[strings.ToLower(m.Name)] = struct{}{}
	}
	byName := make(map[string]struct{}, len(workloads))
	var reasons []string
	for _, w := range workloads {
		key := strings.ToLower(strings.TrimSpace(w.Name))
		if _, exists := byName[key]; exists {
			reasons = append(reasons, fmt.Sprintf("workload %q produces an ambiguous dependency name", w.Name))
			continue
		}
		byName[key] = struct{}{}
	}
	for _, w := range workloads {
		for _, dep := range w.DependsOn {
			depKey := strings.ToLower(strings.TrimSpace(dep))
			if depKey == "" {
				continue
			}
			if depKey == strings.ToLower(w.Name) {
				reasons = append(reasons, fmt.Sprintf("workload %q cannot depend on itself", w.Name))
			} else if _, ok := byName[depKey]; !ok {
				if _, managedOK := managedNames[depKey]; !managedOK {
					reasons = append(reasons, fmt.Sprintf("workload %q depends on unknown service %q", w.Name, dep))
				}
			}
		}
	}
	sort.Strings(reasons)
	return reasons
}
