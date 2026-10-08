package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"time"
)

var ErrManagedRealtimeBatchLimit = errors.New("state: batch idempotency ledger limit reached")

const ManagedRealtimeBatchMaxMessages = 32
const ManagedRealtimeBatchMaxBytes = 64 << 10

type ManagedRealtimeBatchItem struct {
	Metadata map[string]string `json:"Metadata,omitempty"`
	Data     []byte
	Binary   bool
}
type managedRealtimeBatchKey struct{ endpointID, channel, id string }
type managedRealtimeBatchRecord struct {
	Hash      []byte
	First     int64
	CreatedAt time.Time
}
type ManagedRealtimeBatchStore interface {
	AppendManagedRealtimeChannelBatch(context.Context, string, string, string, []ManagedRealtimeBatchItem) ([]ManagedRealtimeChannelMessage, error)
}

func validateBatch(ep, ch, id string, items []ManagedRealtimeBatchItem) ([]byte, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || id == "" || len(id) > 128 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "\x00\r\n") || len(items) < 1 || len(items) > 32 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	total := 0
	for _, item := range items {
		total += len(item.Data)
		if api.ValidateRealtimeMetadata(item.Metadata) != nil || len(item.Data) > ManagedRealtimeHistoryMaxPayloadBytes {
			return nil, ErrManagedRealtimeHistoryInvalid
		}
	}
	if total > ManagedRealtimeBatchMaxBytes {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	canonical := make([]ManagedRealtimeBatchItem, len(items))
	for i, item := range items {
		canonical[i] = item
		canonical[i].Metadata = cloneRealtimeMetadata(item.Metadata)
		if len(canonical[i].Metadata) == 0 {
			canonical[i].Metadata = nil
		}
		if canonical[i].Data == nil {
			canonical[i].Data = []byte{}
		}
	}
	raw, _ := json.Marshal(canonical)
	hash := sha256.Sum256(raw)
	return hash[:], nil
}
func batchMessages(ep, ch, id string, items []ManagedRealtimeBatchItem, first int64, created time.Time) []ManagedRealtimeChannelMessage {
	out := make([]ManagedRealtimeChannelMessage, len(items))
	hash := sha256.Sum256([]byte(id))
	for i, item := range items {
		key := fmt.Sprintf("batch:%x:%d", hash, i)
		out[i] = ManagedRealtimeChannelMessage{Metadata: cloneRealtimeMetadata(item.Metadata), EndpointID: ep, Channel: ch, Sequence: first + int64(i), Data: append([]byte(nil), item.Data...), Binary: item.Binary, CreatedAt: created, IdempotencyKey: key, TargetMessageID: key, Version: 1, MessageEvent: "created"}
	}
	return out
}
func (m *MemStore) AppendManagedRealtimeChannelBatch(ctx context.Context, ep, ch, id string, items []ManagedRealtimeBatchItem) ([]ManagedRealtimeChannelMessage, error) {
	return m.AppendManagedRealtimeBatchConditional(ctx, ep, ch, id, items, nil)
}
func (m *MemStore) AppendManagedRealtimeBatchConditional(ctx context.Context, ep, ch, id string, items []ManagedRealtimeBatchItem, expected *int64) ([]ManagedRealtimeChannelMessage, error) {
	if expected != nil && *expected < 0 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	hash, err := validateBatch(ep, ch, id, items)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	if m.managedRealtimeBatches == nil {
		m.managedRealtimeBatches = map[managedRealtimeBatchKey]managedRealtimeBatchRecord{}
	}
	count := 0
	for key, record := range m.managedRealtimeBatches {
		if key.endpointID == ep {
			if !record.CreatedAt.After(now.Add(-24 * time.Hour)) {
				delete(m.managedRealtimeBatches, key)
			} else {
				count++
			}
		}
	}
	key := managedRealtimeBatchKey{ep, ch, id}
	if old, ok := m.managedRealtimeBatches[key]; ok {
		if string(old.Hash) != string(hash) {
			return nil, ErrConflict
		}
		return batchMessages(ep, ch, id, items, old.First, old.CreatedAt), nil
	}
	current := int64(0)
	if history := m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: ep, channel: ch}]; history != nil {
		current = history.next - 1
	}
	if err := checkExpectedRealtimeSequence(expected, current); err != nil {
		return nil, err
	}
	if count >= 256 {
		return nil, ErrManagedRealtimeBatchLimit
	}
	for i, item := range items {
		if err := m.validateEventSchemaLocked(ep, ch, item.Data, item.Binary, item.Metadata); err != nil {
			var detail *ManagedRealtimeEventSchemaError
			if errors.As(err, &detail) {
				detail.Item = i
			}
			return nil, err
		}
	}
	hkey := managedRealtimeHistoryKey{endpointID: ep, channel: ch}
	h := m.managedRealtimeHistory[hkey]
	if h == nil {
		channels := 0
		for key := range m.managedRealtimeHistory {
			if key.endpointID == ep {
				channels++
			}
		}
		if channels >= 32 {
			return nil, ErrManagedRealtimeHistoryLimit
		}
		h = &managedRealtimeHistoryState{next: 1, oldest: 1}
		m.managedRealtimeHistory[hkey] = h
	}
	h.trimExpired(now.Add(-24 * time.Hour))
	messages := batchMessages(ep, ch, id, items, h.next, now)
	for _, incoming := range messages {
		for _, old := range h.messages {
			if old.TargetMessageID == incoming.TargetMessageID {
				return nil, ErrConflict
			}
		}
	}
	reducer, err := m.prepareReducerLocked(ep, ch, messages)
	if err != nil {
		return nil, err
	}
	for _, msg := range messages {
		h.messages = append(h.messages, cloneManagedRealtimeChannelMessage(msg))
	}
	m.saveReducerLocked(reducer)
	h.next += int64(len(items))
	if len(h.messages) > ManagedRealtimeHistoryMaxMessages {
		drop := len(h.messages) - ManagedRealtimeHistoryMaxMessages
		h.messages = append([]ManagedRealtimeChannelMessage(nil), h.messages[drop:]...)
		h.oldest = h.messages[0].Sequence
	}
	m.managedRealtimeBatches[key] = managedRealtimeBatchRecord{hash, messages[0].Sequence, now}
	return messages, nil
}
func (s *PgStore) AppendManagedRealtimeChannelBatch(ctx context.Context, ep, ch, id string, items []ManagedRealtimeBatchItem) ([]ManagedRealtimeChannelMessage, error) {
	return s.AppendManagedRealtimeBatchConditional(ctx, ep, ch, id, items, nil)
}
func (s *PgStore) AppendManagedRealtimeBatchConditional(ctx context.Context, ep, ch, id string, items []ManagedRealtimeBatchItem, expected *int64) ([]ManagedRealtimeChannelMessage, error) {
	if expected != nil && *expected < 0 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	hash, err := validateBatch(ep, ch, id, items)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	if err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, ep).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return nil, err
	}
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `delete from managed_realtime_channel_batches where endpoint_id=$1 and created_at<=$2`, ep, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	var old managedRealtimeBatchRecord
	err = tx.QueryRow(ctx, `select payload_hash,first_sequence,created_at from managed_realtime_channel_batches where endpoint_id=$1 and channel=$2 and batch_id=$3`, ep, ch, id).Scan(&old.Hash, &old.First, &old.CreatedAt)
	if err == nil {
		if string(old.Hash) != string(hash) {
			return nil, ErrConflict
		}
		return batchMessages(ep, ch, id, items, old.First, old.CreatedAt), tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var count int
	if err = tx.QueryRow(ctx, `select count(*) from managed_realtime_channel_batches where endpoint_id=$1`, ep).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 256 {
		return nil, ErrManagedRealtimeBatchLimit
	}
	var exists bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2)`, ep, ch).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		if err = tx.QueryRow(ctx, `select count(*) from managed_realtime_channel_heads where endpoint_id=$1`, ep).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 32 {
			return nil, ErrManagedRealtimeHistoryLimit
		}
	}
	if _, err = tx.Exec(ctx, `insert into managed_realtime_channel_heads(endpoint_id,channel) values($1,$2) on conflict do nothing`, ep, ch); err != nil {
		return nil, err
	}
	var next, floor int64
	if err = tx.QueryRow(ctx, `select next_sequence,oldest_sequence from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2 for update`, ep, ch).Scan(&next, &floor); err != nil {
		return nil, err
	}
	if err := checkExpectedRealtimeSequence(expected, next-1); err != nil {
		return nil, err
	}
	var expired int64
	if err = tx.QueryRow(ctx, `select coalesce(max(sequence)+1,0) from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2 and created_at<$3`, ep, ch, now.Add(-24*time.Hour)).Scan(&expired); err != nil {
		return nil, err
	}
	if expired > floor {
		floor = expired
	}
	for i, item := range items {
		if err := validateEventSchemaPG(ctx, tx, ep, ch, item.Data, item.Binary, item.Metadata); err != nil {
			var detail *ManagedRealtimeEventSchemaError
			if errors.As(err, &detail) {
				detail.Item = i
			}
			return nil, err
		}
	}
	messages := batchMessages(ep, ch, id, items, next, now)
	for _, msg := range messages {
		var duplicate bool
		if err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2 and target_message_id=$3)`, ep, ch, msg.TargetMessageID).Scan(&duplicate); err != nil {
			return nil, err
		}
		if duplicate {
			return nil, ErrConflict
		}
	}
	for _, msg := range messages {
		data := msg.Data
		if data == nil {
			data = []byte{}
		}
		_, err = tx.Exec(ctx, `insert into managed_realtime_channel_messages(endpoint_id,channel,sequence,data,is_binary,idempotency_key,created_at,metadata) values($1,$2,$3,$4,$5,$6,$7,$8)`, ep, ch, msg.Sequence, data, msg.Binary, msg.IdempotencyKey, now, metadataJSON(msg.Metadata))
		if err != nil {
			return nil, err
		}
	}
	if err = applyReducerPG(ctx, tx, ep, ch, messages); err != nil {
		return nil, err
	}
	next += int64(len(items))
	if limit := next - ManagedRealtimeHistoryMaxMessages; limit > floor {
		floor = limit
	}
	if _, err = tx.Exec(ctx, `update managed_realtime_channel_heads set next_sequence=$3,oldest_sequence=$4 where endpoint_id=$1 and channel=$2`, ep, ch, next, floor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `delete from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2 and sequence<$3`, ep, ch, floor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `insert into managed_realtime_channel_batches(endpoint_id,channel,batch_id,payload_hash,first_sequence,message_count,created_at) values($1,$2,$3,$4,$5,$6,$7)`, ep, ch, id, hash, messages[0].Sequence, len(items), now); err != nil {
		return nil, err
	}
	return messages, tx.Commit(ctx)
}
