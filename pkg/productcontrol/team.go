package productcontrol

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func ValidateTeamPolicyShape(p *api.ProductTeamPolicy) error {
	if p == nil {
		return nil
	}
	if p.Delegations == nil || len(p.Delegations) > api.ProductControlAccessMaxRoles {
		return fmt.Errorf("team.delegations must be a bounded object")
	}
	for actor, targets := range p.Delegations {
		if !ValidAccessRole(actor) || targets == nil || len(targets) > api.ProductControlAccessMaxRoles {
			return fmt.Errorf("invalid team delegation for %q", actor)
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if !ValidAccessRole(target) || seen[target] {
				return fmt.Errorf("invalid or duplicate delegated role for %q", actor)
			}
			seen[target] = true
		}
	}
	return nil
}

func ValidateTeamPolicy(p *api.ProductTeamPolicy, access *api.ProductAccessPolicy) error {
	if err := ValidateTeamPolicyShape(p); err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	if err := ValidateAccess(access); err != nil {
		return err
	}
	roles := DefaultAccess()
	if access != nil {
		roles = *access
	}
	for actor, targets := range p.Delegations {
		if _, ok := roles.Roles[actor]; !ok {
			return fmt.Errorf("team manager role %q is not in the product access policy", actor)
		}
		for _, target := range targets {
			if _, ok := roles.Roles[target]; !ok {
				return fmt.Errorf("delegated role %q is not in the product access policy", target)
			}
		}
	}
	return nil
}
func TeamRoleAllowed(p *api.ProductTeamPolicy, actor, target string) bool {
	return p != nil && slices.Contains(p.Delegations[actor], target)
}
func TeamPolicyConfiguration(c api.ProductCatalog) api.ProductTeamPolicyConfiguration {
	p := api.ProductTeamPolicy{Delegations: map[string][]string{}}
	if c.Spec.Team != nil {
		for actor, roles := range c.Spec.Team.Delegations {
			p.Delegations[actor] = append([]string{}, roles...)
		}
	}
	return api.ProductTeamPolicyConfiguration{Revision: c.Revision, Policy: p}
}
func PreviewTeamPolicy(c api.ProductCatalog, p api.ProductTeamPolicy) (api.ProductTeamPolicyPreview, error) {
	spec := c.Spec
	spec.Team = &p
	updated, err := Compile(spec)
	if err != nil {
		return api.ProductTeamPolicyPreview{}, err
	}
	raw, err := json.Marshal(updated.Spec)
	if err != nil || len(raw) > api.ProductControlMaxCatalogBytes {
		return api.ProductTeamPolicyPreview{}, fmt.Errorf("resulting product catalog exceeds its size limit")
	}
	return api.ProductTeamPolicyPreview{Before: TeamPolicyConfiguration(c), After: TeamPolicyConfiguration(updated), Change: api.ProductTeamPolicyChange{ExpectedHead: c.Revision, ExpectedRevision: updated.Revision, Policy: *updated.Spec.Team}}, nil
}
