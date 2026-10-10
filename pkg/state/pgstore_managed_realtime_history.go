package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ObserveManagedRealtimeHistoryStorage samples actual table, TOAST, and index
// allocation. It is constant-cost with respect to retained message count and
// deliberately has no customer identifiers for Prometheus label cardinality.
func (s *PgStore) ObserveManagedRealtimeHistoryStorage(ctx context.Context) (ManagedRealtimeHistoryStorageStats, error) {
	var stats ManagedRealtimeHistoryStorageStats
	err := s.pool.QueryRow(ctx, `
		select pg_total_relation_size('managed_realtime_channel_heads'::regclass),
		       pg_total_relation_size('managed_realtime_channel_messages'::regclass)
	`).Scan(&stats.HeadsRelationBytes, &stats.MessagesRelationBytes)
	if err != nil {
		return ManagedRealtimeHistoryStorageStats{}, fmt.Errorf("state: observe managed realtime history storage: %w", err)
	}
	return stats, nil
}

// ReadManagedRealtimeHistoryUsage aggregates only endpoints owned by accountID.
// A repeatable-read snapshot keeps counts coherent during append and prune.
func (s *PgStore) ReadManagedRealtimeHistoryUsage(ctx context.Context, accountID string) (ManagedRealtimeHistoryUsage, error) {
	if accountID == "" {
		return ManagedRealtimeHistoryUsage{}, ErrManagedRealtimeHistoryInvalid
	}
	usage := ManagedRealtimeHistoryUsage{ObservedAt: time.Now().UTC()}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ManagedRealtimeHistoryUsage{}, fmt.Errorf("state: begin realtime history usage read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `
		with scoped_heads as (
			select h.endpoint_id, h.channel, h.oldest_sequence
			from managed_realtime_channel_heads h
			join managed_realtime_endpoints e on e.id = h.endpoint_id
			where e.account_id = $1
		), channel_usage as (
			select h.endpoint_id, h.channel,
			       count(m.sequence) as stored_messages,
			       coalesce(sum(octet_length(m.data)), 0) as stored_bytes,
			       count(m.sequence) filter (where m.sequence >= expiry.visible_floor) as replayable_messages,
			       coalesce(sum(octet_length(m.data)) filter (where m.sequence >= expiry.visible_floor), 0) as replayable_bytes
			from scoped_heads h
			cross join lateral (
				select greatest(h.oldest_sequence, coalesce(max(sequence) + 1, h.oldest_sequence)) as visible_floor
				from managed_realtime_channel_messages
				where endpoint_id = h.endpoint_id and channel = h.channel and created_at < $2
			) expiry
			left join managed_realtime_channel_messages m
			  on m.endpoint_id = h.endpoint_id and m.channel = h.channel
			group by h.endpoint_id, h.channel, expiry.visible_floor
		)
		select count(distinct endpoint_id), count(*),
		       coalesce(sum(stored_messages), 0), coalesce(sum(stored_bytes), 0),
		       coalesce(sum(replayable_messages), 0), coalesce(sum(replayable_bytes), 0)
		from channel_usage
	`, accountID, usage.ObservedAt.Add(-ManagedRealtimeHistoryRetention)).Scan(
		&usage.EndpointCount, &usage.ChannelCount, &usage.StoredMessageCount,
		&usage.StoredPayloadBytes, &usage.ReplayableMessageCount, &usage.ReplayablePayloadBytes,
	)
	if err != nil {
		return ManagedRealtimeHistoryUsage{}, fmt.Errorf("state: read realtime history usage: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedRealtimeHistoryUsage{}, fmt.Errorf("state: commit realtime history usage read: %w", err)
	}
	return usage, nil
}

// AppendManagedRealtimeChannelMessage allocates the sequence and persists the
// message in one transaction. The channel head row is the cross-replica
// serialization point; rolled-back attempts cannot leave sequence holes.
func (s *PgStore) AppendManagedRealtimeChannelMessage(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string) (ManagedRealtimeChannelMessage, error) {
	return s.AppendManagedRealtimeChannelMetadata(ctx, endpointID, channel, data, binary, idempotencyKey, nil)
}
func (s *PgStore) AppendManagedRealtimeChannelMetadata(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, metadata map[string]string) (ManagedRealtimeChannelMessage, error) {
	return s.AppendManagedRealtimeChannelConditional(ctx, endpointID, channel, data, binary, idempotencyKey, metadata, nil)
}
func (s *PgStore) AppendManagedRealtimeChannelConditional(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, metadata map[string]string, expected *int64) (ManagedRealtimeChannelMessage, error) {
	return s.appendManagedRealtimeChannel(ctx, endpointID, channel, data, binary, idempotencyKey, metadata, expected, nil, nil)
}
func (s *PgStore) appendManagedRealtimeChannel(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, metadata map[string]string, expected *int64, expiration *ManagedRealtimeEntityExpiration, schedule *ManagedRealtimeSchedule) (ManagedRealtimeChannelMessage, error) {
	if expected != nil && *expected < 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if api.ValidateRealtimeMetadata(metadata) != nil {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := validateManagedRealtimeHistoryAppend(endpointID, channel, data, idempotencyKey); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: begin realtime history append: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from managed_realtime_channel_heads where endpoint_id = $1 and channel = $2)
	`, endpointID, channel).Scan(&exists); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: check realtime channel head: %w", err)
	}
	if !exists {
		var lockedID string
		if err := tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id = $1 for update`, endpointID).Scan(&lockedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ManagedRealtimeChannelMessage{}, ErrNotFound
			}
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: lock realtime endpoint: %w", err)
		}
		// A concurrent request may have created this same channel while we
		// waited for the endpoint lock. Count only if it is still new.
		if err := tx.QueryRow(ctx, `
			select exists(select 1 from managed_realtime_channel_heads where endpoint_id = $1 and channel = $2)
		`, endpointID, channel).Scan(&exists); err != nil {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: recheck realtime channel head: %w", err)
		}
		if !exists {
			var channels int
			if err := tx.QueryRow(ctx, `select count(*) from managed_realtime_channel_heads where endpoint_id = $1`, endpointID).Scan(&channels); err != nil {
				return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: count realtime channels: %w", err)
			}
			if channels >= ManagedRealtimeHistoryMaxChannels {
				return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryLimit
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		insert into managed_realtime_channel_heads (endpoint_id, channel)
		values ($1, $2) on conflict do nothing
	`, endpointID, channel); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
			return ManagedRealtimeChannelMessage{}, ErrNotFound
		}
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: create realtime channel head: %w", err)
	}
	var next, oldest int64
	if err := tx.QueryRow(ctx, `
		select next_sequence, oldest_sequence
		from managed_realtime_channel_heads
		where endpoint_id = $1 and channel = $2 for update
	`, endpointID, channel).Scan(&next, &oldest); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: lock realtime channel head: %w", err)
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
	// Expire a prefix, including any fresher low-sequence rows. Concurrent
	// transactions can commit in a different order from their timestamps;
	// retaining a hole would make cursor replay silently incomplete.
	var expiryFloor int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(sequence) + 1, $4::bigint)
		from managed_realtime_channel_messages
		where endpoint_id = $1 and channel = $2 and created_at < $3
	`, endpointID, channel, cutoff, oldest).Scan(&expiryFloor); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: find expired realtime sequence: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_channel_messages
		where endpoint_id = $1 and channel = $2 and sequence < $3
	`, endpointID, channel, expiryFloor); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: expire realtime channel messages: %w", err)
	}
	var retainedOldest int64
	if err := tx.QueryRow(ctx, `
		select coalesce(min(sequence), $3::bigint)
		from managed_realtime_channel_messages
		where endpoint_id = $1 and channel = $2
	`, endpointID, channel, next).Scan(&retainedOldest); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: find realtime retention floor: %w", err)
	}
	if retainedOldest > oldest {
		oldest = retainedOldest
		if _, err := tx.Exec(ctx, `
			update managed_realtime_channel_heads set oldest_sequence = $3
			where endpoint_id = $1 and channel = $2
		`, endpointID, channel, oldest); err != nil {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: advance realtime retention floor: %w", err)
		}
	}
	if idempotencyKey != "" {
		var existing ManagedRealtimeChannelMessage
		err := tx.QueryRow(ctx, `
			select sequence, data, is_binary, created_at, target_message_id, version, message_event, deleted,metadata
			from managed_realtime_channel_messages
			where endpoint_id = $1 and channel = $2 and target_message_id = $3 order by sequence desc limit 1
		`, endpointID, channel, idempotencyKey).Scan(&existing.Sequence, &existing.Data, &existing.Binary, &existing.CreatedAt, &existing.TargetMessageID, &existing.Version, &existing.MessageEvent, &existing.Deleted, &existing.Metadata)
		if err == nil {
			if !equalRealtimeMetadata(existing.Metadata, metadata) || existing.Version > 1 || existing.Deleted || existing.Binary != binary || !bytes.Equal(existing.Data, data) {
				return ManagedRealtimeChannelMessage{}, ErrConflict
			}
			existing.EndpointID, existing.Channel, existing.IdempotencyKey = endpointID, channel, idempotencyKey
			if err := tx.Commit(ctx); err != nil {
				return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: commit idempotent realtime history append: %w", err)
			}
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: read realtime publish key: %w", err)
		}
	}
	if err := checkExpectedRealtimeSequence(expected, next-1); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if expiration != nil {
		row, err := readReducerPG(ctx, tx, endpointID, channel)
		if err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if row == nil || !reducerExpirationMatches(*row, *expiration, time.Now().UTC()) {
			return ManagedRealtimeChannelMessage{}, ErrConflict
		}
	}
	if schedule != nil {
		row, err := readRealtimeSchedulePG(ctx, tx, schedule.EndpointID, schedule.Channel, schedule.ID, true)
		if err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if !scheduleMatches(row, *schedule, time.Now().UTC()) {
			return ManagedRealtimeChannelMessage{}, ErrConflict
		}
		schedule = &row
		reducer, err := readReducerPG(ctx, tx, endpointID, channel)
		if err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if err = checkScheduleConditions(row, reducer); err != nil {
			var failure *ManagedRealtimeScheduleConditionFailure
			if row.OnConditionFailure == "skip" && errors.As(err, &failure) {
				updated, eventRow := finishRealtimeScheduleOccurrence(row, 0, time.Now().UTC(), failure.Error())
				if err = saveSkippedRealtimeSchedulePG(ctx, tx, updated); err != nil {
					return ManagedRealtimeChannelMessage{}, err
				}
				if err = recordRealtimeScheduleHistoryPG(ctx, tx, eventRow, "skipped"); err != nil {
					return ManagedRealtimeChannelMessage{}, err
				}
				if !exists {
					if _, err = tx.Exec(ctx, `delete from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2 and next_sequence=1`, endpointID, channel); err != nil {
						return ManagedRealtimeChannelMessage{}, err
					}
				}
				if err = tx.Commit(ctx); err != nil {
					return ManagedRealtimeChannelMessage{}, err
				}
				return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeScheduleSkipped
			}
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	if schedule != nil {
		var maxBytes int64
		if err := tx.QueryRow(ctx, `select max_message_bytes from managed_realtime_endpoints where id=$1`, endpointID).Scan(&maxBytes); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
		}
	}
	if expiration == nil {
		if err := validateEventSchemaPG(ctx, tx, endpointID, channel, data, binary, metadata); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	message := ManagedRealtimeChannelMessage{
		Metadata: cloneRealtimeMetadata(metadata), TargetMessageID: idempotencyKey, Version: 1, MessageEvent: "created",
		EndpointID: endpointID, Channel: channel, Sequence: next,
		Data: append([]byte(nil), data...), Binary: binary, IdempotencyKey: idempotencyKey,
	}
	var key any
	if idempotencyKey != "" {
		key = idempotencyKey
	}
	if err := tx.QueryRow(ctx, `
		insert into managed_realtime_channel_messages
			(endpoint_id, channel, sequence, data, is_binary, idempotency_key, created_at,metadata)
		values ($1, $2, $3, $4, $5, $6, clock_timestamp(),$7) returning created_at
	`, endpointID, channel, next, data, binary, key, metadataJSON(metadata)).Scan(&message.CreatedAt); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: insert realtime channel message: %w", err)
	}
	if err := applyReducerPG(ctx, tx, endpointID, channel, []ManagedRealtimeChannelMessage{message}); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	newOldest := oldest
	if floor := next - ManagedRealtimeHistoryMaxMessages + 1; floor > newOldest {
		newOldest = floor
	}
	if _, err := tx.Exec(ctx, `
		update managed_realtime_channel_heads
		set next_sequence = $3, oldest_sequence = $4
		where endpoint_id = $1 and channel = $2
	`, endpointID, channel, next+1, newOldest); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: advance realtime channel head: %w", err)
	}
	if newOldest > oldest {
		if _, err := tx.Exec(ctx, `
			delete from managed_realtime_channel_messages
			where endpoint_id = $1 and channel = $2 and sequence < $3
		`, endpointID, channel, newOldest); err != nil {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: trim realtime channel history: %w", err)
		}
	}
	if schedule != nil {
		row, eventRow := completeRealtimeSchedule(*schedule, message.Sequence, message.CreatedAt)
		_, err := tx.Exec(ctx, `update managed_realtime_schedules set status=$4,sequence=$5,attempts=$6,cycle_attempts=$7,next_attempt_at=null,last_attempt_at=$8,version=$9,updated_at=$8,deliver_at=$10,occurrence=$11,completed_occurrences=$12 where endpoint_id=$1 and channel=$2 and schedule_id=$3`, row.EndpointID, row.Channel, row.ID, row.Status, row.Sequence, row.Attempts, row.CycleAttempts, row.LastAttemptAt, row.Version, row.DeliverAt, row.Occurrence, row.CompletedOccurrences)
		if err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if err = recordRealtimeScheduleHistoryPG(ctx, tx, eventRow, "published"); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: commit realtime history append: %w", err)
	}
	return message, nil
}

// ReadManagedRealtimeChannelHistory uses one repeatable-read snapshot for the
// retention floor and page, so a concurrent append/trim cannot hide a gap.
func (s *PgStore) ReadManagedRealtimeChannelHistory(ctx context.Context, endpointID, channel string, after int64, limit int) (ManagedRealtimeChannelHistory, error) {
	if err := validateManagedRealtimeHistoryRead(endpointID, channel, after, limit); err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: begin realtime history read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	history := ManagedRealtimeChannelHistory{OldestSequence: 1}
	var next int64
	err = tx.QueryRow(ctx, `
		select next_sequence, oldest_sequence
		from managed_realtime_channel_heads
		where endpoint_id = $1 and channel = $2
	`, endpointID, channel).Scan(&next, &history.OldestSequence)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_endpoints where id = $1)`, endpointID).Scan(&exists); err != nil {
			return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: check realtime endpoint: %w", err)
		}
		if !exists {
			return ManagedRealtimeChannelHistory{}, ErrNotFound
		}
		if after > 0 {
			return ManagedRealtimeChannelHistory{}, ErrManagedRealtimeHistoryInvalid
		}
		return history, nil
	}
	if err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: read realtime channel head: %w", err)
	}
	history.LatestSequence = next - 1
	if after > history.LatestSequence {
		return ManagedRealtimeChannelHistory{}, ErrManagedRealtimeHistoryInvalid
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
	// Use the highest expired sequence as the visible floor so pages remain
	// contiguous even before the cleanup pass physically removes old rows.
	var visibleOldest int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(sequence) + 1, $4::bigint)
		from managed_realtime_channel_messages
		where endpoint_id = $1 and channel = $2 and created_at < $3
	`, endpointID, channel, cutoff, history.OldestSequence).Scan(&visibleOldest); err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: read realtime retention floor: %w", err)
	}
	if visibleOldest > history.OldestSequence {
		history.OldestSequence = visibleOldest
	}
	if after < history.OldestSequence-1 {
		history.HistoryUnavailable = true
		return history, nil
	}
	rows, err := tx.Query(ctx, `
		select sequence, data, is_binary, idempotency_key, created_at, target_message_id, version, message_event, deleted,metadata
		from managed_realtime_channel_messages
		where endpoint_id = $1 and channel = $2 and sequence > $3 and sequence >= $4
		order by sequence asc limit $5
	`, endpointID, channel, after, history.OldestSequence, limit)
	if err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: list realtime channel history: %w", err)
	}
	for rows.Next() {
		message := ManagedRealtimeChannelMessage{EndpointID: endpointID, Channel: channel}
		var key *string
		if err := rows.Scan(&message.Sequence, &message.Data, &message.Binary, &key, &message.CreatedAt, &message.TargetMessageID, &message.Version, &message.MessageEvent, &message.Deleted, &message.Metadata); err != nil {
			rows.Close()
			return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: scan realtime channel message: %w", err)
		}
		if key != nil {
			message.IdempotencyKey = *key
		}
		history.Messages = append(history.Messages, message)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: iterate realtime channel history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: commit realtime history read: %w", err)
	}
	return history, nil
}

// PruneExpiredManagedRealtimeChannelMessages physically removes old rows.
// Channel-head locks serialize cleanup with append and keep the floor exact.
func (s *PgStore) PruneExpiredManagedRealtimeChannelMessages(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin realtime history prune: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select h.endpoint_id, h.channel
		from managed_realtime_channel_heads h
		where exists (
			select 1 from managed_realtime_channel_messages m
			where m.endpoint_id = h.endpoint_id and m.channel = h.channel and m.created_at < $1
		)
		order by h.endpoint_id, h.channel
		limit $2 for update of h skip locked
	`, cutoff, batch)
	if err != nil {
		return 0, fmt.Errorf("state: select expired realtime channels: %w", err)
	}
	type key struct{ endpointID, channel string }
	var keys []key
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.endpointID, &item.channel); err != nil {
			rows.Close()
			return 0, fmt.Errorf("state: scan expired realtime channel: %w", err)
		}
		keys = append(keys, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("state: iterate expired realtime channels: %w", err)
	}
	var removed int64
	for _, item := range keys {
		var expiryFloor int64
		if err := tx.QueryRow(ctx, `
			select coalesce(max(sequence) + 1, 1)
			from managed_realtime_channel_messages
			where endpoint_id = $1 and channel = $2 and created_at < $3
		`, item.endpointID, item.channel, cutoff).Scan(&expiryFloor); err != nil {
			return 0, fmt.Errorf("state: find expired realtime sequence: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			delete from managed_realtime_channel_messages
			where endpoint_id = $1 and channel = $2 and sequence < $3
		`, item.endpointID, item.channel, expiryFloor)
		if err != nil {
			return 0, fmt.Errorf("state: prune expired realtime messages: %w", err)
		}
		removed += tag.RowsAffected()
		if _, err := tx.Exec(ctx, `
			update managed_realtime_channel_heads h
			set oldest_sequence = greatest(h.oldest_sequence, coalesce((
				select min(m.sequence) from managed_realtime_channel_messages m
				where m.endpoint_id = h.endpoint_id and m.channel = h.channel
			), h.next_sequence))
			where h.endpoint_id = $1 and h.channel = $2
		`, item.endpointID, item.channel); err != nil {
			return 0, fmt.Errorf("state: advance pruned realtime floor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit realtime history prune: %w", err)
	}
	return removed, nil
}
