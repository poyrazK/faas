package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ RouteLifecycleApprovalStore = (*MemStore)(nil)

func copyLifecycleApproval(a api.RouteLifecycleApproval) api.RouteLifecycleApproval {
	body, _ := json.Marshal(a)
	var out api.RouteLifecycleApproval
	_ = json.Unmarshal(body, &out)
	return out
}
func (m *MemStore) ApproveRouteLifecycle(_ context.Context, accountID, appID, actor string, r api.ApproveRouteLifecycleRequest, fingerprint RouteCheckFingerprinter, validator RouteLifecycleCompatibilityValidator) (api.RouteLifecycleApproval, error) {
	var a api.RouteLifecycleApproval
	if validateLifecycleApprovalRequest(r) != nil || actor == "" || fingerprint == nil || validator == nil {
		return a, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.routePolicySnapshotLocked(accountID, appID)
	if err != nil {
		return a, err
	}
	if snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0 || !snapshot.Account.Plan.TrafficSplitAllowed() || !snapshot.Account.MayDeploy() {
		return a, ErrRouteGatePlan
	}
	gate, err := m.canaryRouteGateLocked(accountID, appID)
	if err != nil {
		return a, err
	}
	saved, err := m.savedRouteRequirementsLocked(accountID, appID)
	if err != nil {
		return a, err
	}
	removal := m.routeRemovalPolicyLocked(appID)
	before := snapshot
	if err = m.routePolicyContractLocked(&before, r.BaselineDeploymentID); err != nil {
		return a, err
	}
	if err = m.routePolicyContractLocked(&snapshot, r.CandidateDeploymentID); err != nil {
		return a, err
	}
	b, c := m.deployments[r.BaselineDeploymentID], m.deployments[r.CandidateDeploymentID]
	if !routeRemovalProductionScope(b.Scope) || !routeRemovalProductionScope(c.Scope) || !sameLifecycleScope(b.Scope, c.Scope) || !(b.Status == DeployLive && b.TrafficPercent > 0 || b.ID == removal.BaselineDeploymentID) {
		return a, &RouteLifecycleReviewBlockedError{"baseline_not_applicable_to_candidate"}
	}
	mappings, _, _ := normalizeLifecycleMappings(r.Mappings)
	if err = m.loadLifecycleSuccessorsLocked(&snapshot, r.CandidateDeploymentID, mappings); err != nil {
		return a, err
	}
	binding := lifecycleBinding(snapshot, gate, saved, removal, fingerprint)
	a, err = lifecycleApproval(before.Contract, snapshot.Contract, r, binding, actor, appID, time.Now().UTC(), validator, snapshot)
	if err != nil {
		return a, err
	}
	if m.routeLifecycleApprovals == nil {
		m.routeLifecycleApprovals = map[string]api.RouteLifecycleApproval{}
	}
	if m.lifecycleApprovalConfigurations == nil {
		m.lifecycleApprovalConfigurations = map[string]string{}
	}
	m.lifecycleApprovalConfigurations[a.ID] = lifecycleMemoryConfiguration(snapshot)
	if m.lifecycleApprovalSuccessors == nil {
		m.lifecycleApprovalSuccessors = map[string]string{}
	}
	m.lifecycleApprovalSuccessors[a.ID] = m.lifecycleSuccessorBindingLocked(a)
	m.routeLifecycleApprovals[a.ID] = copyLifecycleApproval(a)
	return a, nil
}
func (m *MemStore) GetRouteLifecycleApproval(_ context.Context, accountID, appID, id string) (api.RouteLifecycleApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.routePolicySnapshotLocked(accountID, appID); err != nil {
		return api.RouteLifecycleApproval{}, err
	}
	a, ok := m.routeLifecycleApprovals[id]
	if !ok || a.AppID != appID {
		return api.RouteLifecycleApproval{}, ErrNotFound
	}
	return copyLifecycleApproval(a), nil
}
func (m *MemStore) invalidateLifecycleApprovalsLocked(deploymentID string, at time.Time) {
	for id, a := range m.routeLifecycleApprovals {
		target := false
		for _, mapping := range a.Mappings {
			target = target || mapping.SuccessorDeploymentID == deploymentID
		}
		if a.InvalidatedAt == nil && (target || a.BaselineDeploymentID == deploymentID || a.CandidateDeploymentID == deploymentID) {
			a.InvalidatedAt = &at
			m.routeLifecycleApprovals[id] = a
		}
	}
}
