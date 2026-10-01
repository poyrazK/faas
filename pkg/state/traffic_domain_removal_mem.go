// adr: 375
package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) checkMemTrafficDomainRemovalLocked(ctx context.Context, domain string) error {
	change := memTrafficPolicyChange{Domains: map[string]CustomDomain{domain: {}}}
	var before, after []trafficDomainClaim
	var globalBefore, globalAfter trafficHostAnalysis
	if err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		before, err = m.memTrafficDomainClaimsLocked(bounded, memTrafficPolicyChange{})
		if err != nil {
			return err
		}
		after, err = m.memTrafficDomainClaimsLocked(bounded, change)
		return err
	}); err != nil {
		return err
	}
	if err := globalTrafficPolicyError(boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		globalBefore, err = m.readMemTrafficHostAnalysisLocked(bounded, "", memTrafficPolicyChange{GlobalRoutes: true})
		if err != nil {
			return err
		}
		globalChange := change
		globalChange.GlobalRoutes = true
		globalAfter, err = m.readMemTrafficHostAnalysisLocked(bounded, "", globalChange)
		if err != nil {
			return err
		}
		return checkTrafficHostAnalysis(bounded, globalBefore, globalAfter)
	})); err != nil {
		return err
	}
	owners := make(map[string]bool)
	for _, account := range trafficDomainRemovalAccounts(before, domain) {
		owners[account] = true
	}
	for _, rule := range m.edgeRules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rule.Enabled && rule.Kind == EdgeRuleKindRoute {
			owners[rule.AccountID] = true
		}
	}
	if len(owners) > api.TrafficPolicyMaxAnalysisInputs {
		return analysisLimit("inputs", "owners", api.TrafficPolicyMaxAnalysisInputs, int64(len(owners)))
	}
	accounts := make([]string, 0, len(owners))
	for account := range owners {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	return boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		for _, account := range accounts {
			view, err := m.readMemTrafficHostAnalysisLocked(bounded, account, memTrafficPolicyChange{})
			if err != nil {
				return err
			}
			if err := checkTrafficDomainRemovalOwner(bounded, view, before, after, account, globalBefore, globalAfter); err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *MemStore) deleteTrafficCustomDomainLocked(ctx context.Context, domain, expectedApp string, activity *OrgActivity) (int64, error) {
	claim, found := m.domains[domain]
	if !found || expectedApp != "" && claim.AppID != expectedApp {
		return 0, ErrNotFound
	}
	if err := m.checkMemTrafficDomainRemovalLocked(ctx, domain); err != nil {
		return 0, err
	}
	if err := m.deleteCustomDomainLocked(domain); err != nil {
		return 0, err
	}
	if activity == nil {
		return 0, nil
	}
	return m.enqueueOrgActivityOutboxLocked(*activity), nil
}

func (m *MemStore) DeleteCustomDomainForApp(ctx context.Context, domain, app string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.deleteTrafficCustomDomainLocked(ctx, domain, app, nil)
	return err
}

func (m *MemStore) DeleteCustomDomainForAppWithActivity(ctx context.Context, domain, app string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteTrafficCustomDomainLocked(ctx, domain, app, &entry)
}
