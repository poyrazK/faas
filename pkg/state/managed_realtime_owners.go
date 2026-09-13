package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/dispatch"
)

// ErrManagedRealtimeOwnerConflict means another live lease owns the
// connection. Callers should re-read the directory and retry discovery rather
// than replacing a lease that is still valid.
var ErrManagedRealtimeOwnerConflict = dispatch.ErrLeaseConflict

// ManagedRealtimeConnectionOwner is the durable directory projection used by
// apid to route connection operations. The token is opaque and must be
// presented to renew or release the lease.
type ManagedRealtimeConnectionOwner struct {
	ConnectionID   string
	EndpointID     string
	NodeID         string
	LeaseToken     string
	LeaseExpiresAt time.Time
	UpdatedAt      time.Time
}

// ManagedRealtimeConnectionOwnerStore is deliberately separate from Store so
// older integrations and test doubles remain source-compatible. PgStore and
// MemStore implement it; the owner resolver fails closed when it is absent.
type ManagedRealtimeConnectionOwnerStore interface {
	ClaimManagedRealtimeConnectionOwner(context.Context, string, string, string, time.Duration) (ManagedRealtimeConnectionOwner, error)
	GetManagedRealtimeConnectionOwner(context.Context, string, string) (ManagedRealtimeConnectionOwner, error)
	RenewManagedRealtimeConnectionOwner(context.Context, string, string, string, time.Duration) (ManagedRealtimeConnectionOwner, error)
	ReleaseManagedRealtimeConnectionOwner(context.Context, string, string) error
}

func validateRealtimeOwnerLeaseInput(connectionID, endpointID, nodeID string, ttl time.Duration) error {
	if connectionID == "" || endpointID == "" || nodeID == "" || ttl <= 0 {
		return errors.New("state: connection, endpoint, node, and positive lease TTL are required")
	}
	return nil
}

func scanManagedRealtimeConnectionOwner(row pgx.Row) (ManagedRealtimeConnectionOwner, error) {
	var out ManagedRealtimeConnectionOwner
	if err := row.Scan(&out.ConnectionID, &out.EndpointID, &out.NodeID, &out.LeaseToken, &out.LeaseExpiresAt, &out.UpdatedAt); err != nil {
		return ManagedRealtimeConnectionOwner{}, err
	}
	return out, nil
}

// ClaimManagedRealtimeConnectionOwner acquires an unowned/expired directory
// row using a fresh token. A live lease is never overwritten, even when the
// requested node differs.
func (s *PgStore) ClaimManagedRealtimeConnectionOwner(ctx context.Context, connectionID, endpointID, nodeID string, ttl time.Duration) (ManagedRealtimeConnectionOwner, error) {
	if err := validateRealtimeOwnerLeaseInput(connectionID, endpointID, nodeID, ttl); err != nil {
		return ManagedRealtimeConnectionOwner{}, err
	}
	token := uuid.NewString()
	row := s.pool.QueryRow(ctx, `
		insert into managed_realtime_connection_owners
		    (connection_id, endpoint_id, node_id, lease_token, lease_expires_at, updated_at)
		values ($1, $2, $3, $4, now() + ($5 * interval '1 millisecond'), now())
		on conflict (connection_id) do update
		  set endpoint_id = excluded.endpoint_id,
		      node_id = excluded.node_id,
		      lease_token = excluded.lease_token,
		      lease_expires_at = excluded.lease_expires_at,
		      updated_at = now()
		where managed_realtime_connection_owners.lease_expires_at <= now()
		returning connection_id, endpoint_id, node_id, lease_token, lease_expires_at, updated_at`,
		connectionID, endpointID, nodeID, token, ttl.Milliseconds())
	out, err := scanManagedRealtimeConnectionOwner(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeConnectionOwner{}, ErrManagedRealtimeOwnerConflict
	}
	if err != nil {
		return ManagedRealtimeConnectionOwner{}, fmt.Errorf("state: claim managed realtime owner: %w", err)
	}
	return out, nil
}

func (s *PgStore) GetManagedRealtimeConnectionOwner(ctx context.Context, connectionID, endpointID string) (ManagedRealtimeConnectionOwner, error) {
	if connectionID == "" || endpointID == "" {
		return ManagedRealtimeConnectionOwner{}, ErrNotFound
	}
	out, err := scanManagedRealtimeConnectionOwner(s.pool.QueryRow(ctx, `
		select connection_id, endpoint_id, node_id, lease_token, lease_expires_at, updated_at
		  from managed_realtime_connection_owners
		 where connection_id = $1 and endpoint_id = $2 and lease_expires_at > now()`, connectionID, endpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeConnectionOwner{}, ErrNotFound
	}
	if err != nil {
		return ManagedRealtimeConnectionOwner{}, fmt.Errorf("state: get managed realtime owner: %w", err)
	}
	return out, nil
}

func (s *PgStore) RenewManagedRealtimeConnectionOwner(ctx context.Context, connectionID, endpointID, token string, ttl time.Duration) (ManagedRealtimeConnectionOwner, error) {
	if connectionID == "" || endpointID == "" || token == "" || ttl <= 0 {
		return ManagedRealtimeConnectionOwner{}, errors.New("state: connection, endpoint, token, and positive lease TTL are required")
	}
	out, err := scanManagedRealtimeConnectionOwner(s.pool.QueryRow(ctx, `
		update managed_realtime_connection_owners
		   set lease_expires_at = now() + ($4 * interval '1 millisecond'), updated_at = now()
		 where connection_id = $1 and endpoint_id = $2 and lease_token = $3
		   and lease_expires_at > now()
		returning connection_id, endpoint_id, node_id, lease_token, lease_expires_at, updated_at`, connectionID, endpointID, token, ttl.Milliseconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeConnectionOwner{}, ErrManagedRealtimeOwnerConflict
	}
	if err != nil {
		return ManagedRealtimeConnectionOwner{}, fmt.Errorf("state: renew managed realtime owner: %w", err)
	}
	return out, nil
}

func (s *PgStore) ReleaseManagedRealtimeConnectionOwner(ctx context.Context, connectionID, token string) error {
	if connectionID == "" || token == "" {
		return errors.New("state: connection and token are required")
	}
	tag, err := s.pool.Exec(ctx, `
		delete from managed_realtime_connection_owners
		 where connection_id = $1 and lease_token = $2`, connectionID, token)
	if err != nil {
		return fmt.Errorf("state: release managed realtime owner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrManagedRealtimeOwnerConflict
	}
	return nil
}
