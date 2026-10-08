package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) profileGateDecisionLocked(d Deployment, now time.Time) api.ProfileCanaryGateDecision {
	p := m.profileDeploymentPolicies[d.AppID]
	stable, count := m.profileCanaryStableLocked(d)
	active := 0
	for _, other := range m.deployments {
		if other.AppID == d.AppID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(d.Scope) && other.Status == DeployLive && other.DeletedAt == nil && other.TrafficPercent > 0 {
			active++
		}
	}
	if active != 2 {
		count = 0
	}
	var signal *api.CanaryProfileSignal
	if d.CanaryStepStartedAt != nil {
		key := ProfileCanaryCheckKey{DeploymentID: d.ID, CanaryStep: d.CanaryStep, CanaryStepStartedAt: *d.CanaryStepStartedAt, PolicyRevision: p.Revision}
		if row := m.profileCanaryChecks[profileCanaryCheckMapKey(key)]; row != nil {
			copy := copyProfileCanarySignal(row.check)
			signal = &copy
		}
	}
	out := decideProfileCanaryGate(d, p, signal, stable.ID, count, now)
	if m.apps[d.AppID].Manifest.ExecutionMode == api.ExecutionModeService {
		out.AutoRollback = false
	}
	return out
}

func (m *MemStore) ReadProfileCanaryGate(_ context.Context, accountID, appID, deploymentID string) (api.ProfileCanaryGateDecision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok || d.AppID != appID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileCanaryGateDecision{}, ErrNotFound
	}
	return m.profileGateDecisionLocked(d, time.Now().UTC()), nil
}

func profileGateRollbackAudit(params *CanaryAdvanceParams) error {
	params.ProfileGateDecision.Status = "rolled_back"
	var data map[string]any
	if err := json.Unmarshal(params.Audit.Data, &data); err != nil {
		return err
	}
	decision := *params.ProfileGateDecision
	decision.Signal = nil
	data["profile_gate"] = decision
	body, err := json.Marshal(data)
	params.Audit.Data = body
	return err
}

func (m *MemStore) abortProfileGatedCanaryLocked(ctx context.Context, d Deployment, params CanaryAdvanceParams, now time.Time) (Deployment, int64, error) {
	stableID := params.ProfileGateDecision.StableDeploymentID
	stable, ok := m.deployments[stableID]
	if !ok || stable.AppID != d.AppID || normalizedDeploymentScope(stable.Scope) != normalizedDeploymentScope(d.Scope) || stable.Status != DeployLive || stable.TrafficPercent <= 0 {
		return Deployment{}, 0, ErrCanaryStateInvalid
	}
	if err := m.checkBindingReleaseTrafficLocked(ctx, map[string]int{d.ID: 0, stableID: 100}); err != nil {
		return Deployment{}, 0, err
	}
	now = time.Now().UTC()
	if !m.safeReleaseWorkerLeaseUntil.After(now) {
		return Deployment{}, 0, ErrSafeReleaseLeaseUnavailable
	}
	if err := profileGateRollbackAudit(&params); err != nil {
		return Deployment{}, 0, err
	}
	audit := params.Audit
	audit.DeploymentID = uuid.MustParse(d.ID)
	audit.At = now
	auditID, err := m.appendDeploymentAuditLocked(audit)
	if err != nil {
		return Deployment{}, 0, err
	}
	before := d
	d.TrafficPercent = 0
	d.RolloutState = "aborted"
	d.RolloutAbortedAt = &now
	d.RolloutAbortedReason = params.ProfileGateDecision.Reason
	stable.TrafficPercent = 100
	m.putDeploymentLocked(stableID, stable)
	m.putDeploymentLocked(d.ID, d)
	m.enqueueRolloutOutcomeWebhooksLocked(before, d)
	return d, auditID, nil
}
