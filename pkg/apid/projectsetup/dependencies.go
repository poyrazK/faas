package projectsetup

import "github.com/onebox-faas/faas/pkg/reposcan"

// DependencyReferences retain source-reviewed names and classes without adding
// unselected services to reconciliation, quotas or deployment admission.
func DependencyReferences(source, selected []reposcan.Workload) []reposcan.Workload {
	chosen := map[string]bool{}
	for _, w := range selected {
		chosen[w.Name] = true
	}
	var references []reposcan.Workload
	for _, w := range source {
		if !chosen[w.Name] {
			references = append(references, reposcan.Workload{Name: w.Name, Class: w.Class})
		}
	}
	return references
}
