package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) lifecycleGateLocked(snapshot RoutePolicySnapshot, deployment Deployment, decision *api.RouteGateDecision, at time.Time, saved api.SavedRouteRequirements, fingerprint RouteCheckFingerprinter) error {
	appendLifecycleGateFindings(decision, snapshot.Contract, snapshot.Contract, at, false)
	binding := lifecycleBinding(snapshot, api.CanaryRouteGate{Revision: decision.Revision}, saved, m.routeRemovalPolicyLocked(snapshot.App.ID), fingerprint)
	ids := []string{}
	policy := m.routeRemovalPolicies[snapshot.App.ID]
	for id, baseline := range m.deployments {
		sameScope := normalizedDeploymentScope(baseline.Scope) == normalizedDeploymentScope(deployment.Scope) || routeRemovalProductionScope(baseline.Scope) && routeRemovalProductionScope(deployment.Scope)
		if id != deployment.ID && baseline.AppID == snapshot.App.ID && sameScope && (baseline.Status == DeployLive && baseline.TrafficPercent > 0 || id == policy.BaselineDeploymentID) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		baseline := RoutePolicySnapshot{Account: snapshot.Account, App: snapshot.App}
		if err := m.routePolicyContractLocked(&baseline, id); err != nil {
			return err
		}
		reviewed := false
		var selected api.RouteLifecycleApproval
		for _, receipt := range m.routeLifecycleApprovals {
			if receipt.AppID == snapshot.App.ID && m.lifecycleSuccessorCurrentLocked(receipt) && matchingLifecycleApproval(receipt, binding, baseline.Contract, snapshot.Contract, at) && (!reviewed || receipt.ApprovedAt.After(selected.ApprovedAt)) {
				selected = receipt
				reviewed = true
			}
		}
		if reviewed {
			decision.LifecycleApprovalIDs = append(decision.LifecycleApprovalIDs, selected.ID)
		}
		appendLifecycleGateFindings(decision, baseline.Contract, snapshot.Contract, at, reviewed)
	}
	return nil
}
