package state

import (
	"context"
	"time"
)

func (m *MemStore) PublishOwnedInstanceRuntime(ctx context.Context, p RuntimeInstancePublication) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if err := validateRuntimeInstancePublication(p); err != nil {
		return Instance{}, err
	}
	// MemStore has no notification channel to commit a notification with.
	if p.Bookkeeping != nil && p.Bookkeeping.NotifyChannel != "" {
		return Instance{}, ErrInvalidArgument
	}
	_, guarded, err := generationGuardedPublication(ctx, p)
	if err != nil {
		return Instance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if guarded {
		if err := m.checkRuntimeGenerationLocked(ctx); err != nil {
			return Instance{}, err
		}
	}
	instance, err := m.checkOwnedRuntimePublicationLocked(p)
	if err != nil {
		return Instance{}, err
	}
	if capture, found := m.instanceApplicationStandardAdmissions[p.InstanceID]; found && capture.Managed && p.AdmissionReceipt == nil && p.PromotionReceipt == nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	acceptance, err := m.prepareRuntimeUpgradeAcceptanceLocked(p)
	if err != nil {
		return Instance{}, err
	}
	if p.AdmissionReceipt != nil || p.PromotionReceipt != nil {
		instance, err := m.publishOwnedStandardRuntimeLocked(ctx, p)
		if err != nil {
			return Instance{}, err
		}
		m.runtimeInstanceConfigProofs[p.InstanceID] = runtimeInstanceConfigProof{WakeID: p.WakeID, NodeID: p.NodeID, Fence: p.ConfigFence}
		if acceptance != nil {
			if m.runtimeUpgradeAcceptances == nil {
				m.runtimeUpgradeAcceptances = make(map[string]RuntimeUpgradeAcceptance)
			}
			m.runtimeUpgradeAcceptances[acceptance.DeploymentID] = *acceptance
		}
		m.recordPublicationBookkeepingLocked(p, instance.DeploymentID)
		return instance, nil
	}
	prior := instance
	instance.Netns, instance.HostIP, instance.GuestUID = p.Netns, p.HostIP, p.GuestUID
	instance.StartedAt, instance.State = time.Now().UTC(), p.targetState()
	if p.ExpectedState == string(StateWarm) || p.ExpectedState == string(StateFrozen) {
		// A resumed VM's latest activity is the wake that resumed it.
		instance.LastRequestAt = instance.StartedAt
	}
	if err := m.guardInstanceRuntimeTransitionLocked(ctx, m.instances[p.InstanceID], instance); err != nil {
		return Instance{}, err
	}
	m.instances[p.InstanceID] = instance
	if p.Inputs != nil && p.targetState() == string(StateRunning) {
		if err := m.recordInstanceRuntimeConfigReceiptLocked(p.InstanceID, p.WakeID, *p.Inputs); err != nil {
			m.instances[p.InstanceID] = prior
			return Instance{}, err
		}
	}
	m.runtimeInstanceConfigProofs[p.InstanceID] = runtimeInstanceConfigProof{WakeID: p.WakeID, NodeID: p.NodeID, Fence: p.ConfigFence}
	if acceptance != nil {
		if m.runtimeUpgradeAcceptances == nil {
			m.runtimeUpgradeAcceptances = make(map[string]RuntimeUpgradeAcceptance)
		}
		m.runtimeUpgradeAcceptances[acceptance.DeploymentID] = *acceptance
	}
	m.recordPublicationBookkeepingLocked(p, instance.DeploymentID)
	return instance, nil
}

// recordPublicationBookkeepingLocked mirrors the bookkeeping PgStore commits
// with a publication.
func (m *MemStore) recordPublicationBookkeepingLocked(p RuntimeInstancePublication, deploymentID string) {
	b := p.Bookkeeping
	if b == nil {
		return
	}
	if b.EventKind != "" {
		subject := p.InstanceID
		_ = m.appendEventLocked("schedd", b.EventKind, &subject, b.EventData, nil, time.Now())
	}
	d := m.deployments[deploymentID]
	b.SnapshotBackoffRecorded = d.SnapshotMissCount != 0 || d.SnapshotMissLastAt != nil || d.SnapshotMissBackoffUntil != nil
}

func (m *MemStore) checkOwnedRuntimePublicationLocked(p RuntimeInstancePublication) (Instance, error) {
	if _, err := m.runtimeAppSecretFenceLocked(p.AccountID, p.AppID, p.InstanceID, p.Fence, true); err != nil {
		return Instance{}, err
	}
	instance := m.instances[p.InstanceID]
	if err := validateWorkerInstanceMutation(instance, p.targetState(), instance.Mode); err != nil {
		return Instance{}, err
	}
	if err := m.requireInstanceLayerArtifactsLocked(instance.DeploymentID, p.targetState()); err != nil {
		return Instance{}, err
	}
	if (instance.State != p.ExpectedState && (p.PromotionReceipt == nil || instance.State != string(StateRunning))) || instance.WakeID != p.WakeID || instance.NodeID != p.NodeID {
		return Instance{}, ErrConflict
	}
	snapshot, err := m.runtimeAppValuesLocked(p.AccountID, p.AppID, instance.DeploymentID)
	if err != nil {
		return Instance{}, runtimeSecretFenceError(err)
	}
	current, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || current != p.ConfigFence || current.SecretFence != p.Fence {
		return Instance{}, ErrConflict
	}
	if p.ExpectedState == string(StateWarm) || p.ExpectedState == string(StateFrozen) {
		if !m.instanceSnapshotRecoveryFreshLocked(p.InstanceID) {
			return Instance{}, ErrConflict
		}
		proof, ok := m.runtimeInstanceConfigProofs[p.InstanceID]
		if !ok || proof.WakeID != p.WakeID || proof.NodeID != p.NodeID || proof.Fence != p.ConfigFence {
			return Instance{}, ErrConflict
		}
	}
	return instance, nil
}

type runtimeInstanceConfigProof struct {
	WakeID, NodeID string
	Fence          RuntimeAppConfigFence
}

func (m *MemStore) InstanceRuntimeConfigFence(_ context.Context, accountID, appID, instanceID string) (RuntimeAppConfigFence, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, instanceID); err != nil {
		return RuntimeAppConfigFence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, appOK := m.apps[appID]
	instance, ok := m.instances[instanceID]
	proof, proved := m.runtimeInstanceConfigProofs[instanceID]
	if !appOK || app.AccountID != accountID || app.Status == AppDeleted || !ok || instance.AppID != appID || !proved ||
		proof.WakeID != instance.WakeID || proof.NodeID != instance.NodeID || proof.Fence.SecretFence.DeploymentID != instance.DeploymentID {
		return RuntimeAppConfigFence{}, ErrNotFound
	}
	return proof.Fence, nil
}
