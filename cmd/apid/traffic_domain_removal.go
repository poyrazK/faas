// adr: 570
package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) deleteDomainIntent(r *http.Request, account state.Account, app state.App, domain string, activity state.OrgActivity) (int64, error) {
	if owned, ok := s.store.(state.CustomDomainRemovalOwnerStore); ok {
		prepared, prepareErr := s.prepareAppActivity(r.Context(), r, account, app, activity)
		if prepareErr == nil {
			return owned.DeleteCustomDomainForAppWithActivity(r.Context(), domain, app.ID, prepared)
		}
		return 0, owned.DeleteCustomDomainForApp(r.Context(), domain, app.ID)
	}
	if _, authoritative := s.store.(state.PublicHostPolicySnapshotStore); authoritative {
		return 0, errors.New("owner-bound domain removal is unavailable")
	}
	if mutation, ok := s.store.(state.OrgActivityDomainMutationStore); ok {
		prepared, prepareErr := s.prepareAppActivity(r.Context(), r, account, app, activity)
		if prepareErr == nil {
			return mutation.DeleteCustomDomainWithActivity(r.Context(), domain, prepared)
		}
	}
	return 0, s.store.DeleteCustomDomain(r.Context(), domain)
}

func domainRemovalWriteProblem(err error) *api.Problem {
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		// The policy exposed by detachment can belong to another account.
		// Report only a proven lower bound, with no foreign witness or count.
		minimum := aggregate.Limit + 1
		problem := api.ErrTrafficPolicyAggregateTooLarge("domain_removal", aggregate.Unit, aggregate.Limit, minimum)
		problem.Detail = fmt.Sprintf("Removing this domain would expose a traffic policy with at least %d %s, above the %d cap; the domain remains attached", minimum, aggregate.Unit, aggregate.Limit)
		return problem.WithHint("Repair the policy the removal would expose, then retry. An operator can help when that policy belongs to another account.")
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		problem := api.ErrTrafficPolicyTooComplex("domain_removal", analysis.Unit, analysis.Limit, analysis.Limit+1)
		problem.Detail = "Domain removal could not be verified within platform analysis limits; the domain remains attached"
		return problem
	}
	return api.ErrCapacity("could not delete domain")
}
