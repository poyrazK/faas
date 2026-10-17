// adr: 20261017201008
package state

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioCommandStore is the durable authority shared by admission,
// scheduler publication, runtime retirement and daemon recovery.
type ScenarioCommandStore interface {
	rehearsalrecorder.CommandStore
	ScenarioRehearsalCommand(context.Context, rehearsalrecorder.Identity) (rehearsalcommand.Spec, error)
	RecordScenarioCommandNetwork(context.Context, rehearsalrecorder.Identity, string, string, string) error
	RevokeScenarioCommandNetworksForNode(context.Context, string) error
}

func commandTaskSpec(task AppTask, identity rehearsalrecorder.Identity) (rehearsalcommand.Spec, error) {
	spec, err := rehearsalcommand.Decode(task.Command)
	if err != nil || task.AccountID != identity.AccountID || task.AppID != identity.AppID || task.ID != identity.InstanceID || task.DeploymentID != identity.DeploymentID || spec.DeploymentID != task.DeploymentID || spec.RunID != identity.RunID || task.CommandShell || task.Kind != AppTaskKindManual || task.MaxOutputBytes != rehearsalcommand.MaxReceiptBytes || task.Status != AppTaskRestoring && task.Status != AppTaskRunning {
		return rehearsalcommand.Spec{}, ErrConflict
	}
	return spec, nil
}

func (m *MemStore) validateRehearsalTaskCompletionLocked(task AppTask, params CompleteAppTaskParams) error {
	if !rehearsalcommand.IsCommand(task.Command) || params.Status != AppTaskSucceeded {
		return nil
	}
	if params.OutputTruncated {
		return ErrConflict
	}
	for _, r := range m.scenarioActionPolicies[task.AppID].Recorders {
		if r.Identity.InstanceID == task.ID && r.Identity.DeploymentID == task.DeploymentID && r.Status == "closed" && r.CommandResult == params.StdoutTail {
			return nil
		}
	}
	return ErrConflict
}

// ScenarioRehearsalCommand is read independently by schedd and vmmd before
// boot. It never turns an ordinary manual task into a contained command.
func (s *PgStore) ScenarioRehearsalCommand(ctx context.Context, identity rehearsalrecorder.Identity) (rehearsalcommand.Spec, error) {
	task, err := s.AppTaskByID(ctx, identity.AccountID, identity.AppID, identity.InstanceID)
	if err != nil {
		return rehearsalcommand.Spec{}, err
	}
	spec, err := commandTaskSpec(task, identity)
	if err != nil {
		return spec, err
	}
	tx, policy, err := recorderTransaction(ctx, s, identity)
	if err != nil {
		return spec, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if policy.RecorderClosing || !policy.Active.Valid || !policy.Active.Bool || policy.DeploymentID != identity.DeploymentID {
		return spec, ErrConflict
	}
	return spec, tx.Commit(ctx)
}

func (m *MemStore) ScenarioRehearsalCommand(ctx context.Context, identity rehearsalrecorder.Identity) (rehearsalcommand.Spec, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalcommand.Spec{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(identity)
	if err != nil || p.RecorderClosing || p.SimulationFailed {
		return rehearsalcommand.Spec{}, ErrConflict
	}
	if _, ok := m.appTaskAttach[identity.InstanceID]; ok {
		return rehearsalcommand.Spec{}, ErrConflict
	}
	return commandTaskSpec(m.appTasks[identity.InstanceID], identity)
}

func (m *MemStore) rehearsalExecutionLocked(identity rehearsalrecorder.Identity) bool {
	if !m.suspendExecutionAllowedLocked(identity) {
		return false
	}
	for _, f := range m.scenarioWorkerFaults {
		if f.AppID == identity.AppID && f.InstanceID == identity.InstanceID && (f.State == "fenced" || f.State == "terminated" || f.State == "failed") {
			return false
		}
	}
	inst := m.instances[identity.InstanceID]
	if inst.AppID == identity.AppID && (identity.DeploymentID == "" || inst.DeploymentID == identity.DeploymentID) && (simulationLiveState(inst.State) || inst.State == "frozen" && m.suspendFrozenResumeLocked(inst.ID)) {
		return m.coldExecutionAllowedLocked(rehearsalrecorder.Identity{AppID: inst.AppID, InstanceID: inst.ID, DeploymentID: inst.DeploymentID})
	}
	task := m.appTasks[identity.InstanceID]
	if identity.DeploymentID == "" {
		identity.DeploymentID = task.DeploymentID
	}
	_, err := commandTaskSpec(task, identity)
	_, interactive := m.appTaskAttach[identity.InstanceID]
	return err == nil && !interactive && (task.Status == AppTaskRestoring || task.Status == AppTaskRunning)
}

func (m *MemStore) admitRehearsalCommandLocked(params CreateAppTaskParams) error {
	if m.suspendWorkPendingLocked(params.AppID) {
		return ErrConflict
	}
	p, enrolled := m.scenarioActionPolicyLocked(params.AppID)
	contained := enrolled && p.Member.NetworkMode == ScenarioNetworkContained
	if !contained && !rehearsalcommand.IsCommand(params.Command) {
		return nil
	}
	spec, err := rehearsalcommand.Decode(params.Command)
	if err != nil || !contained || p.Member.AccountID != params.AccountID || spec.RunID != p.Member.RunID || spec.DeploymentID != params.DeploymentID || p.Member.SimulationJSON == "" || p.RecorderClosing || !p.SealedAt.IsZero() || p.SimulationFailed || params.Kind != AppTaskKindManual || params.CommandShell || params.Interactive != nil || params.MaxOutputBytes != rehearsalcommand.MaxReceiptBytes || m.deployments[params.DeploymentID].Status != DeployLive {
		return fmt.Errorf("%w: contained command admission unavailable", ErrAppTaskInvalid)
	}
	count := 0
	for _, task := range m.appTasks {
		other := m.scenarioActionPolicies[task.AppID]
		if other.Member.AccountID == params.AccountID && other.Member.RunID == p.Member.RunID {
			count++
		}
	}
	if count >= rehearsalrecorder.MaxSessions {
		return ErrConflict
	}
	return nil
}

// RetireScenarioCommandRecorder is called only by the owner after physical VM
// cleanup. The exact guest receipt is later matched to scheduler finalization.
func (s *PgStore) RetireScenarioCommandRecorder(ctx context.Context, identity rehearsalrecorder.Identity, id, result string) error {
	task, err := s.AppTaskByID(ctx, identity.AccountID, identity.AppID, identity.InstanceID)
	if err != nil {
		return err
	}
	spec, err := commandTaskSpec(task, identity)
	if err != nil {
		return err
	}
	if _, err = rehearsalcommand.ParseReceipt([]byte(result), task.ID, spec); err != nil {
		return err
	}
	tx, _, err := recorderTransaction(ctx, s, identity)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().ScenarioRecorderSession(ctx, tx, recorderSessionParams(identity, id))
	if err != nil {
		return err
	}
	if row.Status != "recording" || row.Pending != 0 || row.Failed != 0 {
		return ErrConflict
	}
	n, err := sqlc.New().RetireScenarioCommandRecorder(ctx, tx, sqlc.RetireScenarioCommandRecorderParams{ID: id, Result: result})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (m *MemStore) RetireScenarioCommandRecorder(ctx context.Context, identity rehearsalrecorder.Identity, id, result string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(identity)
	if err != nil {
		return err
	}
	spec, err := commandTaskSpec(m.appTasks[identity.InstanceID], identity)
	if err != nil {
		return err
	}
	if _, err = rehearsalcommand.ParseReceipt([]byte(result), identity.InstanceID, spec); err != nil {
		return err
	}
	r, ok := p.Recorders[id]
	if !ok || r.Identity != identity || r.Status != "recording" || r.NetworkNodeID == "" || r.NetworkOpen {
		return ErrConflict
	}
	for _, request := range r.Requests {
		if request.Status != "completed" {
			return ErrConflict
		}
	}
	r.Status, r.CommandResult = "closed", result
	p.Recorders[id] = r
	m.scenarioActionPolicies[identity.AppID] = p
	return nil
}

func (s *PgStore) RecordScenarioCommandNetwork(ctx context.Context, identity rehearsalrecorder.Identity, nodeID, leaseToken, hostIP string) error {
	address, err := netip.ParseAddr(hostIP)
	if err != nil || !address.Is4() || !netip.MustParsePrefix("10.0.0.0/8").Contains(address) {
		return ErrInvalidArgument
	}
	if _, err := s.ScenarioRehearsalCommand(ctx, identity); err != nil {
		return err
	}
	tx, _, err := recorderTransaction(ctx, s, identity)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, err := sqlc.New().RecordScenarioCommandNetwork(ctx, tx, sqlc.RecordScenarioCommandNetworkParams{TaskID: identity.InstanceID, NodeID: nodeID, HostIp: hostIP, LeaseToken: leaseToken})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (m *MemStore) RecordScenarioCommandNetwork(ctx context.Context, identity rehearsalrecorder.Identity, nodeID, leaseToken, hostIP string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(identity)
	if err != nil || p.RecorderClosing || nodeID == "" {
		return ErrConflict
	}
	task := m.appTasks[identity.InstanceID]
	if _, err := commandTaskSpec(task, identity); err != nil {
		return err
	}
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || *task.LeaseToken != leaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	address, err := netip.ParseAddr(hostIP)
	if err != nil || !address.Is4() || !netip.MustParsePrefix("10.0.0.0/8").Contains(address) {
		return ErrInvalidArgument
	}
	for id, r := range p.Recorders {
		if r.Identity == identity && r.Status == "recording" && r.NetworkNodeID == "" {
			r.NetworkNodeID, r.NetworkHostIP, r.NetworkOpen = nodeID, hostIP, true
			p.Recorders[id] = r
			m.scenarioActionPolicies[identity.AppID] = p
			return nil
		}
	}
	return ErrConflict
}

func (s *PgStore) FenceScenarioCommandNetwork(ctx context.Context, identity rehearsalrecorder.Identity) error {
	task, err := s.AppTaskByID(ctx, identity.AccountID, identity.AppID, identity.InstanceID)
	if err != nil {
		return err
	}
	task.Status = AppTaskRunning // Revocation also applies to cancelled or expired owners.
	if _, err := commandTaskSpec(task, identity); err != nil {
		return err
	}
	return sqlc.New().FenceScenarioCommandNetwork(ctx, s.pool, identity.InstanceID)
}
func (m *MemStore) FenceScenarioCommandNetwork(ctx context.Context, identity rehearsalrecorder.Identity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.appTasks[identity.InstanceID]
	task.Status = AppTaskRunning
	if _, err := commandTaskSpec(task, identity); err != nil {
		return err
	}
	p := m.scenarioActionPolicies[identity.AppID]
	for id, r := range p.Recorders {
		if r.Identity == identity {
			r.NetworkOpen = false
			p.Recorders[id] = r
		}
	}
	m.scenarioActionPolicies[identity.AppID] = p
	return nil
}
func (s *PgStore) RevokeScenarioCommandNetworksForNode(ctx context.Context, nodeID string) error {
	return sqlc.New().RevokeScenarioCommandNetworksForNode(ctx, s.pool, sqlc.RevokeScenarioCommandNetworksForNodeParams{NodeID: nodeID, LocalName: DefaultLocalNodeName})
}
func (m *MemStore) RevokeScenarioCommandNetworksForNode(ctx context.Context, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for app, p := range m.scenarioActionPolicies {
		for id, r := range p.Recorders {
			if nodeID == "" || r.NetworkNodeID == nodeID {
				r.NetworkOpen = false
				p.Recorders[id] = r
			}
		}
		m.scenarioActionPolicies[app] = p
	}
	return nil
}

// LiveServiceCallersByHostIP adds leased commands and the exact original
// resuming recorder to the fresh caller lookup. These source-only identities
// never expose a command or a still-unpublished resumed VM as a serving target.
func (s *PgStore) LiveServiceCallersByHostIP(ctx context.Context, nodeName, hostIP string) ([]Instance, error) {
	instances, err := s.LiveInstancesByHostIP(ctx, nodeName, hostIP)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ScenarioCommandCallersByHostIP(ctx, s.pool, sqlc.ScenarioCommandCallersByHostIPParams{NodeName: nodeName, HostIp: hostIP})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		instances = append(instances, Instance{ID: r.InstanceID, AppID: r.AppID, DeploymentID: r.DeploymentID, HostIP: hostIP, NodeID: nodeName, State: string(StateRunning)})
	}
	resuming, err := sqlc.New().ScenarioResumingCallersByHostIP(ctx, s.pool, sqlc.ScenarioResumingCallersByHostIPParams{NodeName: nodeName, HostIp: hostIP})
	if err != nil {
		return nil, err
	}
	for _, r := range resuming {
		instances = append(instances, Instance{ID: r.InstanceID, AppID: r.AppID, DeploymentID: r.DeploymentID, HostIP: hostIP, NodeID: nodeName, State: string(StateRunning)})
	}
	return instances, nil
}
