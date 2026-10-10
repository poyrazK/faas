package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) LoadManagedRealtimeDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, initialSequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, initialSequence); err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin realtime durable cursor load: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedID string
	if err := tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id = $1 for update`, endpointID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("state: lock realtime endpoint for durable cursor: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_durable_cursors
		where endpoint_id = $1 and updated_at < clock_timestamp() - make_interval(secs => $2::double precision)
	`, endpointID, ManagedRealtimeDurableCursorRetention.Seconds()); err != nil {
		return 0, fmt.Errorf("state: expire realtime durable cursors: %w", err)
	}
	var sequence int64
	err = tx.QueryRow(ctx, `
		select sequence from managed_realtime_durable_cursors
		where endpoint_id = $1 and principal = $2 and subscription = $3 and channel = $4
		for update
	`, endpointID, principal, subscription, channel).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		var latest int64
		err := tx.QueryRow(ctx, `
			select next_sequence - 1 from managed_realtime_channel_heads
			where endpoint_id = $1 and channel = $2
		`, endpointID, channel).Scan(&latest)
		if errors.Is(err, pgx.ErrNoRows) {
			if initialSequence > 0 {
				return 0, ErrManagedRealtimeHistoryInvalid
			}
		} else if err != nil {
			return 0, fmt.Errorf("state: validate realtime durable cursor baseline: %w", err)
		} else if initialSequence > latest {
			return 0, ErrManagedRealtimeHistoryInvalid
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from managed_realtime_durable_cursors where endpoint_id = $1`, endpointID).Scan(&count); err != nil {
			return 0, fmt.Errorf("state: count realtime durable cursors: %w", err)
		}
		if count >= ManagedRealtimeDurableCursorMaxPerEndpoint {
			return 0, ErrManagedRealtimeDurableCursorLimit
		}
		if err := tx.QueryRow(ctx, `
			insert into managed_realtime_durable_cursors(endpoint_id, principal, subscription, channel, sequence)
			values ($1, $2, $3, $4, $5) returning sequence
		`, endpointID, principal, subscription, channel, initialSequence).Scan(&sequence); err != nil {
			return 0, fmt.Errorf("state: create realtime durable cursor: %w", err)
		}
	} else if err != nil {
		return 0, fmt.Errorf("state: read realtime durable cursor: %w", err)
	} else {
		if _, err := tx.Exec(ctx, `
			update managed_realtime_durable_cursors set updated_at = clock_timestamp()
			where endpoint_id = $1 and principal = $2 and subscription = $3 and channel = $4
		`, endpointID, principal, subscription, channel); err != nil {
			return 0, fmt.Errorf("state: refresh realtime durable cursor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit realtime durable cursor load: %w", err)
	}
	return sequence, nil
}

func (s *PgStore) AdvanceManagedRealtimeDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, sequence); err != nil {
		return 0, err
	}
	var current int64
	err := s.pool.QueryRow(ctx, `
		update managed_realtime_durable_cursors
		set sequence = greatest(sequence, $5), updated_at = clock_timestamp()
		where endpoint_id = $1 and principal = $2 and subscription = $3 and channel = $4
		returning sequence
	`, endpointID, principal, subscription, channel, sequence).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("state: advance realtime durable cursor: %w", err)
	}
	return current, nil
}

func (s *PgStore) ResetManagedRealtimeDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, sequence); err != nil {
		return 0, err
	}
	history, err := s.ReadManagedRealtimeChannelHistory(ctx, endpointID, channel, sequence, 1)
	if errors.Is(err, ErrManagedRealtimeHistoryInvalid) {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("state: validate realtime durable cursor reset: %w", err)
	}
	if history.HistoryUnavailable {
		return 0, ErrManagedRealtimeDurableCursorExpired
	}
	var current int64
	err = s.pool.QueryRow(ctx, `
		update managed_realtime_durable_cursors
		set sequence = $5, updated_at = clock_timestamp()
		where endpoint_id = $1 and principal = $2 and subscription = $3 and channel = $4
		returning sequence
	`, endpointID, principal, subscription, channel, sequence).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("state: reset realtime durable cursor: %w", err)
	}
	return current, nil
}

func (s *PgStore) PruneExpiredManagedRealtimeDurableCursors(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeDurableCursorInvalid
	}
	tag, err := s.pool.Exec(ctx, `
		with expired as (
			select ctid from managed_realtime_durable_cursors
			where updated_at < clock_timestamp() - make_interval(secs => $2::double precision)
			order by updated_at
			limit $1
			for update skip locked
		)
		delete from managed_realtime_durable_cursors c
		where c.ctid in (select ctid from expired)
	`, batch, ManagedRealtimeDurableCursorRetention.Seconds())
	if err != nil {
		return 0, fmt.Errorf("state: prune expired realtime durable cursors: %w", err)
	}
	return tag.RowsAffected(), nil
}
