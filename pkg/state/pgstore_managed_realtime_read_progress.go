package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"time"
)

type realtimeReadQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readProgressPG(ctx context.Context, q realtimeReadQuery, key managedRealtimeReadKey) (ManagedRealtimeReadProgress, error) {
	result := ManagedRealtimeReadProgress{OldestSequence: 1}
	err := q.QueryRow(ctx, `select sequence,updated_at from managed_realtime_read_progress where endpoint_id=$1 and principal=$2 and stream=$3 and inbox=$4`, key.endpointID, key.principal, key.stream, key.inbox).Scan(&result.Sequence, &result.UpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	head, table := "managed_realtime_channel_heads", "managed_realtime_channel_messages"
	if key.inbox {
		head, table = "managed_realtime_inbox_heads", "managed_realtime_inbox_messages"
	}
	err = q.QueryRow(ctx, fmt.Sprintf(`with bounds as (
 select next_sequence-1 as latest,greatest(oldest_sequence,coalesce((select max(sequence)+1 from %s where endpoint_id=$1 and channel=$2 and created_at<$3),1)) as oldest from %s where endpoint_id=$1 and channel=$2
 ) select b.latest,b.oldest,(select count(distinct case when target_message_id<>'' then 'id:'||target_message_id else 'seq:'||sequence::text end) from %s where endpoint_id=$1 and channel=$2 and sequence>=b.oldest and sequence>$4 and not deleted) from bounds b`, table, head, table), key.endpointID, key.stream, time.Now().UTC().Add(-ManagedRealtimeHistoryRetention), result.Sequence).Scan(&result.LatestSequence, &result.OldestSequence, &result.Unread)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	result.HistoryUnavailable = result.Sequence < result.OldestSequence-1
	return result, nil
}
func (s *PgStore) GetManagedRealtimeReadProgress(ctx context.Context, ep, principal, channel string, inbox bool) (ManagedRealtimeReadProgress, error) {
	key, err := readProgressKey(ep, principal, channel, inbox)
	if err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_endpoints where id=$1)`, ep).Scan(&exists)
	if err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	if !exists {
		return ManagedRealtimeReadProgress{}, ErrNotFound
	}
	return readProgressPG(ctx, tx, key)
}
func (s *PgStore) AdvanceManagedRealtimeReadProgress(ctx context.Context, ep, principal, channel string, inbox bool, sequence int64) (ManagedRealtimeReadProgress, error) {
	key, err := readProgressKey(ep, principal, channel, inbox)
	if err != nil || sequence < 0 {
		return ManagedRealtimeReadProgress{}, ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Readers advance markers many times a second (ADR-1323, amendment of
	// 2026-11-03). A repeated or older sequence answers without a lock or a
	// write, and a marker that already counts toward the endpoint's cap is
	// advanced under its own row lock. Only a new marker takes the endpoint
	// lock that keeps the cap exact.
	var endpointExists bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_endpoints where id=$1)`, ep).Scan(&endpointExists); err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	if !endpointExists {
		return ManagedRealtimeReadProgress{}, ErrNotFound
	}
	current, err := readProgressPG(ctx, tx, key)
	if err != nil {
		return current, err
	}
	if sequence > current.LatestSequence {
		return current, ErrManagedRealtimeHistoryInvalid
	}
	if sequence <= current.Sequence {
		return current, nil
	}
	var live bool
	err = tx.QueryRow(ctx, `select true from managed_realtime_read_progress where endpoint_id=$1 and principal=$2 and stream=$3 and inbox=$4 and updated_at >= clock_timestamp() - make_interval(secs => $5::double precision) for update`, ep, key.principal, key.stream, inbox, ManagedRealtimeReadMarkerRetention.Seconds()).Scan(&live)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return current, err
	}
	if !live {
		var locked string
		err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for no key update`, ep).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedRealtimeReadProgress{}, ErrNotFound
		}
		if err != nil {
			return ManagedRealtimeReadProgress{}, err
		}
		var exists bool
		var count int
		// Only markers updated within the retention count; the reaper deletes the
		// rest (ADR-1323, amendment of 2026-10-31).
		err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_read_progress where endpoint_id=$1 and principal=$2 and stream=$3 and inbox=$4 and updated_at >= clock_timestamp() - make_interval(secs => $5::double precision)),(select count(*) from managed_realtime_read_progress where endpoint_id=$1 and updated_at >= clock_timestamp() - make_interval(secs => $5::double precision))`, ep, key.principal, key.stream, inbox, ManagedRealtimeReadMarkerRetention.Seconds()).Scan(&exists, &count)
		if err != nil {
			return current, err
		}
		if !exists && count >= ManagedRealtimeMaxReadMarkers {
			return current, ErrManagedRealtimeDurableCursorLimit
		}
	}
	// The row lock (or the endpoint lock) orders concurrent advances, so
	// the webhook's previous sequence is the one this advance replaced.
	var stored int64
	err = tx.QueryRow(ctx, `select sequence from managed_realtime_read_progress where endpoint_id=$1 and principal=$2 and stream=$3 and inbox=$4`, ep, key.principal, key.stream, inbox).Scan(&stored)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return current, err
	}
	if err == nil && stored > current.Sequence {
		if sequence <= stored {
			return readProgressPG(ctx, tx, key)
		}
		current.Sequence = stored
	}
	_, err = tx.Exec(ctx, `insert into managed_realtime_read_progress(endpoint_id,principal,stream,inbox,sequence) values($1,$2,$3,$4,$5) on conflict(endpoint_id,principal,stream,inbox) do update set sequence=greatest(managed_realtime_read_progress.sequence,excluded.sequence),updated_at=clock_timestamp()`, ep, key.principal, key.stream, inbox, sequence)
	if err != nil {
		return current, err
	}
	result, err := readProgressPG(ctx, tx, key)
	if err != nil {
		return result, err
	}
	channelName := channel
	if inbox {
		channelName = ""
	}
	_, err = tx.Exec(ctx, `select faas_capture_realtime_inbox_webhook($1::uuid,$2::text,'','realtime.message.read',jsonb_build_object('sequence',$3::bigint,'previous_sequence',$4::bigint,'unread',$5::bigint,'inbox',$6::boolean,'channel',$7::text))`, ep, key.principal, result.Sequence, current.Sequence, result.Unread, inbox, channelName)
	if err != nil {
		return result, err
	}
	err = tx.Commit(ctx)
	return result, err
}

func (s *PgStore) PruneExpiredManagedRealtimeReadMarkers(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeDurableCursorInvalid
	}
	tag, err := s.pool.Exec(ctx, `
		with expired as (
			select ctid from managed_realtime_read_progress
			where updated_at < clock_timestamp() - make_interval(secs => $2::double precision)
			order by updated_at
			limit $1
			for update skip locked
		)
		delete from managed_realtime_read_progress p
		where p.ctid in (select ctid from expired)
	`, batch, ManagedRealtimeReadMarkerRetention.Seconds())
	if err != nil {
		return 0, fmt.Errorf("state: prune expired realtime read markers: %w", err)
	}
	return tag.RowsAffected(), nil
}
