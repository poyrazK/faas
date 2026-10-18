package state

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

func (m *MemStore) ScenarioSuspendResume(ctx context.Context, id string) (rehearsalresume.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalresume.Receipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioSuspendResumes[id]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}

func (m *MemStore) suspendEvidenceLocked(account, run string) (rehearsalresume.Evidence, error) {
	var raw string
	var names []string
	for _, p := range m.scenarioActionPolicies {
		if p.Member.AccountID == account && p.Member.RunID == run {
			if raw != "" && raw != p.Member.SuspendResumeJSON {
				return rehearsalresume.Evidence{}, ErrConflict
			}
			raw = p.Member.SuspendResumeJSON
			names = append(names, p.Member.Workload)
		}
	}
	if raw == "" {
		return rehearsalresume.Evidence{}, ErrNotFound
	}
	e := newSuspendEvidence(run, raw, names)
	for _, r := range m.scenarioSuspendResumes {
		if r.AccountID == account && r.RunID == run {
			e.Receipts = append(e.Receipts, r)
		}
	}
	finishSuspendEvidence(&e, names)
	return e, nil
}
func (m *MemStore) ScenarioSuspendResumeEvidence(ctx context.Context, account, run string) (rehearsalresume.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalresume.Evidence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.suspendEvidenceLocked(account, run)
}

func (m *MemStore) suspendBusyLocked(account, run string) bool {
	return m.suspendBusyExceptLocked(account, run, "", false)
}

func (m *MemStore) suspendBusyExceptLocked(account, run, invocation string, allowCommands bool) bool {
	for _, v := range m.invocations {
		p := m.scenarioActionPolicies[v.AppID]
		if p.Member.AccountID == account && p.Member.RunID == run && v.ID != invocation {
			return true
		}
	}
	if allowCommands {
		return false
	}
	for _, v := range m.appTasks {
		p := m.scenarioActionPolicies[v.AppID]
		if p.Member.AccountID == account && p.Member.RunID == run && (v.Status == AppTaskQueued || v.Status == AppTaskRestoring || v.Status == AppTaskRunning) {
			return true
		}
	}
	return false
}

func (m *MemStore) suspendCheckpointBusyLocked(r rehearsalresume.Receipt) bool {
	if m.suspendBusyExceptLocked(r.AccountID, r.RunID, r.InvocationID, r.State == "waiting") {
		return true
	}
	deps := 0
	for _, d := range m.deployments {
		if d.AppID == r.AppID {
			deps++
		}
	}
	if deps != 1 {
		return true
	}
	for _, i := range m.instances {
		if i.AppID == r.AppID && i.ID != r.InstanceID && !coldTerminal(i.State) {
			return true
		}
	}
	for _, v := range m.exclusiveOperations {
		if v.AppID == r.AppID && (v.State == "pending" || v.State == "running") {
			return true
		}
	}
	for _, v := range m.workflowRuns {
		if v.AppID == r.AppID && (v.Status == "pending" || v.Status == "running" || v.Status == "awaiting_event") {
			return true
		}
	}
	return false
}
func (m *MemStore) suspendReceiptValidLocked(r rehearsalresume.Receipt, expectedState string) bool {
	p := m.scenarioActionPolicies[r.AppID]
	a := m.apps[r.AppID]
	i := m.instances[r.InstanceID]
	rc, found := p.Recorders[r.RecorderSessionID]
	if !eligibleScenarioTestApp(a, r.AccountID) || a.Status != AppActive || p.RecorderClosing || p.SimulationFailed || !p.SealedAt.IsZero() || p.Member.RunID != r.RunID || p.Member.SuspendResumeJSON == "" || !found || rc.Status != "recording" || !suspendIdentity(r, rc.Identity, rc.ID) || i.AppID != r.AppID || i.DeploymentID != r.DeploymentID || i.State != expectedState || i.TailCount != 0 {
		return false
	}
	if r.InvocationID != "" && !m.suspendJobActiveLocked(r) {
		return false
	}
	for _, q := range rc.Requests {
		if q.Status != "completed" {
			return false
		}
	}
	return true
}

func (m *MemStore) BeginScenarioSuspendResume(ctx context.Context, account, run string) (rehearsalresume.Evidence, error) {
	var empty rehearsalresume.Evidence
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.suspendEvidenceLocked(account, run)
	if err != nil {
		return empty, err
	}
	if e.Spec.ManagedJob() {
		return empty, ErrConflict
	}
	if len(e.Receipts) > 0 {
		if len(e.Receipts) != len(e.Spec.Workloads) {
			return empty, ErrConflict
		}
		return e, nil
	}
	if m.suspendBusyLocked(account, run) {
		return empty, ErrConflict
	}
	var receipts []rehearsalresume.Receipt
	for app, p := range m.scenarioActionPolicies {
		if p.Member.AccountID != account || p.Member.RunID != run || !slices.Contains(e.Spec.Workloads, p.Member.Workload) {
			continue
		}
		r := rehearsalresume.Receipt{ID: uuid.NewString(), AccountID: account, RunID: run, AppID: app, Workload: p.Member.Workload, State: "armed"}
		deps := 0
		for _, d := range m.deployments {
			if d.AppID == app {
				deps++
				r.DeploymentID = d.ID
			}
		}
		live := 0
		for _, i := range m.instances {
			if i.AppID == app && !coldTerminal(i.State) {
				live++
				r.InstanceID = i.ID
			}
		}
		for _, rc := range p.Recorders {
			if rc.Identity.InstanceID == r.InstanceID && rc.Status == "recording" {
				r.RecorderSessionID = rc.ID
			}
		}
		if deps != 1 || live != 1 || !m.suspendReceiptValidLocked(r, "running") {
			return empty, ErrConflict
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
		receipts = append(receipts, r)
	}
	if len(receipts) != len(e.Spec.Workloads) {
		return empty, ErrConflict
	}
	if m.scenarioSuspendResumes == nil {
		m.scenarioSuspendResumes = map[string]rehearsalresume.Receipt{}
	}
	for _, r := range receipts {
		m.scenarioSuspendResumes[r.ID] = r
	}
	return m.suspendEvidenceLocked(account, run)
}
func (m *MemStore) PendingScenarioSuspendResumes(ctx context.Context) ([]rehearsalresume.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []rehearsalresume.Receipt
	for _, r := range m.scenarioSuspendResumes {
		p := m.scenarioActionPolicies[r.AppID]
		if r.State != "waiting" && r.State != "complete" && r.State != "failed" && eligibleScenarioTestApp(m.apps[r.AppID], r.AccountID) && !p.RecorderClosing && !p.SimulationFailed && p.SealedAt.IsZero() {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b rehearsalresume.Receipt) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if len(out) > 16 {
		out = out[:16]
	}
	return out, nil
}
func (m *MemStore) ClaimScenarioSuspendResume(ctx context.Context, id string) (rehearsalresume.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalresume.Receipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioSuspendResumes[id]
	if !ok || !m.suspendReceiptValidLocked(r, "running") || m.suspendCheckpointBusyLocked(r) {
		return r, ErrConflict
	}
	next, err := suspendStep(r, "armed", "claimed", 0, 0)
	if err == nil {
		m.scenarioSuspendResumes[id] = next
	}
	return next, err
}
func (m *MemStore) CompleteScenarioSuspendResume(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioSuspendResumes[id]
	if !ok || !m.suspendReceiptValidLocked(r, "running") || m.suspendCheckpointBusyLocked(r) {
		return ErrConflict
	}
	next, err := suspendStep(r, "resumed", "complete", 0, 0)
	if err == nil {
		m.scenarioSuspendResumes[id] = next
	}
	return err
}
func (m *MemStore) FailScenarioSuspendResume(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioSuspendResumes[id]
	if !ok {
		return ErrNotFound
	}
	p := m.scenarioActionPolicies[r.AppID]
	if !p.SealedAt.IsZero() {
		return ErrConflict
	}
	if r.State != "complete" && r.State != "failed" {
		r.State = "failed"
		m.scenarioSuspendResumes[id] = r
	}
	return nil
}
func (m *MemStore) AdvanceScenarioSuspendResume(ctx context.Context, id rehearsalrecorder.Identity, session, receipt, expected, next string, pause, resume int64) (rehearsalresume.Spec, error) {
	var empty rehearsalresume.Spec
	if !suspendOwnerStep(expected, next) {
		return empty, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioSuspendResumes[receipt]
	expectedInstance := "frozen"
	if next == "armed" {
		expectedInstance = "running"
	}
	if !ok || !suspendIdentity(r, id, session) || !m.suspendReceiptValidLocked(r, expectedInstance) {
		return empty, ErrConflict
	}
	if next == "armed" && m.suspendCommandsPendingLocked(r.AccountID, r.RunID) {
		return empty, rehearsalresume.ErrCheckpointBusy
	}
	if m.suspendCheckpointBusyLocked(r) {
		return empty, ErrConflict
	}
	if next == "armed" && !m.suspendCheckpointCompletedLocked(r) {
		return empty, ErrConflict
	}
	spec := rehearsalresume.Decode(m.scenarioActionPolicies[r.AppID].Member.SuspendResumeJSON)
	if next == "resuming" && pause < spec.PauseMS {
		return empty, ErrConflict
	}
	if next == "resuming" {
		proof, found := m.runtimeInstanceConfigProofs[id.InstanceID]
		instance := m.instances[id.InstanceID]
		values, err := m.runtimeAppValuesLocked(id.AccountID, id.AppID, id.DeploymentID)
		if err != nil {
			return empty, err
		}
		current, err := NewRuntimeAppConfigFence(values)
		if err != nil || !found || proof.Fence != current || proof.WakeID != instance.WakeID || proof.NodeID != instance.NodeID {
			return empty, ErrConflict
		}
	}
	updated, err := suspendStep(r, expected, next, pause, resume)
	if err != nil {
		return empty, err
	}
	m.scenarioSuspendResumes[receipt] = updated
	return spec, nil
}

func (m *MemStore) suspendExecutionAllowedLocked(id rehearsalrecorder.Identity) bool {
	for _, r := range m.scenarioSuspendResumes {
		if r.AppID == id.AppID {
			if r.State == "fenced" || r.State == "paused" || r.State == "failed" {
				return false
			}
			if _, serving := m.instances[id.InstanceID]; serving && r.InstanceID != id.InstanceID {
				return false
			}
		}
	}
	return true
}
func (m *MemStore) suspendFrozenResumeLocked(instance string) bool {
	for _, r := range m.scenarioSuspendResumes {
		if r.InstanceID == instance && (r.State == "resuming" || r.State == "resumed") {
			return true
		}
	}
	return false
}
func (m *MemStore) suspendWorkPendingLocked(app string) bool {
	p := m.scenarioActionPolicies[app]
	for _, r := range m.scenarioSuspendResumes {
		if r.AccountID == p.Member.AccountID && r.RunID == p.Member.RunID && r.State != "complete" {
			return true
		}
	}
	return false
}

var _ rehearsalresume.Store = (*MemStore)(nil)

func (m *MemStore) suspendCommandsPendingLocked(account, run string) bool {
	for _, t := range m.appTasks {
		p := m.scenarioActionPolicies[t.AppID].Member
		if p.AccountID == account && p.RunID == run && (t.Status == AppTaskQueued || t.Status == AppTaskRestoring || t.Status == AppTaskRunning) {
			return true
		}
	}
	return false
}
