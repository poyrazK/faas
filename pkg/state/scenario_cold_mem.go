package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

func (m *MemStore) ScenarioColdPreparation(ctx context.Context, app string) (rehearsalcold.Preparation, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalcold.Preparation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[app]
	if !ok {
		return p, ErrNotFound
	}
	return p, nil
}
func (m *MemStore) BeginScenarioColdPreparation(ctx context.Context, account, app string) (rehearsalcold.Preparation, error) {
	var empty rehearsalcold.Preparation
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, ok := m.scenarioActionPolicies[app]
	if !ok || policy.Member.AccountID != account || policy.Member.ColdStartJSON == "" {
		return empty, ErrNotFound
	}
	if !eligibleScenarioTestApp(m.apps[app], account) || policy.RecorderClosing || policy.SimulationFailed || !policy.SealedAt.IsZero() {
		return empty, ErrConflict
	}
	if p, exists := m.scenarioColdPreparations[app]; exists {
		if p.State == "failed" || p.State == "booted" {
			return empty, ErrConflict
		}
		return p, nil
	}
	p := rehearsalcold.Preparation{ID: uuid.NewString(), AccountID: account, AppID: app, RunID: policy.Member.RunID, Workload: policy.Member.Workload, State: "retired"}
	deps := 0
	for _, d := range m.deployments {
		if d.AppID == app {
			deps++
			p.DeploymentID = d.ID
		}
	}
	if deps != 1 {
		return empty, ErrConflict
	}
	for _, v := range m.invocations {
		if v.AppID == app {
			return empty, ErrConflict
		}
	}
	for _, v := range m.appTasks {
		if v.AppID == app && (v.Status == AppTaskQueued || v.Status == AppTaskRestoring || v.Status == AppTaskRunning) {
			return empty, ErrConflict
		}
	}
	for _, v := range m.exclusiveOperations {
		if v.AppID == app && (v.State == "pending" || v.State == "running") {
			return empty, ErrConflict
		}
	}
	for _, v := range m.workflowRuns {
		if v.AppID == app && (v.Status == "pending" || v.Status == "running" || v.Status == "awaiting_event") {
			return empty, ErrConflict
		}
	}
	for _, r := range policy.Recorders {
		if r.Status == "failed" {
			return empty, ErrConflict
		}
		for _, c := range r.Requests {
			if c.Status != "completed" {
				return empty, ErrConflict
			}
		}
	}
	for _, i := range m.instances {
		if i.AppID != app || coldTerminal(i.State) {
			continue
		}
		if p.InstanceID != "" || i.State != "running" || i.TailCount != 0 || i.DeploymentID != p.DeploymentID {
			return empty, ErrConflict
		}
		p.InstanceID, p.State = i.ID, "armed"
		for _, r := range policy.Recorders {
			if r.Identity.InstanceID == i.ID && r.Identity.DeploymentID == i.DeploymentID && r.Status == "recording" {
				p.RecorderSessionID = r.ID
			}
		}
		if p.RecorderSessionID == "" {
			return empty, ErrConflict
		}
	}
	if m.scenarioColdPreparations == nil {
		m.scenarioColdPreparations = map[string]rehearsalcold.Preparation{}
	}
	m.scenarioColdPreparations[app] = p
	return p, nil
}
func coldTerminal(s string) bool { return s == "stopped" || s == "parked" || s == "failed" }

func (m *MemStore) ClaimScenarioColdPreparation(ctx context.Context, app string) (rehearsalcold.Preparation, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalcold.Preparation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[app]
	policy := m.scenarioActionPolicies[app]
	if !ok || p.State != "armed" || policy.RecorderClosing || !policy.SealedAt.IsZero() {
		return p, ErrConflict
	}
	p.State = "claimed"
	m.scenarioColdPreparations[app] = p
	return p, nil
}
func (m *MemStore) CompleteScenarioColdPreparation(ctx context.Context, account, app string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[app]
	policy := m.scenarioActionPolicies[app]
	if !ok || p.AccountID != account {
		return ErrNotFound
	}
	if p.State == "ready" {
		return nil
	}
	if p.State != "retired" || policy.RecorderClosing || !policy.SealedAt.IsZero() {
		return ErrConflict
	}
	for _, i := range m.instances {
		if i.AppID == app && !coldTerminal(i.State) {
			return ErrConflict
		}
	}
	for _, snap := range m.snapshots {
		if m.deployments[snap.DeploymentID].AppID == app && !snap.Stale {
			return ErrConflict
		}
	}
	p.State = "ready"
	m.scenarioColdPreparations[app] = p
	return nil
}
func (m *MemStore) FailScenarioColdPreparation(ctx context.Context, app string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[app]
	if ok && p.State != "booted" && p.State != "failed" {
		p.State = "failed"
		m.scenarioColdPreparations[app] = p
	}
	return nil
}
func (m *MemStore) coldRetirement(ctx context.Context, id rehearsalrecorder.Identity, prep, session, expected, next string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[id.AppID]
	policy, err := m.recorderPolicyLocked(id)
	if err != nil {
		return err
	}
	r, found := policy.Recorders[session]
	if !ok || !found || !coldIdentity(p, id, prep, session) || r.Identity != id || r.Status != "recording" || p.State != expected || policy.RecorderClosing || policy.SimulationFailed {
		return ErrConflict
	}
	for _, c := range r.Requests {
		if c.Status != "completed" {
			return ErrConflict
		}
	}
	p.State = next
	m.scenarioColdPreparations[id.AppID] = p
	if next == "retired" {
		r.Status = "closed"
		policy.Recorders[session] = r
		m.scenarioActionPolicies[id.AppID] = policy
	}
	return nil
}
func (m *MemStore) FenceScenarioColdRetirement(ctx context.Context, id rehearsalrecorder.Identity, prep, session string) error {
	return m.coldRetirement(ctx, id, prep, session, "claimed", "fenced")
}
func (m *MemStore) CompleteScenarioColdRetirement(ctx context.Context, id rehearsalrecorder.Identity, prep, session string) error {
	return m.coldRetirement(ctx, id, prep, session, "fenced", "retired")
}
func (m *MemStore) ScenarioColdBootRequired(ctx context.Context, id rehearsalrecorder.Identity) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[id.AppID]
	if !ok {
		return false, nil
	}
	return coldBootRequired(p, id)
}
func (m *MemStore) RecordScenarioColdBoot(ctx context.Context, id rehearsalrecorder.Identity, session, method string, ms int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioColdPreparations[id.AppID]
	if !ok {
		return nil
	}
	required, err := coldBootRequired(p, id)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}
	policy, err := m.recorderPolicyLocked(id)
	if err != nil {
		return err
	}
	r, found := policy.Recorders[session]
	i := m.instances[id.InstanceID]
	if !found || r.Identity != id || r.Status != "recording" || policy.RecorderClosing || policy.SimulationFailed || !m.rehearsalExecutionLocked(id) || i.WakeID == "" || method != "cold_boot" || ms < 0 {
		return ErrConflict
	}
	p.State, p.BootInstanceID, p.BootRecorderSessionID, p.Method, p.StartupMS, p.WakeID = "booted", id.InstanceID, session, method, ms, i.WakeID
	m.scenarioColdPreparations[id.AppID] = p
	return nil
}

func (m *MemStore) coldExecutionAllowedLocked(id rehearsalrecorder.Identity) bool {
	p, ok := m.scenarioColdPreparations[id.AppID]
	if !ok {
		return true
	}
	if p.State == "failed" || p.DeploymentID != id.DeploymentID {
		return false
	}
	switch p.State {
	case "armed", "claimed":
		return p.InstanceID == id.InstanceID
	case "ready":
		return p.InstanceID != id.InstanceID
	case "booted":
		return p.BootInstanceID == id.InstanceID
	default:
		return false
	}
}
