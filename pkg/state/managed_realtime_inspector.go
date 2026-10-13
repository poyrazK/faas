package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	inspectorsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeInspectorChannel struct {
	Channel                        string
	OldestSequence, LatestSequence int64
}
type RealtimeOutboxObservation struct {
	EventID, EndpointID, Channel, BlockedCode string
	Attempts                                  int
	CreatedAt, ObservedAt                     time.Time
}
type RealtimeInspectorSource struct {
	ID, Name, Status string
	CheckedAt        *time.Time
	Pending, Blocked *int64
}
type RealtimeInspectorStore interface {
	ListRealtimeInspectorChannels(context.Context, string) ([]RealtimeInspectorChannel, error)
}
type RealtimeInspectorOutboxStore interface {
	ListRealtimeInspectorSources(context.Context, string, string) ([]RealtimeInspectorSource, error)
	ReadRealtimeInspectorOutbox(context.Context, string, string) ([]RealtimeOutboxObservation, error)
	RecordRealtimeInspectorOutbox(context.Context, CommitRelaySource, []RealtimeOutboxObservation) error
}

func (s *PgStore) ListRealtimeInspectorChannels(ctx context.Context, ep string) ([]RealtimeInspectorChannel, error) {

	rows, err := inspectorsqlc.New().ListInspectorChannelsWithSchedules(ctx, s.pool, inspectorsqlc.ListInspectorChannelsWithSchedulesParams{EndpointID: ep, RowLimit: api.RealtimeInspectorSampleMax})
	if err != nil {
		return nil, err
	}
	out := make([]RealtimeInspectorChannel, 0, len(rows))
	for _, row := range rows {
		out = append(out, RealtimeInspectorChannel{Channel: row.Channel, OldestSequence: row.OldestSequence, LatestSequence: row.LatestSequence})
	}
	return out, nil
}
func (s *PgStore) ListRealtimeInspectorSources(ctx context.Context, account, app string) ([]RealtimeInspectorSource, error) {
	rows, err := s.pool.Query(ctx, `select id,name,relay_status,last_checked_at,pending_events,blocked_events from commit_sources where account_id=$1 and app_id=$2 order by name,id limit $3`, account, app, api.RealtimeInspectorSampleMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RealtimeInspectorSource{}
	for rows.Next() {
		var r RealtimeInspectorSource
		if err = rows.Scan(&r.ID, &r.Name, &r.Status, &r.CheckedAt, &r.Pending, &r.Blocked); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *PgStore) ReadRealtimeInspectorOutbox(ctx context.Context, account, source string) ([]RealtimeOutboxObservation, error) {
	rows, err := s.pool.Query(ctx, `select o.event_id::text,o.endpoint_id,o.channel,o.attempts,coalesce(o.blocked_code,''),o.created_at,o.observed_at from managed_realtime_outbox_observations o join commit_sources s on s.id=o.source_id where s.account_id=$1 and s.id=$2 order by o.created_at,o.event_id limit $3`, account, source, api.RealtimeInspectorSampleMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RealtimeOutboxObservation{}
	for rows.Next() {
		var o RealtimeOutboxObservation
		if err = rows.Scan(&o.EventID, &o.EndpointID, &o.Channel, &o.Attempts, &o.BlockedCode, &o.CreatedAt, &o.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *PgStore) RecordRealtimeInspectorOutbox(ctx context.Context, src CommitRelaySource, items []RealtimeOutboxObservation) error {
	if len(items) > api.RealtimeInspectorSampleMax {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current bool
	if err = tx.QueryRow(ctx, `select enabled and credential_revision=$3 from commit_sources where id=$1 and account_id=$2 for update`, src.ID, src.AccountID, src.CredentialRevision).Scan(&current); err != nil {
		return err
	}
	if !current {
		return nil
	}
	if _, err = tx.Exec(ctx, `delete from managed_realtime_outbox_observations where source_id=$1`, src.ID); err != nil {
		return err
	}
	for _, o := range items {
		if _, err = tx.Exec(ctx, `insert into managed_realtime_outbox_observations(source_id,event_id,endpoint_id,channel,attempts,blocked_code,created_at) values($1,$2,$3,$4,$5,nullif($6,''),$7)`, src.ID, o.EventID, o.EndpointID, o.Channel, o.Attempts, o.BlockedCode, o.CreatedAt); err != nil {
			return fmt.Errorf("state: save realtime observation: %w", err)
		}
	}
	return tx.Commit(ctx)
}
func (m *MemStore) ListRealtimeInspectorChannels(_ context.Context, ep string) ([]RealtimeInspectorChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	channels := map[string]RealtimeInspectorChannel{}
	for key, h := range m.managedRealtimeHistory {
		if key.endpointID == ep {
			channels[key.channel] = RealtimeInspectorChannel{key.channel, h.oldest, h.next - 1}
		}
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	for key, row := range m.managedRealtimeSchedules {
		if key.endpointID == ep && (row.Status == "pending" || row.Status == "paused" || !row.UpdatedAt.Before(cutoff)) {
			if _, ok := channels[key.channel]; !ok {
				channels[key.channel] = RealtimeInspectorChannel{Channel: key.channel, OldestSequence: 1}
			}
		}
	}
	out := make([]RealtimeInspectorChannel, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	if len(out) > api.RealtimeInspectorSampleMax {
		out = out[:api.RealtimeInspectorSampleMax]
	}
	return out, nil
}
