package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) UpsertManagedRealtimePresenceLease(ctx context.Context, lease ManagedRealtimePresenceLease) ([]ManagedRealtimePresenceLease, error) {
	if err := validateManagedRealtimePresenceLease(lease); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: begin managed realtime presence upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_presence_leases
		where endpoint_id = $1 and channel = $2 and expires_at <= clock_timestamp()
	`, lease.EndpointID, lease.Channel); err != nil {
		return nil, fmt.Errorf("state: expire managed realtime presence leases: %w", err)
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1), hashtext($2))`, lease.EndpointID, lease.Channel); err != nil {
		return nil, fmt.Errorf("state: lock managed realtime presence channel: %w", err)
	}
	if lease.Principal != "" {
		err := tx.QueryRow(ctx, `
			select member_id from managed_realtime_presence_leases
			where endpoint_id = $1 and channel = $2 and principal = $3 and expires_at > clock_timestamp()
		order by updated_at desc
		limit 1
		`, lease.EndpointID, lease.Channel, lease.Principal).Scan(&lease.MemberID)
		if err != nil && err != pgx.ErrNoRows {
			return nil, fmt.Errorf("state: find managed realtime principal presence: %w", err)
		}
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from managed_realtime_presence_leases
		              where endpoint_id = $1 and channel = $2 and node_id = $3 and connection_id = $4)
	`, lease.EndpointID, lease.Channel, lease.NodeID, lease.ConnectionID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("state: check managed realtime presence lease: %w", err)
	}
	if !exists {
		var count int
		if err := tx.QueryRow(ctx, `
			select count(*) from managed_realtime_presence_leases
			where endpoint_id = $1 and channel = $2 and expires_at > clock_timestamp()
		`, lease.EndpointID, lease.Channel).Scan(&count); err != nil {
			return nil, fmt.Errorf("state: count managed realtime presence members: %w", err)
		}
		if count >= ManagedRealtimePresenceMaxMembers {
			return nil, ErrManagedRealtimePresenceLimit
		}
	}
	if err := tx.QueryRow(ctx, `
		insert into managed_realtime_presence_leases
			(endpoint_id, channel, node_id, connection_id, member_id, principal, state, expires_at, updated_at, state_updated_at)
		values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, clock_timestamp(), clock_timestamp())
		on conflict (endpoint_id, channel, node_id, connection_id) do update
		set member_id = excluded.member_id, principal = excluded.principal, state = excluded.state,
		    expires_at = excluded.expires_at, updated_at = clock_timestamp(),
		    state_updated_at = case
		      when managed_realtime_presence_leases.member_id is distinct from excluded.member_id
		        or managed_realtime_presence_leases.principal is distinct from excluded.principal
		        or managed_realtime_presence_leases.state is distinct from excluded.state
		      then clock_timestamp()
		      else managed_realtime_presence_leases.state_updated_at
		    end
		returning state_updated_at
	`, lease.EndpointID, lease.Channel, lease.NodeID, lease.ConnectionID, lease.MemberID, lease.Principal, lease.State, lease.ExpiresAt).Scan(&lease.StateUpdatedAt); err != nil {
		return nil, fmt.Errorf("state: upsert managed realtime presence lease: %w", err)
	}
	rows, err := readManagedRealtimePresenceSnapshot(ctx, tx, lease.EndpointID, lease.Channel)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit managed realtime presence upsert: %w", err)
	}
	return rows, nil
}

func (s *PgStore) ReadManagedRealtimePresenceSnapshot(ctx context.Context, endpointID, channel string) ([]ManagedRealtimePresenceLease, error) {
	if validateManagedRealtimeHistoryRequest(endpointID, channel) != nil {
		return nil, ErrManagedRealtimePresenceInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("state: begin managed realtime presence snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_presence_leases
		where endpoint_id = $1 and channel = $2 and expires_at <= clock_timestamp()
	`, endpointID, channel); err != nil {
		return nil, fmt.Errorf("state: expire managed realtime presence snapshot: %w", err)
	}
	rows, err := readManagedRealtimePresenceSnapshot(ctx, tx, endpointID, channel)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit managed realtime presence snapshot: %w", err)
	}
	return rows, nil
}

func (s *PgStore) DeleteManagedRealtimePresenceLease(ctx context.Context, endpointID, channel, nodeID, connectionID string) ([]ManagedRealtimePresenceLease, time.Time, error) {
	if err := validateManagedRealtimePresenceKey(endpointID, channel, nodeID, connectionID); err != nil {
		return nil, time.Time{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("state: begin managed realtime presence delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1), hashtext($2))`, endpointID, channel); err != nil {
		return nil, time.Time{}, fmt.Errorf("state: lock managed realtime presence channel for delete: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_presence_leases
		where endpoint_id = $1 and channel = $2 and node_id = $3 and connection_id = $4
	`, endpointID, channel, nodeID, connectionID); err != nil {
		return nil, time.Time{}, fmt.Errorf("state: delete managed realtime presence lease: %w", err)
	}
	var observedAt time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&observedAt); err != nil {
		return nil, time.Time{}, fmt.Errorf("state: timestamp managed realtime presence delete: %w", err)
	}
	rows, err := readManagedRealtimePresenceSnapshot(ctx, tx, endpointID, channel)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, time.Time{}, fmt.Errorf("state: commit managed realtime presence delete: %w", err)
	}
	return rows, observedAt, nil
}

func (s *PgStore) PruneExpiredManagedRealtimePresenceLeases(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimePresenceInvalid
	}
	result, err := s.pool.Exec(ctx, `
		delete from managed_realtime_presence_leases
		where ctid in (
			select ctid from managed_realtime_presence_leases
			where expires_at <= clock_timestamp()
			order by expires_at
			limit $1 for update skip locked
		)
	`, batch)
	if err != nil {
		return 0, fmt.Errorf("state: prune expired managed realtime presence leases: %w", err)
	}
	return result.RowsAffected(), nil
}

type managedRealtimePresenceQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readManagedRealtimePresenceSnapshot(ctx context.Context, q managedRealtimePresenceQuerier, endpointID, channel string) ([]ManagedRealtimePresenceLease, error) {
	rows, err := q.Query(ctx, `
		with snapshot as (select clock_timestamp() as observed_at),
		active as (
			select endpoint_id::text as endpoint_id, channel, node_id::text as node_id, connection_id,
			       member_id, principal, state::text as state, expires_at, state_updated_at,
			       snapshot.observed_at,
			       count(*) over (partition by member_id) as connection_count,
			       row_number() over (partition by member_id order by state_updated_at desc, node_id, connection_id) as member_rank
			from managed_realtime_presence_leases cross join snapshot
			where endpoint_id = $1 and channel = $2 and expires_at > clock_timestamp()
		)
		select endpoint_id, channel, node_id, connection_id, member_id, principal,
		       state, expires_at, observed_at, state_updated_at, connection_count
		from active where member_rank = 1 order by member_id
	`, endpointID, channel)
	if err != nil {
		return nil, fmt.Errorf("state: read managed realtime presence snapshot: %w", err)
	}
	defer rows.Close()
	result := make([]ManagedRealtimePresenceLease, 0)
	for rows.Next() {
		var lease ManagedRealtimePresenceLease
		if err := rows.Scan(&lease.EndpointID, &lease.Channel, &lease.NodeID, &lease.ConnectionID,
			&lease.MemberID, &lease.Principal, &lease.State, &lease.ExpiresAt, &lease.UpdatedAt,
			&lease.StateUpdatedAt, &lease.ConnectionCount); err != nil {
			return nil, fmt.Errorf("state: scan managed realtime presence snapshot: %w", err)
		}
		lease.NodeID = ""
		lease.ConnectionID = ""
		result = append(result, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate managed realtime presence snapshot: %w", err)
	}
	return result, nil
}
