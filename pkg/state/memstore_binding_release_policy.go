package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ BindingReleasePolicyStore = (*MemStore)(nil)

type bindingReleasePolicyKey struct{ appID, scope string }

func (m *MemStore) bindingReleasePolicyLocked(appID, scope string) api.BindingReleasePolicy {
	key := bindingReleasePolicyKey{appID, normalizedDeploymentScope(scope)}
	p, ok := m.bindingReleasePolicies[key]
	if !ok {
		return defaultBindingReleasePolicy(appID, scope)
	}
	if p.UpdatedAt != nil {
		stamp := *p.UpdatedAt
		p.UpdatedAt = &stamp
	}
	return p
}
func (m *MemStore) GetBindingReleasePolicy(_ context.Context, accountID, appID, scope string) (api.BindingReleasePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.AccountID != accountID || a.Status == AppDeleted {
		return api.BindingReleasePolicy{}, ErrNotFound
	}
	return m.bindingReleasePolicyLocked(appID, scope), nil
}
func (m *MemStore) SetBindingReleasePolicy(_ context.Context, accountID, appID, scope string, r api.SetBindingReleasePolicyRequest) (api.BindingReleasePolicy, error) {
	if api.ValidateBindingReleasePolicyRequest(r) != nil || api.ValidateScope(normalizedDeploymentScope(scope)) != nil {
		return api.BindingReleasePolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.AccountID != accountID || a.Status == AppDeleted {
		return api.BindingReleasePolicy{}, ErrNotFound
	}
	p := m.bindingReleasePolicyLocked(appID, scope)
	if p.Revision != *r.ExpectedRevision {
		return p, ErrBindingReleasePolicyRevision
	}
	age := api.DefaultBindingVerificationAge
	if r.MaxVerificationAge != "" {
		age, _ = time.ParseDuration(r.MaxVerificationAge)
	}
	now := time.Now().UTC()
	p.Mode, p.Revision, p.MaxVerificationAge, p.RequireApplicationAck, p.UpdatedAt, p.Reason = r.Mode, p.Revision+1, age.String(), r.RequireApplicationAck, &now, r.Reason
	if m.bindingReleasePolicies == nil {
		m.bindingReleasePolicies = map[bindingReleasePolicyKey]api.BindingReleasePolicy{}
	}
	m.bindingReleasePolicies[bindingReleasePolicyKey{appID, p.Scope}] = p
	m.bindingReleasePolicyHistory = append(m.bindingReleasePolicyHistory, p)
	return m.bindingReleasePolicyLocked(appID, scope), nil
}

// Check the complete proposed distribution before mutating anything.
func (m *MemStore) checkBindingReleaseTrafficLocked(ctx context.Context, proposed map[string]int) error {
	for _, f := range bindingReleaseFences(ctx) {
		d := m.deployments[f.DeploymentID]
		if d.ID == "" {
			for id, candidate := range m.deployments {
				if sameDeploymentID(id, f.DeploymentID) {
					d = candidate
					break
				}
			}
		}
		if err := m.checkBindingTrafficGuardLocked(d, &bindingTrafficGuard{fence: f}); err != nil {
			return err
		}
	}
	for id, percent := range proposed {
		d := m.deployments[id]
		if percent <= d.TrafficPercent {
			continue
		}
		if r, ok := m.checkedRollbackForTargetLocked(id); ok && (rollbackRequestID(ctx) != r.ID || r.Status != "ready" && r.Status != "blocked") {
			return ErrCheckedRollbackRequired
		}
		p := m.bindingReleasePolicyLocked(d.AppID, d.Scope)
		if p.Mode != "enforce" {
			continue
		}
		found := false
		for _, f := range bindingReleaseFences(ctx) {
			if !sameDeploymentID(f.DeploymentID, id) || !releaseFenceMatchesPolicy(f, p) {
				continue
			}
			if err := m.checkBindingTrafficGuardLocked(d, &bindingTrafficGuard{fence: f}); err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			return ErrBindingReleaseRequired
		}
	}
	return nil
}

func (m *MemStore) rejectUncheckedBindingReleaseLocked(appID, scope string) error {
	if m.bindingReleasePolicyLocked(appID, scope).Mode == "enforce" {
		return ErrBindingReleaseRequired
	}
	return nil
}

func (m *MemStore) checkBindingReleaseFailureLocked(d Deployment) error {
	var fallback Deployment
	for id, candidate := range m.deployments {
		if id == d.ID || candidate.AppID != d.AppID || candidate.Status != DeployLive {
			continue
		}
		if fallback.ID == "" || candidate.TrafficPercent > fallback.TrafficPercent || candidate.TrafficPercent == fallback.TrafficPercent && candidate.CreatedAt.After(fallback.CreatedAt) {
			fallback = candidate
		}
	}
	if fallback.ID != "" && fallback.TrafficPercent < 100 {
		return m.rejectUncheckedBindingReleaseLocked(fallback.AppID, fallback.Scope)
	}
	return nil
}

func (m *MemStore) rejectUncheckedBindingReleaseGraphLocked(projectID, scope string) error {
	for _, app := range m.apps {
		if app.ProjectID == projectID && app.Status != AppDeleted {
			if err := m.rejectUncheckedBindingReleaseLocked(app.ID, scope); err != nil {
				return err
			}
		}
	}
	return nil
}
