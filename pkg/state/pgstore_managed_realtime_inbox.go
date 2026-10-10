package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

// appendManagedRealtimeInboxMessage allocates the sequence and persists the
// message in one transaction. The channel head row is the cross-replica
// serialization point; rolled-back attempts cannot leave sequence holes.
func (s *PgStore) appendManagedRealtimeInboxMessage(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, fallbackAfter int, categories ...string) (ManagedRealtimeChannelMessage, error) {
	category := "notifications"
	if len(categories) > 0 {
		category = categories[0]
	}
	groupKey, groupLabel := "", ""
	if len(categories) > 1 {
		groupKey = categories[1]
	}
	if len(categories) > 2 {
		groupLabel = categories[2]
	}
	notBeforeRaw := ""
	if len(categories) > 6 {
		notBeforeRaw = categories[6]
	}
	notBefore, scheduleErr := api.ParseRealtimeNotificationNotBefore(notBeforeRaw, time.Now().UTC())
	if scheduleErr != nil || notBeforeRaw != "" && fallbackAfter == 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if !notBefore.IsZero() {
		notBeforeRaw = notBefore.Format(time.RFC3339Nano)
	}
	collapseKey := ""
	if len(categories) > 5 {
		collapseKey = categories[5]
	}
	if api.ValidateRealtimeNotificationGroup(collapseKey, "") != nil || collapseKey != "" && fallbackAfter == 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	ttl := 0
	if len(categories) > 4 {
		var err error
		ttl, err = strconv.Atoi(categories[4])
		if err != nil || ttl < 0 || ttl > 259200 || ttl > 0 && (fallbackAfter == 0 || ttl <= fallbackAfter) {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
		}
	}
	priority := "normal"
	if len(categories) > 3 && categories[3] != "" {
		priority = categories[3]
	}
	if api.ValidateRealtimeNotificationPriority(priority) != nil {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if api.ValidateRealtimeNotificationGroup(groupKey, groupLabel) != nil {
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
		select exists(select 1 from managed_realtime_inbox_heads where endpoint_id = $1 and channel = $2)
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
			select exists(select 1 from managed_realtime_inbox_heads where endpoint_id = $1 and channel = $2)
		`, endpointID, channel).Scan(&exists); err != nil {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: recheck realtime channel head: %w", err)
		}
		if !exists {
			var channels int
			if err := tx.QueryRow(ctx, `select count(*) from managed_realtime_inbox_heads where endpoint_id = $1`, endpointID).Scan(&channels); err != nil {
				return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: count realtime channels: %w", err)
			}
			if channels >= ManagedRealtimeInboxMaxPrincipals {
				return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryLimit
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		insert into managed_realtime_inbox_heads (endpoint_id, channel)
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
		from managed_realtime_inbox_heads
		where endpoint_id = $1 and channel = $2 for update
	`, endpointID, channel).Scan(&next, &oldest); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: lock realtime channel head: %w", err)
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxRetention)
	// Expire a prefix, including any fresher low-sequence rows. Concurrent
	// transactions can commit in a different order from their timestamps;
	// retaining a hole would make cursor replay silently incomplete.
	var expiryFloor int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(sequence) + 1, $4::bigint)
		from managed_realtime_inbox_messages
		where endpoint_id = $1 and channel = $2 and created_at < $3
	`, endpointID, channel, cutoff, oldest).Scan(&expiryFloor); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: find expired realtime sequence: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		delete from managed_realtime_inbox_messages
		where endpoint_id = $1 and channel = $2 and sequence < $3
	`, endpointID, channel, expiryFloor); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: expire realtime channel messages: %w", err)
	}
	var retainedOldest int64
	if err := tx.QueryRow(ctx, `
		select coalesce(min(sequence), $3::bigint)
		from managed_realtime_inbox_messages
		where endpoint_id = $1 and channel = $2
	`, endpointID, channel, next).Scan(&retainedOldest); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: find realtime retention floor: %w", err)
	}
	if retainedOldest > oldest {
		oldest = retainedOldest
		if _, err := tx.Exec(ctx, `
			update managed_realtime_inbox_heads set oldest_sequence = $3
			where endpoint_id = $1 and channel = $2
		`, endpointID, channel, oldest); err != nil {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: advance realtime retention floor: %w", err)
		}
	}
	if idempotencyKey != "" {
		var existing ManagedRealtimeChannelMessage
		err := tx.QueryRow(ctx, `
			select sequence, data, is_binary, created_at, fallback_after_seconds, notification_category,notification_group_key,notification_group_label,notification_priority,notification_ttl_seconds,notification_collapse_key,notification_not_before, target_message_id, version, message_event, deleted
			from managed_realtime_inbox_messages
			where endpoint_id = $1 and channel = $2 and target_message_id = $3 order by sequence desc limit 1
		`, endpointID, channel, idempotencyKey).Scan(&existing.Sequence, &existing.Data, &existing.Binary, &existing.CreatedAt, &existing.FallbackAfterSeconds, &existing.NotificationCategory, &existing.NotificationGroupKey, &existing.NotificationGroupLabel, &existing.NotificationPriority, &existing.NotificationTTLSeconds, &existing.NotificationCollapseKey, &existing.NotificationNotBefore, &existing.TargetMessageID, &existing.Version, &existing.MessageEvent, &existing.Deleted)
		if err == nil {
			if existing.Version > 1 || existing.Deleted || existing.Binary != binary || !bytes.Equal(existing.Data, data) || existing.FallbackAfterSeconds != fallbackAfter || existing.NotificationCategory != category || existing.NotificationGroupKey != groupKey || existing.NotificationGroupLabel != groupLabel || existing.NotificationPriority != priority || existing.NotificationTTLSeconds != ttl || existing.NotificationCollapseKey != collapseKey || existing.NotificationNotBefore != notBeforeRaw {
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
	if fallbackAfter > 0 {
		var available bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from app_webhooks h join managed_realtime_endpoints e on e.app_id = h.app_id and e.account_id = h.account_id where e.id = $1 and h.scope = 'app' and h.enabled and (cardinality(h.event_filter) = 0 or 'realtime.inbox.fallback_required' = any(h.event_filter))) or exists(select 1 from managed_realtime_push_providers where endpoint_id=$1 and enabled)`, endpointID).Scan(&available); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if !available {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeFallbackSubscription
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from managed_realtime_inbox_fallbacks where endpoint_id = $1 and principal = $2`, endpointID, channel).Scan(&count); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if count >= ManagedRealtimeInboxMaxMessages {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryLimit
		}
	}
	message := ManagedRealtimeChannelMessage{
		TargetMessageID: idempotencyKey, Version: 1, MessageEvent: "created",
		EndpointID: endpointID, Channel: channel, Sequence: next,
		Data: append([]byte(nil), data...), Binary: binary, IdempotencyKey: idempotencyKey, FallbackAfterSeconds: fallbackAfter, NotificationCategory: category, NotificationGroupKey: groupKey, NotificationGroupLabel: groupLabel, NotificationPriority: priority, NotificationTTLSeconds: ttl, NotificationCollapseKey: collapseKey, NotificationNotBefore: notBeforeRaw,
	}
	if ttl > 0 && !notBefore.Before(time.Now().UTC().Add(time.Duration(ttl)*time.Second)) {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	var key any
	if idempotencyKey != "" {
		key = idempotencyKey
	}
	if err := tx.QueryRow(ctx, `
		insert into managed_realtime_inbox_messages
			(endpoint_id, channel, sequence, data, is_binary, idempotency_key, created_at, fallback_after_seconds,notification_category,notification_group_key,notification_group_label,notification_priority,notification_ttl_seconds,notification_collapse_key,notification_not_before)
		values ($1, $2, $3, $4, $5, $6, clock_timestamp(), $7,$8,$9,$10,$11,$12,$13,$14) returning created_at
	`, endpointID, channel, next, data, binary, key, fallbackAfter, category, groupKey, groupLabel, priority, ttl, collapseKey, notBeforeRaw).Scan(&message.CreatedAt); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: insert realtime channel message: %w", err)
	}
	if fallbackAfter > 0 {
		if _, err := tx.Exec(ctx, `insert into managed_realtime_inbox_fallbacks(endpoint_id, principal, sequence, message_id, deadline,category,group_key,group_label,priority,expires_at,collapse_key,not_before) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, endpointID, channel, next, idempotencyKey, message.CreatedAt.Add(time.Duration(fallbackAfter)*time.Second), category, groupKey, groupLabel, priority, notificationDeadline(message.CreatedAt, ttl), collapseKey, notBefore); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	newOldest := oldest
	if floor := next - ManagedRealtimeInboxMaxMessages + 1; floor > newOldest {
		newOldest = floor
	}
	if _, err := tx.Exec(ctx, `
		update managed_realtime_inbox_heads
		set next_sequence = $3, oldest_sequence = $4
		where endpoint_id = $1 and channel = $2
	`, endpointID, channel, next+1, newOldest); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: advance realtime channel head: %w", err)
	}
	if newOldest > oldest {
		if _, err := tx.Exec(ctx, `
			delete from managed_realtime_inbox_messages
			where endpoint_id = $1 and channel = $2 and sequence < $3
		`, endpointID, channel, newOldest); err != nil {
			return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: trim realtime channel history: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedRealtimeChannelMessage{}, fmt.Errorf("state: commit realtime history append: %w", err)
	}
	return message, nil
}

// readManagedRealtimeInbox uses one repeatable-read snapshot for the
// retention floor and page, so a concurrent append/trim cannot hide a gap.
func (s *PgStore) readManagedRealtimeInbox(ctx context.Context, endpointID, channel string, after int64, limit int) (ManagedRealtimeChannelHistory, error) {
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
		from managed_realtime_inbox_heads
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
	cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxRetention)
	// Use the highest expired sequence as the visible floor so pages remain
	// contiguous even before the cleanup pass physically removes old rows.
	var visibleOldest int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(sequence) + 1, $4::bigint)
		from managed_realtime_inbox_messages
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
		select sequence, data, is_binary, idempotency_key, created_at, target_message_id, version, message_event, deleted
		from managed_realtime_inbox_messages
		where endpoint_id = $1 and channel = $2 and sequence > $3 and sequence >= $4
		order by sequence asc limit $5
	`, endpointID, channel, after, history.OldestSequence, limit)
	if err != nil {
		return ManagedRealtimeChannelHistory{}, fmt.Errorf("state: list realtime channel history: %w", err)
	}
	for rows.Next() {
		message := ManagedRealtimeChannelMessage{EndpointID: endpointID, Channel: channel}
		var key *string
		if err := rows.Scan(&message.Sequence, &message.Data, &message.Binary, &key, &message.CreatedAt, &message.TargetMessageID, &message.Version, &message.MessageEvent, &message.Deleted); err != nil {
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

// PruneExpiredManagedRealtimeInboxMessages physically removes old rows.
// Channel-head locks serialize cleanup with append and keep the floor exact.
func (s *PgStore) PruneExpiredManagedRealtimeInboxMessages(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxRetention)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin realtime history prune: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select h.endpoint_id, h.channel
		from managed_realtime_inbox_heads h
		where exists (
			select 1 from managed_realtime_inbox_messages m
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
			from managed_realtime_inbox_messages
			where endpoint_id = $1 and channel = $2 and created_at < $3
		`, item.endpointID, item.channel, cutoff).Scan(&expiryFloor); err != nil {
			return 0, fmt.Errorf("state: find expired realtime sequence: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			delete from managed_realtime_inbox_messages
			where endpoint_id = $1 and channel = $2 and sequence < $3
		`, item.endpointID, item.channel, expiryFloor)
		if err != nil {
			return 0, fmt.Errorf("state: prune expired realtime messages: %w", err)
		}
		removed += tag.RowsAffected()
		if _, err := tx.Exec(ctx, `
			update managed_realtime_inbox_heads h
			set oldest_sequence = greatest(h.oldest_sequence, coalesce((
				select min(m.sequence) from managed_realtime_inbox_messages m
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
