// adr:1352
package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) queueCanarySnapshotLocked(account, appID, deployment, binding string) (queueCanarySnapshot, error) {
	app, ok := m.apps[appID]
	dep, found := m.deployments[deployment]
	if !found {
		for _, d := range m.deployments {
			if sameMemUUID(d.ID, deployment) {
				dep, found = d, true
				deployment = d.ID
				break
			}
		}
	}
	if !ok || !found || app.AccountID != account || app.Status == AppDeleted || dep.AppID != appID || dep.Status != "live" || dep.DeletedAt != nil {
		return queueCanarySnapshot{}, ErrNotFound
	}
	if app.Manifest.RevisionPinTTLSeconds <= 0 || dep.TrafficPercent == 0 && !m.revisionPins[deployment].After(time.Now()) {
		return queueCanarySnapshot{}, ErrConflict
	}
	var out queueCanarySnapshot
	count := 0
	for _, b := range m.queueBindings {
		if b.AppID != appID || b.AccountID != account || b.Name != binding || !b.Enabled || b.RetiredAt != nil || b.Mode != "push" || !m.queueBindingEnvironmentAvailableLocked(b) || b.DeploymentScope != "" && b.DeploymentScope != dep.Scope || b.DeploymentScope == "" && invocationStageScope(dep.Scope) {
			continue
		}
		for _, t := range m.triggers {
			if !t.QueueBindingID.Valid || !sameMemUUID(t.QueueBindingID.String(), b.ID) {
				continue
			}
			count++
			if !t.Enabled || !sameMemUUID(t.AppID.String(), appID) || !sameMemUUID(t.AccountID.String(), account) || t.Kind != "queue" || !t.Source.Valid || t.Source.String != "queue" || t.QueueBindingScope != b.DeploymentScope || (b.EnvironmentID != "" && !sameMemUUID(t.QueueBindingEnvironmentID.String(), b.EnvironmentID)) {
				return out, ErrConflict
			}
			out = queueCanarySnapshot{BindingID: b.ID, Name: b.Name, QueueName: b.QueueName, Scope: dep.Scope, EnvironmentID: b.EnvironmentID, BindingScope: b.DeploymentScope,
				MaxAttempts: int(t.MaxAttempts), PayloadMaxBytes: int(t.PayloadMaxBytes), FilterCriteria: t.FilterCriteria, PoisonStrategy: t.BrokerPoisonStrategy, RevisionTTLSeconds: app.Manifest.RevisionPinTTLSeconds, TriggerID: t.ID.String(), Config: t.Config, BatchSize: int(t.BatchSizeMax), BatchWindow: int(t.BatchWindowMs), Retry: b.RetryPolicyJSON, Concurrency: b.MaxConcurrency,
				DeploymentID: dep.ID, OverrideEnv: dep.OverrideEnv, RootfsKey: dep.RootfsKey, ImageDigest: dep.ImageDigest}
		}
	}
	if count != 1 {
		return out, ErrConflict
	}
	return out, nil
}
func (m *MemStore) CreateQueueBindingCanary(_ context.Context, account, app string, req api.CreateQueueBindingCanaryRequest) (api.QueueBindingCanary, error) {
	if err := validateQueueCanaryRequest(req); err != nil {
		return api.QueueBindingCanary{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createQueueBindingCanaryLocked(account, app, req)
}
func (m *MemStore) createQueueBindingCanaryLocked(account, app string, req api.CreateQueueBindingCanaryRequest) (api.QueueBindingCanary, error) {
	s, err := m.queueCanarySnapshotLocked(account, app, req.DeploymentID, req.Binding)
	if err != nil {
		return api.QueueBindingCanary{}, err
	}
	if !queueCanarySupported(s) {
		return api.QueueBindingCanary{}, ErrInvalidArgument
	}
	var depth int64
	for _, inv := range m.invocations {
		if inv.AppID == app && inv.Source == InvocationQueue && (inv.State == InvocationPending || inv.State == InvocationDispatching) && m.productionInvocationWorkLocked(inv) {
			depth++
		}
	}
	if err := queueCanaryProducerCapacity(m.accounts[account].Plan, depth); err != nil {
		return api.QueueBindingCanary{}, err
	}
	for id, r := range m.queueBindingCanaries {
		if m.invocations[id].AppID == app && r.Snapshot.BindingID == s.BindingID && !r.Cancelled && r.Report.ExpiresAt.After(time.Now()) && (m.invocations[id].State == InvocationPending || m.invocations[id].State == InvocationDispatching) {
			return api.QueueBindingCanary{}, ErrConflict
		}
	}
	r, inv := newQueueCanary(s, req, account, app)
	if _, err := m.enqueueInvocationLocked(inv); err != nil {
		return api.QueueBindingCanary{}, err
	}
	if m.queueBindingCanaries == nil {
		m.queueBindingCanaries = make(map[string]queueCanaryRecord)
	}
	m.queueBindingCanaries[r.Report.ID] = r
	return r.Report, nil
}
func (m *MemStore) getQueueCanaryLocked(account, app, id string) (api.QueueBindingCanary, error) {
	r, ok := m.queueBindingCanaries[id]
	inv, found := m.invocations[id]
	if !ok || !found || inv.AccountID != account || inv.AppID != app {
		return api.QueueBindingCanary{}, ErrNotFound
	}
	current, err := m.queueCanarySnapshotLocked(account, app, r.Report.DeploymentID, r.Report.Binding)
	var snapshot *queueCanarySnapshot
	if err == nil {
		snapshot = &current
	} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
		return api.QueueBindingCanary{}, err
	}
	// Missing or changed consumers are blocked observations, not read failures.
	return queueCanaryStatus(r, inv, snapshot, time.Now()), nil
}
func (m *MemStore) GetQueueBindingCanary(_ context.Context, account, app, id string) (api.QueueBindingCanary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getQueueCanaryLocked(account, app, id)
}
func (m *MemStore) CancelQueueBindingCanary(_ context.Context, account, app, id string) (api.QueueBindingCanary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.getQueueCanaryLocked(account, app, id); err != nil {
		return api.QueueBindingCanary{}, err
	}
	r := m.queueBindingCanaries[id]
	r.Cancelled = true
	m.queueBindingCanaries[id] = r
	inv := m.invocations[id]
	if inv.State == InvocationPending {
		inv.State = InvocationCancelled
		m.invocations[id] = inv
	}
	return m.getQueueCanaryLocked(account, app, id)
}
func (m *MemStore) LatestQueueBindingCanary(_ context.Context, account, app, deployment, binding string) (api.QueueBindingCanary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest queueCanaryRecord
	for id, r := range m.queueBindingCanaries {
		inv := m.invocations[id]
		if inv.AccountID == account && inv.AppID == app && r.Report.DeploymentID == deployment && r.Report.Binding == binding && r.Report.PublishedAt.After(latest.Report.PublishedAt) {
			latest = r
		}
	}
	if latest.Report.ID == "" {
		return api.QueueBindingCanary{}, ErrNotFound
	}
	return m.getQueueCanaryLocked(account, app, latest.Report.ID)
}
