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
	var locked string
	err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, ep).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeReadProgress{}, ErrNotFound
	}
	if err != nil {
		return ManagedRealtimeReadProgress{}, err
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
	var exists bool
	var count int
	err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_read_progress where endpoint_id=$1 and principal=$2 and stream=$3 and inbox=$4),(select count(*) from managed_realtime_read_progress where endpoint_id=$1)`, ep, key.principal, key.stream, inbox).Scan(&exists, &count)
	if err != nil {
		return current, err
	}
	if !exists && count >= ManagedRealtimeMaxReadMarkers {
		return current, ErrManagedRealtimeDurableCursorLimit
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
