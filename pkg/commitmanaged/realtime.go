package commitmanaged

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state"
	ttlq "github.com/onebox-faas/faas/pkg/state/realtimettl/sqlc"
)

type realtimePublisher interface {
	PublishManagedRealtimeCommit(context.Context, state.ManagedRealtimeCommit) (state.ManagedRealtimeChannelMessage, error)
}

// tickRealtime shares the qualified source's credential and binding, but never
// converts a realtime publication into an application invocation.
func (m *Manager) tickRealtime(ctx context.Context, pool *pgxpool.Pool, src state.CommitRelaySource) (int, *commit.Backlog, error) {
	var installed bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.gregale_realtime_outbox') IS NOT NULL`).Scan(&installed); err != nil {
		return 0, nil, err
	}
	if !installed {
		return 0, nil, nil
	}
	if err := commit.QualifyRealtimeSchema(ctx, pool); err != nil {
		return 0, nil, err
	}
	publisher, ok := m.Store.(realtimePublisher)
	if !ok {
		return 0, nil, errors.New("realtime: publisher unavailable")
	}
	token := uuid.NewString()
	rows, err := new(ttlq.Queries).LeaseRealtimeCommits(ctx, pool, ttlq.LeaseRealtimeCommitsParams{LeaseToken: token, SourceID: src.ID})
	if err != nil {
		return 0, nil, err
	}
	type queuedEvent struct {
		event           state.ManagedRealtimeCommit
		invalidMetadata bool
	}
	var events []queuedEvent
	for _, row := range rows {
		e := state.ManagedRealtimeCommit{AccountID: src.AccountID, SourceID: src.ID, EventID: row.OEventID, EndpointID: row.EndpointID, Channel: row.Channel, Data: row.Data, TTLSeconds: int(row.TtlSeconds)}
		metadataErr := json.Unmarshal(row.Metadata, &e.Metadata)
		events = append(events, queuedEvent{event: e, invalidMetadata: metadataErr != nil})
	}
	count := 0
	var failures []error
	for _, queued := range events {
		e := queued.event
		var message state.ManagedRealtimeChannelMessage
		var publishErr error
		if queued.invalidMetadata {
			publishErr = state.ErrInvalidArgument
		} else {
			message, publishErr = publisher.PublishManagedRealtimeCommit(ctx, e)
		}
		if publishErr != nil {
			var code any
			var schemaError *state.ManagedRealtimeEventSchemaError
			var reducerError *state.ManagedRealtimeReducerError
			var versionError *state.ManagedRealtimeEntityVersionConflict
			switch {
			case errors.As(publishErr, &schemaError), errors.As(publishErr, &reducerError), errors.As(publishErr, &versionError):
				code = "invalid_event"
			case errors.Is(publishErr, state.ErrConflict):
				code = "identity_conflict"
			case errors.Is(publishErr, state.ErrNotFound):
				code = "destination_unqualified"
			case errors.Is(publishErr, state.ErrInvalidArgument), errors.Is(publishErr, state.ErrManagedRealtimeHistoryInvalid):
				code = "invalid_event"
			}
			_, err = pool.Exec(ctx, `UPDATE public.gregale_realtime_outbox SET lease_token=NULL,lease_until=NULL,blocked_code=$3,next_attempt_at=clock_timestamp()+interval '5 seconds' WHERE event_id=$1::uuid AND lease_token=$2::uuid AND published_at IS NULL`, e.EventID, token, code)
			failures = append(failures, publishErr, err)
			continue
		}
		tag, checkpointErr := pool.Exec(ctx, `UPDATE public.gregale_realtime_outbox SET published_at=$3,sequence=$4,lease_token=NULL,lease_until=NULL WHERE event_id=$1::uuid AND lease_token=$2::uuid AND published_at IS NULL`, e.EventID, token, message.CreatedAt, message.Sequence)
		if checkpointErr != nil {
			failures = append(failures, checkpointErr)
		} else {
			count += int(tag.RowsAffected())
		}
	}
	// Published rows remain inspectable for the same bounded retention as commit
	// events. Platform identity receipts survive customer-side cleanup.
	retention := m.AcceptedRetention
	if retention == 0 {
		retention = 7 * 24 * time.Hour
	}
	_, err = pool.Exec(ctx, `DELETE FROM public.gregale_realtime_outbox WHERE event_id IN (SELECT event_id FROM public.gregale_realtime_outbox WHERE published_at<$1 ORDER BY published_at,event_id LIMIT 32)`, time.Now().UTC().Add(-retention))
	failures = append(failures, err)
	backlog := &commit.Backlog{}
	err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE blocked_code IS NULL),count(*) FILTER (WHERE blocked_code IS NOT NULL),min(created_at) FROM public.gregale_realtime_outbox WHERE published_at IS NULL`).Scan(&backlog.Pending, &backlog.Blocked, &backlog.OldestPending)
	if err != nil {
		return count, nil, errors.Join(append(failures, err)...)
	}
	if observer, ok := m.Store.(interface {
		RecordRealtimeInspectorOutbox(context.Context, state.CommitRelaySource, []state.RealtimeOutboxObservation) error
	}); ok {
		observations, observeErr := observeRealtimeOutbox(ctx, pool)
		if observeErr == nil {
			observeErr = observer.RecordRealtimeInspectorOutbox(ctx, src, observations)
		}
		failures = append(failures, observeErr)
	}
	return count, backlog, errors.Join(failures...)
}

func observeRealtimeOutbox(ctx context.Context, pool *pgxpool.Pool) ([]state.RealtimeOutboxObservation, error) {
	rows, err := pool.Query(ctx, `select event_id::text,endpoint_id,channel,attempts,coalesce(blocked_code,''),created_at from public.gregale_realtime_outbox where published_at is null order by (blocked_code is null),created_at,event_id limit $1`, api.RealtimeInspectorSampleMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []state.RealtimeOutboxObservation{}
	for rows.Next() {
		var row state.RealtimeOutboxObservation
		if err = rows.Scan(&row.EventID, &row.EndpointID, &row.Channel, &row.Attempts, &row.BlockedCode, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
