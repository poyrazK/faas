package state

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ RuntimeUpgradeVerificationStore = (*MemStore)(nil)
var _ RuntimeUpgradeGatewayStore = (*MemStore)(nil)

func (m *MemStore) RecordRuntimeUpgradeGateway(_ context.Context, appID, sessionID, candidateID string) error {
	if validateRuntimeAppEnvIDs(appID, sessionID, candidateID) != nil {
		return ErrInvalidArgument
	}
	sessionID = uuid.MustParse(sessionID).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	cutover, ok := m.runtimeUpgradeCutovers[candidateID]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	var deps []Deployment
	for _, d := range m.deployments {
		deps = append(deps, d)
	}
	candidate, serving := m.deployments[candidateID], m.deployments[cutover.ServingDeploymentID]
	if candidate.AppID != appID || m.apps[appID].Status != AppActive || !runtimeUpgradeActivatedDeployments(deps, candidate, serving) {
		return ErrConflict
	}
	if m.runtimeUpgradeGatewayReceipts == nil {
		m.runtimeUpgradeGatewayReceipts = map[string]runtimeUpgradeGatewayReceipt{}
	}
	count := 0
	for key, r := range m.runtimeUpgradeGatewayReceipts {
		if len(key) > len(appID) && key[:len(appID)] == appID {
			if now.Sub(r.InstalledAt) > api.RuntimeUpgradeGatewayReceiptMaxAge {
				delete(m.runtimeUpgradeGatewayReceipts, key)
			} else {
				count++
			}
		}
	}
	key := appID + "\x00" + sessionID
	if _, exists := m.runtimeUpgradeGatewayReceipts[key]; !exists && count >= api.RuntimeUpgradeGatewaySessionLimit {
		return ErrConflict
	}
	m.runtimeUpgradeGatewayReceipts[key] = runtimeUpgradeGatewayReceipt{SessionID: sessionID, DeploymentID: candidateID, CutoverAt: cutover.CutoverAt, InstalledAt: now}
	return nil
}

func (m *MemStore) ListRuntimeUpgradeGatewayRepairApps(_ context.Context, after string) ([]string, error) {
	if after != "" && validateRuntimeAppEnvIDs(after, after, after) != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var apps []string
	for id, c := range m.runtimeUpgradeCutovers {
		d := m.deployments[id]
		if d.AppID > after && d.Status == DeployLive && d.DeletedAt == nil && d.TrafficPercent == 100 && time.Since(c.CutoverAt) <= api.RuntimeUpgradeOperationMaxAge {
			apps = append(apps, d.AppID)
		}
	}
	slices.Sort(apps)
	apps = slices.Compact(apps)
	return slices.Clone(apps[:min(len(apps), api.RuntimeUpgradeGatewayRepairBatch)]), nil
}

func (m *MemStore) PruneExpiredRuntimeUpgradeGatewayReceipts(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, r := range m.runtimeUpgradeGatewayReceipts {
		if time.Since(r.InstalledAt) > api.RuntimeUpgradeGatewayReceiptMaxAge {
			delete(m.runtimeUpgradeGatewayReceipts, key)
		}
	}
	return nil
}

func (m *MemStore) VerifyRuntimeUpgrade(_ context.Context, accountID, id string, sessions []string) (RuntimeUpgradeVerification, error) {
	out, err := newRuntimeUpgradeVerification(id, sessions, time.Now().UTC())
	if err != nil || validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeVerification{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.runtimeUpgradeOperations[id]
	if !ok || op.AccountID != accountID {
		return RuntimeUpgradeVerification{}, ErrNotFound
	}
	if op.Phase != RuntimeUpgradeComplete {
		out.Reason = "activation_pending"
		return out, nil
	}
	cutover, ok := m.runtimeUpgradeCutovers[op.DeploymentID]
	if !ok || !op.cutover(op.WakeID).matches(cutover) {
		out.Reason = "activation_changed"
		return out, nil
	}
	candidate := m.deployments[op.DeploymentID]
	baseline, ok := m.runtimeUpgradeBaselines[candidate.ID]
	pin := m.runtimeUpgradeTargets[candidate.ID]
	current, err := m.runtimeUpgradeBaselineForTargetModeLocked(candidate, op.ServingDeploymentID, pin, true)
	target, qualification := m.runtimeReleases[op.TargetReleaseID], m.runtimeReleaseQualifications[op.TargetReleaseID]
	binding := m.runtimeArtifactBindings[runtimeArtifactBindingKey(accountID, candidate.RootfsKey)]
	if !ok || err != nil || !sameRuntimeUpgradeBaseline(baseline, current) || binding != target.ID || qualification.ReportSHA256 != op.QualificationReportSHA256 || !validRuntimeReleaseQualification(target, qualification, out.CheckedAt) {
		out.Reason = "activation_inputs_changed"
		return out, nil
	}
	var receipts []runtimeUpgradeGatewayReceipt
	for _, session := range out.GatewaySessions {
		if r, ok := m.runtimeUpgradeGatewayReceipts[op.AppID+"\x00"+session]; ok {
			receipts = append(receipts, r)
		}
	}
	return evaluateRuntimeUpgradeVerification(out, cutover, candidate.Scope, receipts, m.appHealthHistory[op.AppID].Latest, op.AppID), nil
}
