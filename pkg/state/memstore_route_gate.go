package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ CanaryRouteGateStore = (*MemStore)(nil)

func (m *MemStore) canaryRouteGateLocked(accountID, appID string) (api.CanaryRouteGate, error) {
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.CanaryRouteGate{}, ErrNotFound
	}
	gate, ok := m.canaryRouteGates[appID]
	if !ok {
		gate = defaultCanaryRouteGate(appID)
	}
	if gate.UpdatedAt != nil {
		updated := *gate.UpdatedAt
		gate.UpdatedAt = &updated
	}
	return gate, nil
}

func (m *MemStore) GetCanaryRouteGate(_ context.Context, accountID, appID string) (api.CanaryRouteGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.canaryRouteGateLocked(accountID, appID)
}

func (m *MemStore) SetCanaryRouteGate(_ context.Context, accountID, appID string, request api.SetCanaryRouteGateRequest) (api.CanaryRouteGate, error) {
	if err := ValidateCanaryRouteGate(request); err != nil {
		return api.CanaryRouteGate{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	gate, err := m.canaryRouteGateLocked(accountID, appID)
	if err != nil {
		return gate, err
	}
	if gate.Revision != *request.ExpectedRevision {
		return gate, ErrRouteGateRevision
	}
	if request.Mode == "enforce" {
		if plan := m.accounts[accountID].Plan; !plan.TrafficSplitAllowed() || plan.OpenAPIDocsPerDeployment() <= 0 {
			return gate, ErrRouteGatePlan
		}
		if _, err := m.savedRouteRequirementsLocked(accountID, appID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return gate, ErrRouteGateRequirements
			}
			return gate, err
		}
	}
	if gate.Mode == request.Mode {
		return gate, nil
	}
	if gate.Revision >= api.RouteRequirementsMaxRevision {
		return gate, ErrRouteGateRevision
	}
	now := time.Now().UTC()
	gate.Mode, gate.Revision, gate.UpdatedAt = request.Mode, gate.Revision+1, &now
	if m.canaryRouteGates == nil {
		m.canaryRouteGates = map[string]api.CanaryRouteGate{}
	}
	m.canaryRouteGates[appID] = gate
	return m.canaryRouteGateLocked(accountID, appID)
}

func (m *MemStore) checkCanaryRouteGateLocked(deployment Deployment, params CanaryAdvanceParams) error {
	app := m.apps[deployment.AppID]
	gate, err := m.canaryRouteGateLocked(app.AccountID, app.ID)
	if err != nil {
		return err
	}
	if gate.Mode == "report" && params.RouteGateDecision == nil {
		return nil
	}
	snapshot, err := m.routePolicySnapshotLocked(app.AccountID, app.ID)
	if err != nil {
		return err
	}
	saved, savedErr := m.savedRouteRequirementsLocked(app.AccountID, app.ID)
	if savedErr != nil && !errors.Is(savedErr, ErrNotFound) {
		return savedErr
	}
	if err := m.routePolicyContractLocked(&snapshot, deployment.ID); err != nil {
		return err
	}
	item, exists := m.automaticRouteChecks[deployment.ID]
	var result api.AutomaticRouteCheck
	unavailable := savedErr != nil || params.RouteCheckFingerprint == nil || snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0
	if exists && !unavailable {
		result, err = automaticRouteCheckView(item.Record, snapshot, saved, params.RouteCheckFingerprint)
		if err != nil {
			unavailable = true
		}
	}
	decision, refresh := decideCanaryRouteGate(gate, deployment.ID, result, !exists, unavailable)
	if refresh && savedErr == nil && !unavailable {
		m.enqueueAutomaticRouteCheckLocked(app.ID, deployment.ID, false)
		decision.CheckQueued = true
	}
	if err := m.lifecycleGateLocked(snapshot, deployment, &decision, time.Now().UTC(), saved, params.RouteCheckFingerprint); err != nil {
		return err
	}
	if decision.Status == "blocked" && routeRemovalProductionScope(deployment.Scope) {
		m.recordLifecycleHistoryLocked(deployment, decision, false, "blocked")
	}
	return requireCanaryRouteGate(gate, decision, params.RouteGateDecision)
}
