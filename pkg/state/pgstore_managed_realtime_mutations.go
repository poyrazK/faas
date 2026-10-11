package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *PgStore) MutateManagedRealtimeMessage(ctx context.Context, ep, stream string, inbox bool, id string, version int64, data []byte, binary, remove bool) (ManagedRealtimeChannelMessage, error) {
	channel, err := mutationStream(stream, inbox, id, version, data, remove)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	table, head, maxRows := "managed_realtime_channel_messages", "managed_realtime_channel_heads", ManagedRealtimeHistoryMaxMessages
	if inbox {
		table, head, maxRows = "managed_realtime_inbox_messages", "managed_realtime_inbox_heads", ManagedRealtimeInboxMaxMessages
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var next, oldest int64
	err = tx.QueryRow(ctx, fmt.Sprintf(`select next_sequence,oldest_sequence from %s where endpoint_id=$1 and channel=$2 for update`, head), ep, channel).Scan(&next, &oldest)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeChannelMessage{}, ErrNotFound
	}
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	now := time.Now().UTC()
	var floor int64
	err = tx.QueryRow(ctx, fmt.Sprintf(`select greatest($3::bigint,coalesce(max(sequence)+1,$3::bigint)) from %s where endpoint_id=$1 and channel=$2 and created_at<$4`, table), ep, channel, oldest, now.Add(-ManagedRealtimeHistoryRetention)).Scan(&floor)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	var current ManagedRealtimeChannelMessage
	err = tx.QueryRow(ctx, fmt.Sprintf(`select sequence,version,deleted,data,is_binary,created_at,message_event,metadata from %s where endpoint_id=$1 and channel=$2 and target_message_id=$3 and sequence>=$4 order by sequence desc limit 1`, table), ep, channel, id, floor).Scan(&current.Sequence, &current.Version, &current.Deleted, &current.Data, &current.Binary, &current.CreatedAt, &current.MessageEvent, &current.Metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeChannelMessage{}, ErrNotFound
	}
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if current.Version != version {
		return ManagedRealtimeChannelMessage{}, ErrConflict
	}
	if current.Deleted {
		if !remove {
			return ManagedRealtimeChannelMessage{}, ErrConflict
		}
		current.EndpointID, current.Channel, current.TargetMessageID = ep, channel, id
		return current, nil
	}
	if !remove && !inbox {
		if err := validateEventSchemaPG(ctx, tx, ep, channel, data, binary, current.Metadata); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	if !inbox {
		row, e := readReducerPG(ctx, tx, ep, channel)
		if e != nil {
			return ManagedRealtimeChannelMessage{}, e
		}
		if row != nil {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeReducerActive
		}
	}
	event := "updated"
	if remove {
		event = "deleted"
		data = nil
		binary = false
	}
	if data == nil {
		data = []byte{}
	}
	updated := ManagedRealtimeChannelMessage{Metadata: current.Metadata, EndpointID: ep, Channel: channel, TargetMessageID: id, Version: version + 1, MessageEvent: event, Deleted: remove, Data: data, Binary: binary, Sequence: next, CreatedAt: now}
	_, err = tx.Exec(ctx, fmt.Sprintf(`update %s set data=$4,is_binary=$5,version=$6,deleted=$7,message_event=$8 where endpoint_id=$1 and channel=$2 and target_message_id=$3`, table), ep, channel, id, data, binary, updated.Version, remove, event)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`insert into %s(endpoint_id,channel,sequence,data,is_binary,created_at,target_message_id,version,message_event,deleted,metadata) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, table), ep, channel, next, data, binary, now, id, updated.Version, event, remove, metadataJSON(current.Metadata))
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if remove && inbox {

		if _, err = tx.Exec(ctx, `delete from managed_realtime_inbox_fallbacks where endpoint_id=$1 and principal=$2 and message_id=$3`, ep, channel, id); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
		if _, err = tx.Exec(ctx, `update managed_realtime_push_deliveries set status='cancelled',code='message_deleted',lease=null,lease_until=null,updated_at=clock_timestamp() where endpoint_id=$1 and principal=$2 and message_id=$3 and status in ('pending','sending')`, ep, channel, id); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	if capFloor := next - int64(maxRows) + 1; capFloor > floor {
		floor = capFloor
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`update %s set next_sequence=$3,oldest_sequence=$4 where endpoint_id=$1 and channel=$2`, head), ep, channel, next+1, floor)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`delete from %s where endpoint_id=$1 and channel=$2 and sequence<$3`, table), ep, channel, floor)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return updated, nil
}
