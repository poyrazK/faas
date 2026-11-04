// adr: 570
package state

import "context"

type trafficHostScopeIndex struct {
	environments map[trafficHostDomain]*trafficHostEnvironment
	overlays     map[trafficHostDomain][]int
}

func indexTrafficHostScopes(ctx context.Context, view trafficHostAnalysis) (trafficHostScopeIndex, error) {
	index := trafficHostScopeIndex{environments: make(map[trafficHostDomain]*trafficHostEnvironment), overlays: make(map[trafficHostDomain][]int)}
	for i := range view.Environments {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		environment := &view.Environments[i]
		index.environments[trafficHostDomain{App: environment.App, Environment: environment.ID}] = environment
	}
	for i, group := range view.Groups {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		if group.Environment != "" {
			key := trafficHostDomain{App: group.App, Environment: group.Environment}
			index.overlays[key] = append(index.overlays[key], i)
		}
	}
	for _, domain := range view.Domains {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		if domain.Environment != "" && index.environments[trafficHostDomain{App: domain.App, Environment: domain.Environment}] == nil {
			return index, analysisLimit("domain_environment_identity", "bindings", 0, 1)
		}
	}
	return index, nil
}

func (s trafficHostScopeIndex) totals(view trafficHostAnalysis, accepted map[int]bool, environment *trafficHostEnvironment) trafficHostTotals {
	if environment == nil {
		return environmentHostTotals(view, accepted, nil)
	}
	// Overlays match the resolved binding, rather than only the generated URL
	// used as their metadata pattern. Include them for a scoped custom host.
	withOverlay := make(map[int]bool, len(accepted))
	for i := range accepted {
		withOverlay[i] = true
	}
	for _, i := range s.overlays[trafficHostDomain{App: environment.App, Environment: environment.ID}] {
		withOverlay[i] = true
	}
	return environmentHostTotals(view, withOverlay, environment)
}

func checkTrafficHostBindings(ctx context.Context, views [2]trafficHostAnalysis, scopes [2]trafficHostScopeIndex, accepted [2]map[int]bool, bindings [2]map[trafficHostDomain]*trafficHostEnvironment) error {
	if len(bindings[1]) == 0 {
		if len(bindings[0]) != 0 {
			return nil // Removed registered URLs do not expose global discovery.
		}
		if err := checkHostTotals(scopes[0].totals(views[0], accepted[0], nil), scopes[1].totals(views[1], accepted[1], nil), ""); err != nil {
			return err
		}
		return nil
	}
	for binding, environment := range bindings[1] {
		if err := ctx.Err(); err != nil {
			return err
		}
		var prior trafficHostTotals
		if old, exists := bindings[0][binding]; exists {
			prior = scopes[0].totals(views[0], accepted[0], old)
		}
		if err := checkHostTotals(prior, scopes[1].totals(views[1], accepted[1], environment), ""); err != nil {
			return err
		}
	}
	return nil
}
