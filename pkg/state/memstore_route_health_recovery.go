package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/routehealth"
)

var _ RouteHealthRecoveryStore = (*MemStore)(nil)

func (m *MemStore) RecoverCanaryRouteHealth(_ context.Context, accountID, appID, deploymentID string, expectedStep int) (RouteHealthRecoveryResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	gate, err := m.routeHealthGateLocked(accountID, appID)
	if err != nil {
		return RouteHealthRecoveryResult{}, err
	}
	d, ok := m.deployments[deploymentID]
	result := RouteHealthRecoveryResult{Deployment: d}
	if !ok || d.AppID != appID {
		return result, ErrNotFound
	}
	if err := validateRouteHealthRecoveryCandidate(d, expectedStep); err != nil {
		return result, err
	}
	if !m.safeReleaseWorkerLeaseUntil.After(time.Now()) {
		return result, ErrSafeReleaseLeaseUnavailable
	}
	if gate.Mode != "enforce" || gate.OnRegression != "abort" || !m.accounts[accountID].MayDeploy() {
		return result, nil
	}
	report, err := m.routeHealthReportLocked(accountID, appID, deploymentID)
	if err != nil {
		return result, err
	}
	decision := routehealth.Decision(report)
	result.Decision = &decision
	return result, nil
}
