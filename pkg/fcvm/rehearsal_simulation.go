// adr: 20261017201006
package fcvm

import (
	"context"
	"errors"
)

type RehearsalSimulationPolicyStore interface {
	ScenarioRehearsalSimulationRun(context.Context, string) (string, error)
}

// InstanceRehearsalRelayIdentity grants simulation only after checking both the
// installed containment identity and retained simulation enrollment. It grants
// no live relay, event, or federation capability.
func (m *Manager) InstanceRehearsalRelayIdentity(ctx context.Context, id string) (string, string, string, error) {
	m.mu.Lock()
	inst := m.live[id]
	if inst == nil {
		m.mu.Unlock()
		return "", "", "", errors.New("simulation instance is not live")
	}
	app, account, installed := inst.AppID, inst.AccountID, inst.Net.RehearsalRunID
	m.mu.Unlock()
	if app == "" {
		if installed != "" {
			return "", "", "", ErrRehearsalExternalActionDenied
		}
		return "", "", "", nil
	}
	store, ok := m.rehearsalNetworkPolicies.(RehearsalSimulationPolicyStore)
	if !ok {
		return "", "", "", nil
	}
	run, err := store.ScenarioRehearsalSimulationRun(ctx, app)
	if err != nil {
		return "", "", "", err
	}
	if run == "" {
		return "", "", "", nil
	}
	if run != installed || app == "" || account == "" {
		return "", "", "", ErrRehearsalExternalActionDenied
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live[id] != inst || inst.Net.RehearsalRunID != run {
		return "", "", "", ErrRehearsalExternalActionDenied
	}
	return app, account, run, nil
}
