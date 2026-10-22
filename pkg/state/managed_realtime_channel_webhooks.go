package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type RealtimeChannelWebhookFilter struct {
	EndpointID string            `json:"endpoint_id"`
	Channel    string            `json:"channel"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
type RealtimeChannelWebhookStore interface {
	ReadRealtimeChannelWebhookFilter(context.Context, string) (RealtimeChannelWebhookFilter, error)
	SetRealtimeChannelWebhookFilter(context.Context, string, RealtimeChannelWebhookFilter) error
	DeleteRealtimeChannelWebhookFilter(context.Context, string) error
}

func validateChannelWebhookFilter(f RealtimeChannelWebhookFilter) error {
	if validateManagedRealtimeHistoryRequest(f.EndpointID, f.Channel) != nil || api.ValidateRealtimeMetadata(f.Metadata) != nil {
		return ErrInvalidArgument
	}
	return nil
}
func (s *PgStore) ReadRealtimeChannelWebhookFilter(ctx context.Context, id string) (RealtimeChannelWebhookFilter, error) {
	var f RealtimeChannelWebhookFilter
	err := s.pool.QueryRow(ctx, `select endpoint_id,channel,metadata from managed_realtime_channel_webhook_filters where webhook_id=$1`, id).Scan(&f.EndpointID, &f.Channel, &f.Metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return f, err
}
func (s *PgStore) SetRealtimeChannelWebhookFilter(ctx context.Context, id string, f RealtimeChannelWebhookFilter) error {
	if err := validateChannelWebhookFilter(f); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `insert into managed_realtime_channel_webhook_filters(webhook_id,endpoint_id,channel,metadata)
 select h.id,e.id,$3,$4::jsonb from app_webhooks h join managed_realtime_endpoints e on e.app_id=h.app_id and e.account_id=h.account_id
 where h.id=$1 and e.id=$2 and h.scope='app' and 'realtime.channel.published'=any(h.event_filter)
 on conflict(webhook_id) do update set endpoint_id=excluded.endpoint_id,channel=excluded.channel,metadata=excluded.metadata`, id, f.EndpointID, f.Channel, metadataJSON(f.Metadata))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (s *PgStore) DeleteRealtimeChannelWebhookFilter(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `delete from managed_realtime_channel_webhook_filters where webhook_id=$1`, id)
	return err
}
func (m *MemStore) ReadRealtimeChannelWebhookFilter(_ context.Context, id string) (RealtimeChannelWebhookFilter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.realtimeChannelWebhookFilters[id]
	if !ok {
		return f, ErrNotFound
	}
	f.Metadata = cloneRealtimeMetadata(f.Metadata)
	return f, nil
}
func (m *MemStore) SetRealtimeChannelWebhookFilter(_ context.Context, id string, f RealtimeChannelWebhookFilter) error {
	if err := validateChannelWebhookFilter(f); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.appWebhooks[id]
	e, exists := m.managedRealtimeEndpoints[f.EndpointID]
	explicit := false
	for _, event := range h.EventFilter {
		if event == string(AppWebhookEventRealtimeChannelPublished) {
			explicit = true
		}
	}
	if !ok || !exists || h.Scope != AppWebhookScopeApp || h.AppID != e.AppID || h.AccountID != e.AccountID || !explicit {
		return ErrNotFound
	}
	if m.realtimeChannelWebhookFilters == nil {
		m.realtimeChannelWebhookFilters = map[string]RealtimeChannelWebhookFilter{}
	}
	f.Metadata = cloneRealtimeMetadata(f.Metadata)
	m.realtimeChannelWebhookFilters[id] = f
	return nil
}
func (m *MemStore) DeleteRealtimeChannelWebhookFilter(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.realtimeChannelWebhookFilters, id)
	return nil
}
func (m *MemStore) enqueueRealtimeChannelWebhookLocked(msg ManagedRealtimeChannelMessage) {
	ep, ok := m.managedRealtimeEndpoints[msg.EndpointID]
	if !ok {
		return
	}
	recipients := []string{}
	for id, f := range m.realtimeChannelWebhookFilters {
		h, exists := m.appWebhooks[id]
		if !exists || !h.Enabled || h.Scope != AppWebhookScopeApp || h.AppID != ep.AppID || h.AccountID != ep.AccountID || f.EndpointID != msg.EndpointID || f.Channel != msg.Channel {
			continue
		}
		explicit := false
		for _, event := range h.EventFilter {
			if event == string(AppWebhookEventRealtimeChannelPublished) {
				explicit = true
			}
		}
		if !explicit {
			continue
		}
		matches := true
		for k, v := range f.Metadata {
			actual, exists := msg.Metadata[k]
			if !exists || actual != v {
				matches = false
				break
			}
		}
		if matches {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	id := newID()
	payload, _ := json.Marshal(map[string]any{"event_id": id, "app_id": ep.AppID, "endpoint_id": msg.EndpointID, "channel": msg.Channel, "sequence": strconv.FormatInt(msg.Sequence, 10), "data_base64": base64.StdEncoding.EncodeToString(msg.Data), "binary": msg.Binary, "metadata": msg.Metadata, "target_message_id": msg.TargetMessageID, "version": msg.Version, "message_event": msg.MessageEvent, "deleted": msg.Deleted, "occurred_at": msg.CreatedAt})
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: ep.AccountID, AppID: ep.AppID, Event: AppWebhookEventRealtimeChannelPublished, SourceID: id, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: msg.CreatedAt}
}
