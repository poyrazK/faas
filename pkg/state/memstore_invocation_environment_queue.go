package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
)

var _ ProjectEnvironmentQueueInvocationStore = (*MemStore)(nil)

func (m *MemStore) InvocationEnvironmentQueueAdmission(_ context.Context, id string) (InvocationEnvironmentQueueAdmission, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return InvocationEnvironmentQueueAdmission{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owner, ok := m.invocationEnvironmentQueueAdmissions[id]
	if !ok {
		return owner, ErrNotFound
	}
	return owner, nil
}

func (m *MemStore) EnqueueProjectEnvironmentQueueInvocation(ctx context.Context, accountID, projectID, deploymentID, name string, inv Invocation) (Invocation, error) {
	set, err := m.ProjectEnvironmentQueueConsumersForDeployment(ctx, accountID, projectID, deploymentID)
	if err != nil {
		return Invocation{}, err
	}
	inv, err = prepareEnvironmentQueueInvocation(ctx, m, set, name, inv)
	if err != nil {
		return Invocation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.projectEnvironmentQueueConsumersLocked(accountID, projectID, deploymentID, false, true)
	if err != nil {
		return Invocation{}, err
	}
	consumer, err := queueConsumerByName(current, name)
	if err != nil {
		return Invocation{}, err
	}
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	}
	for storedID := range m.invocations {
		// Legacy MemStore producers use compact UUIDs. Match identity rather
		// than spelling so a caller cannot reuse an existing production ID.
		parsed, err := uuid.Parse(storedID)
		if storedID == inv.ID || (err == nil && parsed.String() == inv.ID) {
			return Invocation{}, ErrConflict
		}
	}
	owner := queueAdmissionForInvocation(set, consumer, inv)
	if _, err := validateQueueAdmission(owner, inv, current); err != nil {
		return Invocation{}, err
	}
	if !m.invocationQueuePinLocked(inv, owner.DeploymentID, true) {
		return Invocation{}, ErrInvocationEnvironmentWorkIsolation
	}
	account, found := m.accounts[accountID]
	if !found {
		return Invocation{}, ErrNotFound
	}
	if err := environmentQueueProducerCapacity(account.Plan, m.environmentQueueProducerDepthLocked(current.EnvironmentID, current.AppID)); err != nil {
		return Invocation{}, err
	}
	m.invocations[inv.ID] = cloneInvocationWorkEnvelope(inv)
	m.invocationEnvironmentQueueAdmissions[inv.ID] = owner
	return cloneInvocationWorkEnvelope(inv), nil
}

func (m *MemStore) invocationQueuePinLocked(inv Invocation, deploymentID string, requireLive bool) bool {
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return false
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return false
	}
	env, app := m.projectEnvironments[inv.EnvironmentID], m.apps[inv.AppID]
	if release != "" {
		set, ok := m.projectReleaseSets[release]
		if !ok || set.AccountID != app.AccountID || set.ProjectID != app.ProjectID || set.EnvironmentSlug != env.Slug || (requireLive && !releaseUsable(set)) {
			return false
		}
		revision = releaseMemberForApp(set, inv.AppID)
	}
	dep, ok := m.deployments[revision]
	return ok && revision == deploymentID && app.Status != AppDeleted && dep.AppID == app.ID && dep.Scope == env.Slug && (dep.Status == DeployLive || !requireLive)
}

func (m *MemStore) validateInvocationQueueClaimLocked(inv Invocation, requireLive bool) (bool, error) {
	owner, owned := m.invocationEnvironmentQueueAdmissions[inv.ID]
	if !owned {
		return false, nil
	}
	if !queueOwnerMatchesEnvelope(owner, inv) {
		return true, ErrInvocationEnvironmentWorkIsolation
	}
	app := m.apps[owner.AppID]
	set, err := m.projectEnvironmentQueueConsumersLocked(owner.AccountID, app.ProjectID, owner.DeploymentID, false, requireLive)
	if err != nil {
		return true, ErrInvocationEnvironmentWorkIsolation
	}
	if _, err := validateQueueAdmission(owner, inv, set); err != nil {
		return true, err
	}
	if !m.invocationQueuePinLocked(inv, owner.DeploymentID, requireLive) {
		return true, ErrInvocationEnvironmentWorkIsolation
	}
	return true, nil
}

func (m *MemStore) queueClaimCapacityLocked(inv Invocation) error {
	owner, owned := m.invocationEnvironmentQueueAdmissions[inv.ID]
	if !owned {
		return nil
	}
	set := m.projectEnvironmentQueueRuntimeSets[owner.DeploymentID]
	var maxConcurrency int
	for _, consumer := range set.Consumers {
		if consumer.ID == owner.ConsumerID {
			maxConcurrency = consumer.MaxConcurrency
		}
	}
	active := 0
	for id, other := range m.invocationEnvironmentQueueAdmissions {
		if other.EnvironmentID == owner.EnvironmentID && other.AppID == owner.AppID && other.QueueName == owner.QueueName {
			row := m.invocations[id]
			if row.State == InvocationDispatching || row.QuotaReserved {
				active++
			}
		}
	}
	if active >= maxConcurrency {
		return ErrQuotaExceeded
	}
	return nil
}
