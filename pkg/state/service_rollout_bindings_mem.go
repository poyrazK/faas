package state

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) prepareServiceBindingLocked(ctx context.Context, target Deployment, action, recipientID, predecessorID, reason string) (Deployment, error) {
	if err := serviceBindingRequestMatches(ctx, target, action, recipientID, predecessorID); err != nil {
		return target, err
	}
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID == "" && target.ServiceRolloutHandoff.BindingsCheck != nil && target.ServiceRolloutHandoff.BindingsCheck.Action == action && target.ServiceRolloutHandoff.BindingsCheck.Status != "passed" {
		return target, ErrBindingReleaseRequired
	}
	if recipientID == "" {
		return target, nil
	}
	requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string)
	if requestID != "" {
		policy := m.bindingReleasePolicyLocked(target.AppID, target.Scope)
		if policy.Mode == "enforce" {
			found := false
			for _, fence := range bindingReleaseFences(ctx) {
				if sameDeploymentID(fence.DeploymentID, recipientID) && releaseFenceMatchesPolicy(fence, policy) {
					found = true
				}
			}
			if !found {
				return target, ErrBindingReleaseRequired
			}
		}
	}
	err := m.checkBindingReleaseTrafficLocked(ctx, map[string]int{recipientID: 100})
	if IsBindingReleaseRequired(err) {
		target = queueServiceBindingCheck(target, action, recipientID, predecessorID, reason)
		m.deployments[target.ID] = target
		return target, ErrBindingReleaseRequired
	}
	if err != nil {
		return target, err
	}
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID != "" {
		desired := 1
		if action == ServiceRolloutActionPromote && m.apps[target.AppID].Manifest.ServiceReplicas != nil {
			desired = m.apps[target.AppID].Manifest.ServiceReplicas.Desired
		}
		ready := 0
		for _, instance := range m.instances {
			if instance.DeploymentID == recipientID && instance.Mode == string(InstanceModeService) && State(instance.State) == StateRunning {
				ready++
			}
		}
		if ready < desired {
			return target, ErrServiceRolloutNotReady
		}
	}
	return target, nil
}

func (m *MemStore) completeServiceBindingLocked(ctx context.Context, target Deployment) (Deployment, error) {
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID == "" {
		return target, nil
	}
	accountID := uuid.MustParse(m.apps[target.AppID].AccountID)
	auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{DeploymentID: uuid.MustParse(target.ID), AccountID: &accountID, Kind: DeployTrafficChanged, Actor: "apid:binding_service_rollout", At: time.Now().UTC(), Data: serviceBindingAudit(ctx, target)})
	if err != nil {
		return target, err
	}
	target.ServiceRolloutHandoff.BindingsCheck = passedServiceBindingGate(ctx, target, strconv.FormatInt(auditID, 10))
	return target, nil
}

func (m *MemStore) UpdateServiceRolloutBindingStatus(_ context.Context, id, requestID, code string, blockers []api.BindingCheckFinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	gate := target.ServiceRolloutHandoff.BindingsCheck
	if !IsServiceRollout(target) || gate == nil || gate.RequestID != requestID || gate.Status == "passed" {
		return ErrServiceRolloutInvalid
	}
	target.ServiceRolloutHandoff.BindingsCheck = blockedServiceBindingGate(gate, code, blockers)
	target.ServiceRolloutHandoff.LastError = target.ServiceRolloutHandoff.BindingsCheck.Code
	m.deployments[id] = target
	return nil
}

func (m *MemStore) RequestServiceRolloutAbort(_ context.Context, appID, id, predecessorID, reason string) (Deployment, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, rows, err := m.serviceRolloutTargetLocked(id)
	if err != nil || target.AppID != appID {
		if err == nil {
			err = ErrNotFound
		}
		return target, 0, err
	}
	previous, found := previousMemServiceRolloutRow(target, rows)
	if !found || previous.id != predecessorID {
		return target, 0, ErrServiceRolloutInvalid
	}
	target = queueServiceBindingCheck(target, ServiceRolloutActionAbort, predecessorID, predecessorID, reason)
	accountID := uuid.MustParse(m.apps[target.AppID].AccountID)
	auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{DeploymentID: uuid.MustParse(id), AccountID: &accountID, Kind: DeployRolledBack, Actor: "operator:cli:recover_rollout", At: time.Now().UTC(), Data: serviceBindingIntentAudit(target, reason)})
	if err != nil {
		return target, 0, err
	}
	m.deployments[id] = target
	return target, auditID, nil
}
