package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrMigrationLeaseExpired distinguishes a live lease from one that can no
// longer be renewed. It stays in state (rather than importing sched) so the
// storage layer remains independent of scheduler policy.
var ErrMigrationLeaseExpired = errors.New("state: migration lease expired")

// MigrationLease is the durable source-side handoff record for one paused
// instance.  The instance row remains the Phase-3 ownership CAS; this record
// carries the snapshot handles needed to finish or roll back the handoff after
// vmmd restarts.
type MigrationLease struct {
	InstanceID        string
	LeaseToken        string
	SourceNodeID      string
	CreatedAt         time.Time
	LeaseExpiresAt    time.Time
	MemStorageKey     string
	VMStateStorageKey string
	Pending           bool
}

// MigrationLeaseStore is optional on state.Store so older fixtures and
// default-local deployments remain source-compatible.  PgStore and MemStore
// implement it; vmmd opts in through Server.WithMigrationStore.
type MigrationLeaseStore interface {
	ReserveMigrationLease(ctx context.Context, lease MigrationLease) error
	CompleteMigrationLease(ctx context.Context, leaseToken, memStorageKey, vmstateStorageKey string) error
	GetMigrationLease(ctx context.Context, instanceID, leaseToken string) (MigrationLease, error)
	GetMigrationLeaseByToken(ctx context.Context, leaseToken string) (MigrationLease, error)
	RenewMigrationLease(ctx context.Context, leaseToken, sourceNodeID string, leaseExpiresAt time.Time) error
	DeleteMigrationLease(ctx context.Context, leaseToken string) error
	ListExpiredMigrationLeases(ctx context.Context, now time.Time) ([]MigrationLease, error)
}

// ReserveMigrationLease creates a lease unless the instance already has a
// live lease. Expired rows are replaced atomically so a crashed source can be
// retried without a manual cleanup step.
func (s *PgStore) ReserveMigrationLease(ctx context.Context, lease MigrationLease) error {
	if lease.InstanceID == "" || lease.LeaseToken == "" {
		return fmt.Errorf("state: reserve migration lease: instance_id and lease_token are required")
	}
	tag, err := s.pool.Exec(ctx, `
		insert into migration_leases
		       (instance_id, lease_token, source_node_id, created_at,
		        lease_expires_at, mem_storage_key, vmstate_storage_key, pending)
		values ($1::uuid, $2, $3, $4, $5, '', '', true)
		on conflict (instance_id) do update
		   set lease_token = excluded.lease_token,
		       source_node_id = excluded.source_node_id,
		       created_at = excluded.created_at,
		       lease_expires_at = excluded.lease_expires_at,
		       mem_storage_key = '',
		       vmstate_storage_key = '',
		       pending = true
		 where migration_leases.lease_expires_at <= now()
	`, lease.InstanceID, lease.LeaseToken, lease.SourceNodeID,
		lease.CreatedAt.UTC(), lease.LeaseExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("state: reserve migration lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// CompleteMigrationLease records the snapshot handles after the source VM is
// paused. The token guard prevents a stale Phase-1 completion from replacing a
// newer lease for the same instance.
func (s *PgStore) CompleteMigrationLease(ctx context.Context, leaseToken, memStorageKey, vmstateStorageKey string) error {
	tag, err := s.pool.Exec(ctx, `
		update migration_leases
		   set mem_storage_key = $2,
		       vmstate_storage_key = $3,
		       pending = false
		 where lease_token = $1`, leaseToken, memStorageKey, vmstateStorageKey)
	if err != nil {
		return fmt.Errorf("state: complete migration lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMigrationLease returns a lease even after expiry. Cleanup needs the
// snapshot handles and token to make the source VM safe before deleting it.
func (s *PgStore) GetMigrationLease(ctx context.Context, instanceID, leaseToken string) (MigrationLease, error) {
	var lease MigrationLease
	err := s.pool.QueryRow(ctx, `
		select instance_id::text, lease_token, source_node_id, created_at,
		       lease_expires_at, mem_storage_key, vmstate_storage_key, pending
		  from migration_leases
		 where instance_id = $1::uuid and lease_token = $2`, instanceID, leaseToken).Scan(
		&lease.InstanceID, &lease.LeaseToken, &lease.SourceNodeID, &lease.CreatedAt,
		&lease.LeaseExpiresAt, &lease.MemStorageKey, &lease.VMStateStorageKey, &lease.Pending)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MigrationLease{}, ErrNotFound
		}
		return MigrationLease{}, fmt.Errorf("state: get migration lease: %w", err)
	}
	return lease, nil
}

func (s *PgStore) GetMigrationLeaseByToken(ctx context.Context, leaseToken string) (MigrationLease, error) {
	var lease MigrationLease
	err := s.pool.QueryRow(ctx, `
		select instance_id::text, lease_token, source_node_id, created_at,
		       lease_expires_at, mem_storage_key, vmstate_storage_key, pending
		  from migration_leases where lease_token = $1`, leaseToken).Scan(
		&lease.InstanceID, &lease.LeaseToken, &lease.SourceNodeID, &lease.CreatedAt,
		&lease.LeaseExpiresAt, &lease.MemStorageKey, &lease.VMStateStorageKey, &lease.Pending)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MigrationLease{}, ErrNotFound
		}
		return MigrationLease{}, fmt.Errorf("state: get migration lease by token: %w", err)
	}
	return lease, nil
}

func (s *PgStore) RenewMigrationLease(ctx context.Context, leaseToken, sourceNodeID string, leaseExpiresAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		update migration_leases
		   set lease_expires_at = $3
		 where lease_token = $1 and source_node_id = $2 and lease_expires_at > now()`,
		leaseToken, sourceNodeID, leaseExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("state: renew migration lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		lease, lookupErr := s.GetMigrationLeaseByToken(ctx, leaseToken)
		if errors.Is(lookupErr, ErrNotFound) {
			return ErrNotFound
		}
		if lookupErr != nil {
			return lookupErr
		}
		if lease.SourceNodeID != sourceNodeID {
			return ErrConflict
		}
		return ErrMigrationLeaseExpired
	}
	return nil
}

// DeleteMigrationLease removes a lease by token. Idempotent callers map a
// missing row to ErrNotFound, just like the in-memory tracker.
func (s *PgStore) DeleteMigrationLease(ctx context.Context, leaseToken string) error {
	tag, err := s.pool.Exec(ctx, `delete from migration_leases where lease_token = $1`, leaseToken)
	if err != nil {
		return fmt.Errorf("state: delete migration lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListExpiredMigrationLeases returns all expired leases, including pending
// Phase-1 records. A restart can therefore resume a source VM even if it died
// between the snapshot call and the completion update.
func (s *PgStore) ListExpiredMigrationLeases(ctx context.Context, now time.Time) ([]MigrationLease, error) {
	rows, err := s.pool.Query(ctx, `
		select instance_id::text, lease_token, source_node_id, created_at,
		       lease_expires_at, mem_storage_key, vmstate_storage_key, pending
		  from migration_leases
		 where lease_expires_at < $1
		 order by lease_expires_at, instance_id`, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("state: list expired migration leases: %w", err)
	}
	defer rows.Close()
	var out []MigrationLease
	for rows.Next() {
		var lease MigrationLease
		if err := rows.Scan(&lease.InstanceID, &lease.LeaseToken, &lease.SourceNodeID,
			&lease.CreatedAt, &lease.LeaseExpiresAt, &lease.MemStorageKey,
			&lease.VMStateStorageKey, &lease.Pending); err != nil {
			return nil, fmt.Errorf("state: scan expired migration lease: %w", err)
		}
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list expired migration leases: %w", err)
	}
	return out, nil
}

var _ MigrationLeaseStore = (*PgStore)(nil)
