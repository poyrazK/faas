// adr: 570
package state

import "context"

// Deleting an environment also removes its environment-bound custom domains
// (PostgreSQL cascades them) and its edge policies. A removed domain can
// expose another owner's wildcard fallback, so the cascade is analyzed
// before anything is deleted.
func (m *MemStore) checkMemTrafficEnvironmentRemovalLocked(ctx context.Context, accountID, projectID, slug, environmentID string) error {
	change := memTrafficPolicyChange{Environments: map[string]ProjectEnvironment{environmentID: {}},
		Domains: make(map[string]CustomDomain), Policies: make(map[string]ProjectEnvironmentEdgePolicy)}
	for id, domain := range m.domains {
		if err := ctx.Err(); err != nil {
			return err
		}
		if domain.EnvironmentID == environmentID {
			change.Domains[id] = CustomDomain{}
		}
	}
	for key, policy := range m.projectEnvironmentEdgePolicies {
		if err := ctx.Err(); err != nil {
			return err
		}
		if policy.ProjectID == projectID && policy.EnvironmentSlug == slug {
			change.Policies[key] = ProjectEnvironmentEdgePolicy{}
		}
	}
	return appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, accountID, nil, "", change))
}
