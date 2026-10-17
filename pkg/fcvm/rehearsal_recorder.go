// adr: 1351
package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

func (m *Manager) startRehearsalRecorder(ctx context.Context, req WakeRequest) (*rehearsalrecorder.Session, error) {
	if req.RehearsalRunID == "" {
		return nil, nil
	}
	policies, ok := m.rehearsalNetworkPolicies.(RehearsalSimulationPolicyStore)
	if !ok {
		return nil, nil
	}
	run, err := policies.ScenarioRehearsalSimulationRun(ctx, req.AppID)
	if err != nil {
		return nil, err
	}
	if run == "" {
		return nil, nil
	}
	store, ok := m.rehearsalNetworkPolicies.(rehearsalrecorder.Store)
	if !ok || run != req.RehearsalRunID {
		return nil, errors.New("durable rehearsal recorder support is unavailable")
	}
	identity := rehearsalrecorder.Identity{AccountID: req.AccountID, AppID: req.AppID, RunID: run, InstanceID: req.Instance, DeploymentID: req.DeploymentID}
	if cold, supported := store.(rehearsalrecorder.ColdStore); supported && !req.AppTaskOnly {
		required, checkErr := cold.ScenarioColdBootRequired(ctx, identity)
		if checkErr != nil {
			return nil, checkErr
		}
		if required && (req.Snapshot != nil || req.KeepPaused) {
			return nil, errors.New("cold-start rehearsal refuses snapshot or paused serving execution")
		}
	}
	session, err := rehearsalrecorder.Start(ctx, m.lifecycleCtx, store, identity) //nolint:contextcheck // The recorder is intentionally daemon-owned, beyond the Wake RPC.
	if err != nil {
		return nil, fmt.Errorf("start rehearsal recorder: %w", err)
	}
	m.mu.Lock()
	if m.rehearsalRecorders == nil {
		m.rehearsalRecorders = make(map[string]*rehearsalrecorder.Session)
	}
	if req.AppTaskOnly {
		session.MarkCommand()
	}
	m.rehearsalRecorders[req.Instance] = session
	m.mu.Unlock()
	return session, nil
}

// InstanceRehearsalRecorder never adopts a previous owner's persisted session.
func (m *Manager) InstanceRehearsalRecorder(instance string) (*rehearsalrecorder.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.rehearsalRecorders[instance]
	if session == nil {
		return nil, errors.New("rehearsal recorder is unavailable")
	}
	return session, nil
}

func (m *Manager) failRehearsalRecorder(instance string) {
	m.mu.Lock()
	session := m.rehearsalRecorders[instance]
	m.mu.Unlock()
	if session != nil {
		if !session.Retiring() {
			session.Fail()
		}
	}
}

func (m *Manager) rehearsalRecorderRetiring(instance string) bool {
	m.mu.Lock()
	session := m.rehearsalRecorders[instance]
	m.mu.Unlock()
	return session != nil && session.Retiring()
}

func (m *Manager) stopRehearsalRecorder(instance string) {
	m.mu.Lock()
	session := m.rehearsalRecorders[instance]
	delete(m.rehearsalRecorders, instance)
	m.mu.Unlock()
	if session != nil {
		session.Stop()
	}
}
