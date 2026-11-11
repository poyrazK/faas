package state

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) prepareServiceBindingLocked(ctx context.Context, target Deployment, action, recipientID, predecessorID, reason string) (Deployment, error) {
	previousRecovery := m.lifecycleRecovery
	m.lifecycleRecovery = action == ServiceRolloutActionAbort
	defer func() { m.lifecycleRecovery = previousRecovery }()
	if err := serviceBindingRequestMatches(ctx, target, action, recipientID, predecessorID); err != nil {
		return target, err
	}
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID == "" && target.ServiceRolloutHandoff.BindingsCheck != nil && target.ServiceRolloutHandoff.BindingsCheck.Action == action && target.ServiceRolloutHandoff.BindingsCheck.Status != "passed" && serviceBindingTraffic(target.ServiceRolloutHandoff.BindingsCheck) == serviceRolloutTraffic(ctx) {
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
		target = queueServiceBindingCheck(ctx, target, action, recipientID, predecessorID, reason)
		m.deployments[target.ID] = target
		return target, ErrBindingReleaseRequired
	}
	if err != nil {
		return target, err
	}
	recipient, ok := m.deployments[recipientID]
	if !ok || recipient.AppID != target.AppID {
		return target, ErrConflict
	}
	sources, err := DeploymentReadinessSources(recipient)
	if err != nil {
		return target, err
	}
	deployed, err := m.serviceAppForDeploymentLocked(m.apps[target.AppID], recipient)
	if err != nil {
		return target, err
	}
	// Reverse routing and final teardown must retain a usable service recipient,
	// including the boot-only readiness contract when no probes are configured.
	reversing := action == ServiceRolloutActionAbort && deployed.Manifest.ExecutionMode == api.ExecutionModeService &&
		(recipient.TrafficPercent < 100 || target.ServiceRolloutHandoff.Action == ServiceRolloutActionAbort)
	if requestID != "" || len(sources) > 0 || reversing || serviceRolloutTraffic(ctx) < 100 {
		desired := 1
		if action == ServiceRolloutActionPromote && deployed.Manifest.ServiceReplicas != nil {
			desired = ServiceRolloutReadyTarget(deployed.Manifest.ServiceReplicas.Desired, serviceRolloutTraffic(ctx))
		}
		var replicas []Instance
		for _, instance := range m.instances {
			if instance.DeploymentID == recipientID {
				replicas = append(replicas, instance)
			}
		}
		targets := serviceReadinessTargets(replicas)
		signals := m.latestInstanceReadinessForTargetsLocked(targets)
		ready := 0
		for _, instance := range targets {
			if serviceReplicaReady(sources, signals[instance.InstanceID]) {
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

func (m *MemStore) RequestServiceRolloutAbort(ctx context.Context, appID, id, predecessorID, reason string) (Deployment, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if serviceAbortAttempt(ctx) != "" {
		hash, err := serviceAbortHash(ctx, appID, id, predecessorID, reason)
		if err != nil {
			return Deployment{}, 0, err
		}
		prior, err := m.serviceAbortAcceptanceLocked(appID, serviceAbortAttempt(ctx))
		if err == nil {
			return Deployment{}, 0, serviceAbortPrior(prior, hash)
		}
		if !errors.Is(err, ErrNotFound) {
			return Deployment{}, 0, err
		}
	}
	target, rows, err := m.serviceRolloutTargetLocked(id)
	if err != nil || target.AppID != appID {
		if err == nil {
			err = ErrNotFound
		}
		return target, 0, err
	}
	if expected, ok := serviceRecoveryReview(ctx); ok {
		current, err := m.releaseReviewLocked(appID, normalizedDeploymentScope(target.Scope))
		if err != nil {
			return target, 0, err
		}
		if err := checkServiceRecoveryReview(expected, current); err != nil {
			return target, 0, err
		}
	}
	previous, found := previousMemServiceRolloutRow(target, rows)
	if !found || previous.id != predecessorID {
		return target, 0, ErrServiceRolloutInvalid
	}
	target = queueServiceBindingCheck(ctx, target, ServiceRolloutActionAbort, predecessorID, predecessorID, reason)
	accountID := uuid.MustParse(m.apps[target.AppID].AccountID)
	auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{DeploymentID: uuid.MustParse(id), AccountID: &accountID, Kind: DeployRolledBack, Actor: rolloutRecoveryActor(ctx), At: time.Now().UTC(), Data: serviceAbortReceiptData(ctx, target, reason)})
	if err != nil {
		return target, 0, err
	}
	m.deployments[id] = target
	return target, auditID, nil
}
