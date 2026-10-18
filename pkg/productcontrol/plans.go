package productcontrol

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
)

func ValidProductPlanName(name string) bool { return slug.MatchString(name) }

func ValidatePlanPolicy(policy *api.ProductPlanPolicy, plans map[string]api.ProductPlan) error {
	if policy == nil {
		return nil
	}
	if policy.Transitions == nil || len(policy.Transitions) > api.ProductControlPlanPolicyMaxSources {
		return fmt.Errorf("self_service.transitions must be a bounded object")
	}
	for source, targets := range policy.Transitions {
		if !slug.MatchString(source) || targets == nil || len(targets) > api.ProductControlPlanPolicyMaxTargets {
			return fmt.Errorf("invalid self-service source %q", source)
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if !slug.MatchString(target) || seen[target] {
				return fmt.Errorf("invalid or duplicate self-service target for %q", source)
			}
			if plans != nil {
				if _, ok := plans[target]; !ok {
					return fmt.Errorf("self-service target %q is not in this catalog", target)
				}
			}
			seen[target] = true
		}
	}
	return nil
}

func PlanOffered(policy *api.ProductPlanPolicy, source, target string) bool {
	return policy != nil && slices.Contains(policy.Transitions[source], target)
}

func PlanPolicyConfiguration(c api.ProductCatalog) api.ProductPlanPolicyConfiguration {
	p := api.ProductPlanPolicy{Transitions: map[string][]string{}}
	if c.Spec.SelfService != nil {
		for source, targets := range c.Spec.SelfService.Transitions {
			p.Transitions[source] = append([]string{}, targets...)
		}
	}
	return api.ProductPlanPolicyConfiguration{Revision: c.Revision, Policy: p}
}

func PreviewPlanPolicy(c api.ProductCatalog, policy api.ProductPlanPolicy) (api.ProductPlanPolicyPreview, error) {
	spec := c.Spec
	spec.SelfService = &policy
	updated, err := Compile(spec)
	if err != nil {
		return api.ProductPlanPolicyPreview{}, err
	}
	raw, err := json.Marshal(updated.Spec)
	if err != nil || len(raw) > api.ProductControlMaxCatalogBytes {
		return api.ProductPlanPolicyPreview{}, fmt.Errorf("resulting product catalog exceeds its size limit")
	}
	return api.ProductPlanPolicyPreview{Before: PlanPolicyConfiguration(c), After: PlanPolicyConfiguration(updated), Change: api.ProductPlanPolicyChange{ExpectedHead: c.Revision, ExpectedRevision: updated.Revision, Policy: *updated.Spec.SelfService}}, nil
}
