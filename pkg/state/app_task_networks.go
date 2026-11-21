package state

import (
	"context"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AppTaskNetworkStore records the host address of a restored app task VM so
// gatewayd-internal resolves its service-mesh calls to the task's app and
// deployment (ADR-20261118070000). schedd records the address while it holds
// the task's restoring lease and retires it before destroying the VM; the
// caller lookup also requires the task to be running under that lease, so an
// expired or cancelled task stops being the app at once. Rehearsal commands
// keep their contained identity (scenario_command_networks) instead.
type AppTaskNetworkStore interface {
	RecordAppTaskNetwork(ctx context.Context, taskID, nodeID, leaseToken, hostIP string) error
	RetireAppTaskNetwork(ctx context.Context, taskID string) error
}

var (
	_ AppTaskNetworkStore = (*PgStore)(nil)
	_ AppTaskNetworkStore = (*MemStore)(nil)
)

type appTaskNetwork struct {
	nodeID, hostIP, leaseToken string
	active                     bool
}

var appTaskNetworkRange = netip.MustParsePrefix("10.0.0.0/8")

func validAppTaskHostIP(hostIP string) bool {
	address, err := netip.ParseAddr(hostIP)
	return err == nil && address.Is4() && appTaskNetworkRange.Contains(address)
}

// RecordAppTaskNetwork records hostIP for a task in AppTaskRestoring under
// leaseToken. It returns ErrConflict when the lease is no longer the
// restoring owner's or the task already has an address.
func (s *PgStore) RecordAppTaskNetwork(ctx context.Context, taskID, nodeID, leaseToken, hostIP string) error {
	if !validAppTaskHostIP(hostIP) || taskID == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().RecordAppTaskNetwork(ctx, s.pool, sqlc.RecordAppTaskNetworkParams{
		HostIp: hostIP, NodeID: nodeID, LocalName: DefaultLocalNodeName, TaskID: taskID, LeaseToken: leaseToken,
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) RetireAppTaskNetwork(ctx context.Context, taskID string) error {
	return sqlc.New().RetireAppTaskNetwork(ctx, s.pool, taskID)
}

func (s *PgStore) appTaskCallersByHostIP(ctx context.Context, nodeName, hostIP string) ([]Instance, error) {
	rows, err := sqlc.New().AppTaskCallersByHostIP(ctx, s.pool, sqlc.AppTaskCallersByHostIPParams{HostIp: hostIP, NodeName: nodeName})
	if err != nil {
		return nil, err
	}
	callers := make([]Instance, 0, len(rows))
	for _, r := range rows {
		callers = append(callers, Instance{ID: r.InstanceID, AppID: r.AppID, DeploymentID: r.DeploymentID, HostIP: hostIP, NodeID: nodeName, State: string(StateRunning)})
	}
	return callers, nil
}

func (m *MemStore) RecordAppTaskNetwork(ctx context.Context, taskID, nodeID, leaseToken, hostIP string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validAppTaskHostIP(hostIP) || taskID == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[taskID]
	if !ok || task.Status != AppTaskRestoring || task.LeaseToken == nil || *task.LeaseToken != leaseToken ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) || task.CancelRequested != nil ||
		rehearsalcommand.IsCommand(task.Command) {
		return ErrConflict
	}
	if _, exists := m.appTaskNetworks[taskID]; exists {
		return ErrConflict
	}
	if m.appTaskNetworks == nil {
		m.appTaskNetworks = map[string]appTaskNetwork{}
	}
	m.appTaskNetworks[taskID] = appTaskNetwork{nodeID: nodeID, hostIP: hostIP, leaseToken: leaseToken, active: true}
	return nil
}

func (m *MemStore) RetireAppTaskNetwork(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.appTaskNetworks[taskID]; ok {
		n.active = false
		m.appTaskNetworks[taskID] = n
	}
	return nil
}

// AppTaskCallersByHostIP is the MemStore half of LiveServiceCallersByHostIP's
// app task lookup, for tests of the sched and gateway wiring.
func (m *MemStore) AppTaskCallersByHostIP(ctx context.Context, nodeID, hostIP string) ([]Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var callers []Instance
	for id, n := range m.appTaskNetworks {
		task := m.appTasks[id]
		if !n.active || n.hostIP != hostIP || (nodeID != "" && n.nodeID != nodeID) || task.Status != AppTaskRunning ||
			task.LeaseToken == nil || *task.LeaseToken != n.leaseToken || task.LeaseExpiresAt == nil ||
			!task.LeaseExpiresAt.After(time.Now()) || task.CancelRequested != nil {
			continue
		}
		if app, ok := m.apps[task.AppID]; !ok || app.Status == AppDeleted {
			continue
		}
		callers = append(callers, Instance{ID: id, AppID: task.AppID, DeploymentID: task.DeploymentID, HostIP: hostIP, NodeID: n.nodeID, State: string(StateRunning)})
	}
	return callers, nil
}
