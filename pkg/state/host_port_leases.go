package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/hostport"
)

// HostPortLeaseStore is intentionally separate from Store. It lets older
// state.Store adapters keep compiling while the scheduler can opt into the
// durable host-port registry when the backing store supports it.
type HostPortLeaseStore interface {
	AcquireHostPortLeases(ctx context.Context, nodeID, instanceID string, requests []hostport.Request) ([]hostport.Lease, error)
	ReleaseHostPortLeases(ctx context.Context, nodeID, instanceID string) error
	ListHostPortLeases(ctx context.Context, nodeID, instanceID string) ([]hostport.Lease, error)
	ReconcileHostPortLeases(ctx context.Context) error
}

var _ HostPortLeaseStore = (*MemStore)(nil)
var _ HostPortLeaseStore = (*PgStore)(nil)

func (m *MemStore) hostPortAllocator() *hostport.Allocator {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostPorts == nil {
		m.hostPorts = hostport.NewDefaultAllocator()
	}
	return m.hostPorts
}

func (m *MemStore) AcquireHostPortLeases(ctx context.Context, nodeID, instanceID string, requests []hostport.Request) ([]hostport.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.hostPortAllocator().Acquire(nodeID, instanceID, requests)
}

func (m *MemStore) ReleaseHostPortLeases(ctx context.Context, nodeID, instanceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.hostPortAllocator().Release(nodeID, instanceID)
	return nil
}

func (m *MemStore) ListHostPortLeases(ctx context.Context, nodeID, instanceID string) ([]hostport.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.hostPortAllocator().List(nodeID, instanceID), nil
}

// MemStore has no separate instance table reader in the allocator; scheduler
// tests release rows through the same transition hooks as production. Keeping
// this method a no-op preserves the durable-store contract for local fixtures.
func (m *MemStore) ReconcileHostPortLeases(ctx context.Context) error {
	return ctx.Err()
}

func validateHostPortRequests(requests []hostport.Request) ([]hostport.Request, error) {
	canonical, err := hostport.CanonicalRequests(requests)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func hostPortLeaseError(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("state: %s host-port leases: %w", op, err)
}

func (s *PgStore) AcquireHostPortLeases(ctx context.Context, nodeID, instanceID string, requests []hostport.Request) ([]hostport.Lease, error) {
	canonical, err := validateHostPortRequests(requests)
	if err != nil {
		return nil, hostPortLeaseError("validate", err)
	}
	if len(canonical) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, hostPortLeaseError("begin", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	result := make([]hostport.Lease, 0, len(canonical))
	for _, req := range canonical {
		var hostPort, guestPort int
		var createdAt time.Time
		err := tx.QueryRow(ctx, `
			SELECT host_port, guest_port, created_at
			  FROM container_host_port_leases
			 WHERE node_id = $1 AND instance_id = $2
			   AND listener_name = $3 AND protocol = $4
			 FOR UPDATE`, nodeID, instanceID, req.Name, string(req.Protocol)).Scan(&hostPort, &guestPort, &createdAt)
		if err == nil {
			if guestPort != req.GuestPort {
				return nil, hostPortLeaseError("acquire", fmt.Errorf("%w: listener %q guest port changed", hostport.ErrInvalidRequest, req.Name))
			}
			result = append(result, hostport.Lease{
				NodeID: nodeID, InstanceID: instanceID, ListenerName: req.Name,
				Protocol: req.Protocol, GuestPort: guestPort, HostPort: hostPort, CreatedAt: createdAt,
			})
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, hostPortLeaseError("lookup", err)
		}

		allocated := false
		for candidate := hostport.DefaultStartPort; candidate <= hostport.DefaultEndPort; candidate++ {
			var insertedAt time.Time
			insertErr := tx.QueryRow(ctx, `
				INSERT INTO container_host_port_leases
				    (node_id, instance_id, listener_name, protocol, guest_port, host_port)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT DO NOTHING
				RETURNING created_at`,
				nodeID, instanceID, req.Name, string(req.Protocol), req.GuestPort, candidate).Scan(&insertedAt)
			if insertErr == nil {
				result = append(result, hostport.Lease{
					NodeID: nodeID, InstanceID: instanceID, ListenerName: req.Name,
					Protocol: req.Protocol, GuestPort: req.GuestPort, HostPort: candidate, CreatedAt: insertedAt,
				})
				allocated = true
				break
			}
			if !errors.Is(insertErr, pgx.ErrNoRows) {
				return nil, hostPortLeaseError("allocate", insertErr)
			}
			// A concurrent retry may have inserted this exact listener while
			// the first lookup was running. Re-read it before trying the next
			// host port so the unique listener key remains idempotent.
			var existingPort, existingGuest int
			var existingAt time.Time
			if lookupErr := tx.QueryRow(ctx, `
				SELECT host_port, guest_port, created_at
				  FROM container_host_port_leases
				 WHERE node_id = $1 AND instance_id = $2
				   AND listener_name = $3 AND protocol = $4
				 FOR UPDATE`, nodeID, instanceID, req.Name, string(req.Protocol)).Scan(&existingPort, &existingGuest, &existingAt); lookupErr == nil {
				if existingGuest != req.GuestPort {
					return nil, hostPortLeaseError("acquire", fmt.Errorf("%w: listener %q guest port changed", hostport.ErrInvalidRequest, req.Name))
				}
				result = append(result, hostport.Lease{
					NodeID: nodeID, InstanceID: instanceID, ListenerName: req.Name,
					Protocol: req.Protocol, GuestPort: existingGuest, HostPort: existingPort, CreatedAt: existingAt,
				})
				allocated = true
				break
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return nil, hostPortLeaseError("lookup after conflict", lookupErr)
			}
		}
		if !allocated {
			return nil, hostPortLeaseError("allocate", fmt.Errorf("%w: node=%s protocol=%s", hostport.ErrExhausted, nodeID, req.Protocol))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, hostPortLeaseError("commit", err)
	}
	return result, nil
}

func (s *PgStore) ReleaseHostPortLeases(ctx context.Context, nodeID, instanceID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM container_host_port_leases WHERE node_id = $1 AND instance_id = $2`, nodeID, instanceID); err != nil {
		return hostPortLeaseError("release", err)
	}
	return nil
}

func (s *PgStore) ListHostPortLeases(ctx context.Context, nodeID, instanceID string) ([]hostport.Lease, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT node_id, instance_id, listener_name, protocol, guest_port, host_port, created_at
		  FROM container_host_port_leases
		 WHERE ($1 = '' OR node_id::text = $1)
		   AND ($2 = '' OR instance_id::text = $2)
		 ORDER BY node_id, protocol, host_port`, nodeID, instanceID)
	if err != nil {
		return nil, hostPortLeaseError("list", err)
	}
	defer rows.Close()
	result := make([]hostport.Lease, 0)
	for rows.Next() {
		var lease hostport.Lease
		var protocol string
		if err := rows.Scan(&lease.NodeID, &lease.InstanceID, &lease.ListenerName, &protocol, &lease.GuestPort, &lease.HostPort, &lease.CreatedAt); err != nil {
			return nil, hostPortLeaseError("list scan", err)
		}
		lease.Protocol = hostport.Protocol(protocol)
		result = append(result, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, hostPortLeaseError("list rows", err)
	}
	return result, nil
}

func (s *PgStore) ReconcileHostPortLeases(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM container_host_port_leases lease
		 WHERE NOT EXISTS (
			SELECT 1
			  FROM instances instance
			 WHERE instance.id = lease.instance_id
			   AND instance.state IN ('waking', 'cold_booting', 'running', 'snapshotting', 'migrating')
		 )`)
	if err != nil {
		return hostPortLeaseError("reconcile", err)
	}
	return nil
}
