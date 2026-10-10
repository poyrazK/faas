package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

const ManagedRealtimeReducerMaxEntities = 128
const ManagedRealtimeReducerMaxVersionKeys = 256
const managedRealtimeMaxEntityVersion int64 = 9007199254740991

type ManagedRealtimeEntityVersionConflict struct {
	Key               string
	Expected, Current int64
	Exists            bool
	Item              int
}

func (e *ManagedRealtimeEntityVersionConflict) Error() string {
	return fmt.Sprintf("entity %q: expected version %d; current version is %d", e.Key, e.Expected, e.Current)
}

func cloneReducerVersions(versions map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(versions))
	for key, version := range versions {
		out[key] = version
	}
	return out
}
func seedReducerVersions(entities map[string]map[string]json.RawMessage) map[string]int64 {
	versions := make(map[string]int64, len(entities))
	for key := range entities {
		versions[key] = 1
	}
	return versions
}

var ErrManagedRealtimeReducerActive = errors.New("state: reducer owns channel state")

type ManagedRealtimeReducerError struct {
	Item   int
	Reason string
}

func (e *ManagedRealtimeReducerError) Error() string {
	if e.Item >= 0 {
		return fmt.Sprintf("messages[%d]: %s", e.Item, e.Reason)
	}
	return e.Reason
}

type ManagedRealtimeReducerState struct {
	EndpointID        string               `json:"-"`
	Channel           string               `json:"channel"`
	Sequence          int64                `json:"sequence"`
	Entities          json.RawMessage      `json:"entities"`
	EntityVersions    map[string]int64     `json:"entity_versions"`
	EntityExpirations map[string]time.Time `json:"entity_expirations"`
	UpdatedAt         time.Time            `json:"updated_at"`
}
type ManagedRealtimeReducerStore interface {
	PutManagedRealtimeReducer(context.Context, ManagedRealtimeReducerState) (ManagedRealtimeReducerState, error)
	GetManagedRealtimeReducer(context.Context, string, string) (ManagedRealtimeReducerState, error)
	DeleteManagedRealtimeReducer(context.Context, string, string) error
}

func reducerKeyValid(key string) bool {
	return key != "" && len(key) <= 128 && strings.TrimSpace(key) == key && !strings.ContainsAny(key, "\x00\r\n")
}
func decodeReducerEntities(raw []byte) (map[string]map[string]json.RawMessage, error) {
	if len(raw) > 65536 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var entities map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &entities) != nil || entities == nil || len(entities) > 128 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	for key, value := range entities {
		if !reducerKeyValid(key) || value == nil {
			return nil, ErrManagedRealtimeHistoryInvalid
		}
	}
	return entities, nil
}
func reducerSnapshot(row ManagedRealtimeReducerState, now time.Time) ManagedRealtimeChannelSnapshot {
	return ManagedRealtimeChannelSnapshot{EndpointID: row.EndpointID, Channel: row.Channel, Sequence: row.Sequence, Data: append([]byte(nil), row.Entities...), EntityVersions: cloneReducerVersions(row.EntityVersions), EntityExpirations: cloneReducerExpirations(row.EntityExpirations), UpdatedAt: row.UpdatedAt, ExpiresAt: now.Add(24 * time.Hour)}
}
func reduceRealtimeEvents(row ManagedRealtimeReducerState, messages []ManagedRealtimeChannelMessage) (ManagedRealtimeReducerState, error) {
	entities, err := decodeReducerEntities(row.Entities)
	if err != nil {
		return row, err
	}
	row.EntityExpirations = cloneReducerExpirations(row.EntityExpirations)
	row.EntityVersions = cloneReducerVersions(row.EntityVersions)
	for key := range entities {
		if row.EntityVersions[key] == 0 {
			row.EntityVersions[key] = 1
		}
	}
	for i, msg := range messages {
		fail := func(reason string) error { return &ManagedRealtimeReducerError{Item: i, Reason: reason} }
		if msg.Sequence != row.Sequence+1 {
			return row, fail("reducer sequence is not contiguous")
		}
		if msg.Binary {
			return row, fail("reducer events must be JSON")
		}
		var operation struct {
			Conditions      json.RawMessage            `json:"conditions"`
			Items           json.RawMessage            `json:"items"`
			Unique          json.RawMessage            `json:"unique"`
			MaxLength       json.RawMessage            `json:"max_length"`
			Field           string                     `json:"field"`
			Delta           json.RawMessage            `json:"delta"`
			Min             json.RawMessage            `json:"min"`
			Max             json.RawMessage            `json:"max"`
			ExpiresAt       json.RawMessage            `json:"expires_at"`
			ExpectedVersion *int64                     `json:"expected_version"`
			Op              string                     `json:"op"`
			Key             string                     `json:"key"`
			Value           map[string]json.RawMessage `json:"value"`
		}
		if json.Unmarshal(msg.Data, &operation) != nil || !reducerKeyValid(operation.Key) {
			return row, fail("reducer event requires op and a key of 1..128 bytes")
		}
		var expiresAt *time.Time
		if len(operation.ExpiresAt) > 0 && string(operation.ExpiresAt) != "null" {
			var deadline time.Time
			if json.Unmarshal(operation.ExpiresAt, &deadline) != nil || !deadline.After(msg.CreatedAt) || deadline.After(msg.CreatedAt.Add(30*24*time.Hour)) {
				return row, fail("expires_at must be a future RFC3339 timestamp within 30 days")
			}
			expiresAt = &deadline
		}
		if operation.Op == "delete" && len(operation.ExpiresAt) > 0 {
			return row, fail("delete does not accept expires_at")
		}
		currentVersion := row.EntityVersions[operation.Key]
		if operation.ExpectedVersion != nil {
			if *operation.ExpectedVersion < 0 || *operation.ExpectedVersion > managedRealtimeMaxEntityVersion {
				return row, fail("expected_version must be a nonnegative safe integer")
			}
			if *operation.ExpectedVersion != currentVersion {
				_, exists := entities[operation.Key]
				return row, &ManagedRealtimeEntityVersionConflict{Key: operation.Key, Expected: *operation.ExpectedVersion, Current: currentVersion, Exists: exists, Item: i}
			}
		}
		if err := checkReducerConditions(operation.Conditions, operation.Key, entities[operation.Key], currentVersion, i); err != nil {
			return row, err
		}
		if currentVersion >= managedRealtimeMaxEntityVersion {
			return row, fail("entity version exhausted")
		}
		if currentVersion == 0 && len(row.EntityVersions) >= ManagedRealtimeReducerMaxVersionKeys {
			return row, fail("reducer exceeds 256 versioned keys, including deleted entities")
		}
		switch operation.Op {
		case "set":
			if operation.Value == nil {
				return row, fail("set requires an object value")
			}
			entities[operation.Key] = operation.Value
		case "merge":
			if operation.Value == nil {
				return row, fail("merge requires an object value")
			}
			current := entities[operation.Key]
			if current == nil {
				current = map[string]json.RawMessage{}
			}
			for field, value := range operation.Value {
				current[field] = value
			}
			entities[operation.Key] = current
		case "increment":
			current, reason := incrementReducerCounter(entities[operation.Key], operation.Field, operation.Delta, operation.Min, operation.Max)
			if reason != "" {
				return row, fail(reason)
			}
			entities[operation.Key] = current
		case "append", "remove":
			current, reason := mutateReducerArray(entities[operation.Key], operation.Op, operation.Field, operation.Items, operation.Unique, operation.MaxLength)
			if reason != "" {
				return row, fail(reason)
			}
			entities[operation.Key] = current
		case "delete":
			delete(entities, operation.Key)
		default:
			return row, fail("reducer op must be set, merge, increment, append, remove or delete")
		}
		if operation.Op == "delete" || (len(operation.ExpiresAt) > 0 && expiresAt == nil) || (operation.Op == "set" && expiresAt == nil) {
			delete(row.EntityExpirations, operation.Key)
		} else if expiresAt != nil {
			row.EntityExpirations[operation.Key] = expiresAt.UTC()
		}
		if _, scheduled := row.EntityExpirations[operation.Key]; scheduled && currentVersion >= managedRealtimeMaxEntityVersion-1 {
			return row, fail("entity version must leave room for expiration")
		}
		if len(entities) > 128 {
			return row, fail("reducer exceeds 128 entities")
		}
		data, _ := json.Marshal(entities)
		if len(data) > 65536 {
			return row, fail("reducer state exceeds 64 KiB")
		}
		row.EntityVersions[operation.Key] = currentVersion + 1
		row.Entities = data
		row.Sequence = msg.Sequence
		row.UpdatedAt = msg.CreatedAt
	}
	return row, nil
}
func (m *MemStore) prepareReducerLocked(ep, ch string, messages []ManagedRealtimeChannelMessage) (*ManagedRealtimeReducerState, error) {
	row, ok := m.managedRealtimeReducers[managedRealtimeHistoryKey{endpointID: ep, channel: ch}]
	if !ok {
		return nil, nil
	}
	updated, err := reduceRealtimeEvents(row, messages)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}
func (m *MemStore) saveReducerLocked(row *ManagedRealtimeReducerState) {
	if row == nil {
		return
	}
	key := managedRealtimeHistoryKey{endpointID: row.EndpointID, channel: row.Channel}
	if m.managedRealtimeSnapshots == nil {
		m.managedRealtimeSnapshots = map[managedRealtimeHistoryKey]ManagedRealtimeChannelSnapshot{}
	}
	m.managedRealtimeReducers[key] = *row
	m.managedRealtimeSnapshots[key] = reducerSnapshot(*row, time.Now().UTC())
}
func readReducerPG(ctx context.Context, tx pgx.Tx, ep, ch string) (*ManagedRealtimeReducerState, error) {
	row := ManagedRealtimeReducerState{EndpointID: ep, Channel: ch}
	err := tx.QueryRow(ctx, `select sequence,entities,entity_versions,entity_expirations,updated_at from managed_realtime_channel_reducers where endpoint_id=$1 and channel=$2`, ep, ch).Scan(&row.Sequence, &row.Entities, &row.EntityVersions, &row.EntityExpirations, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &row, err
}
func applyReducerPG(ctx context.Context, tx pgx.Tx, ep, ch string, messages []ManagedRealtimeChannelMessage) error {
	row, err := readReducerPG(ctx, tx, ep, ch)
	if err != nil || row == nil {
		return err
	}
	updated, err := reduceRealtimeEvents(*row, messages)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_channel_reducers set sequence=$3,entities=$4,entity_versions=$5,entity_expirations=$6,next_expiry=$7,updated_at=$8 where endpoint_id=$1 and channel=$2`, ep, ch, updated.Sequence, updated.Entities, updated.EntityVersions, updated.EntityExpirations, nextReducerExpiry(updated.EntityExpirations), updated.UpdatedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into managed_realtime_channel_snapshots(endpoint_id,channel,sequence,data,is_binary,updated_at,expires_at) values($1,$2,$3,$4,false,$5,$6) on conflict(endpoint_id,channel) do update set sequence=excluded.sequence,data=excluded.data,is_binary=false,updated_at=excluded.updated_at,expires_at=excluded.expires_at`, ep, ch, updated.Sequence, []byte(updated.Entities), updated.UpdatedAt, time.Now().UTC().Add(24*time.Hour))
	return err
}
func (m *MemStore) PutManagedRealtimeReducer(ctx context.Context, row ManagedRealtimeReducerState) (ManagedRealtimeReducerState, error) {
	if validateManagedRealtimeHistoryRequest(row.EndpointID, row.Channel) != nil || row.Sequence < 0 {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	entities, err := decodeReducerEntities(row.Entities)
	if err != nil {
		return row, err
	}
	row.Entities, _ = json.Marshal(entities)
	row.EntityVersions = seedReducerVersions(entities)
	row.EntityExpirations = map[string]time.Time{}
	if len(row.Entities) > 65536 {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	if err = ctx.Err(); err != nil {
		return row, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[row.EndpointID]; !ok {
		return row, ErrNotFound
	}
	key := managedRealtimeHistoryKey{endpointID: row.EndpointID, channel: row.Channel}
	if old, ok := m.managedRealtimeReducers[key]; ok {
		if old.Sequence != row.Sequence || !bytes.Equal(old.Entities, row.Entities) {
			return row, ErrConflict
		}
		old.EntityExpirations = cloneReducerExpirations(old.EntityExpirations)
		old.EntityVersions = cloneReducerVersions(old.EntityVersions)
		old.Entities = append(json.RawMessage(nil), old.Entities...)
		return old, nil
	}
	h := m.managedRealtimeHistory[key]
	if h == nil {
		if row.Sequence != 0 {
			return row, ErrConflict
		}
		channels := 0
		for key := range m.managedRealtimeHistory {
			if key.endpointID == row.EndpointID {
				channels++
			}
		}
		if channels >= 32 {
			return row, ErrManagedRealtimeHistoryLimit
		}
		h = &managedRealtimeHistoryState{next: 1, oldest: 1}
	}
	if row.Sequence != h.next-1 {
		return row, ErrConflict
	}
	m.managedRealtimeHistory[key] = h
	if m.managedRealtimeReducers == nil {
		m.managedRealtimeReducers = map[managedRealtimeHistoryKey]ManagedRealtimeReducerState{}
	}
	row.UpdatedAt = time.Now().UTC()
	m.saveReducerLocked(&row)
	row.EntityExpirations = cloneReducerExpirations(row.EntityExpirations)
	row.EntityVersions = cloneReducerVersions(row.EntityVersions)
	row.Entities = append(json.RawMessage(nil), row.Entities...)
	return row, nil
}
func (m *MemStore) GetManagedRealtimeReducer(ctx context.Context, ep, ch string) (ManagedRealtimeReducerState, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return ManagedRealtimeReducerState{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeReducerState{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.managedRealtimeReducers[managedRealtimeHistoryKey{endpointID: ep, channel: ch}]
	if !ok {
		return row, ErrNotFound
	}
	row.EntityExpirations = cloneReducerExpirations(row.EntityExpirations)
	row.EntityVersions = cloneReducerVersions(row.EntityVersions)
	row.Entities = append(json.RawMessage(nil), row.Entities...)
	return row, nil
}
func (m *MemStore) DeleteManagedRealtimeReducer(ctx context.Context, ep, ch string) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managedRealtimeHistoryKey{endpointID: ep, channel: ch}
	if _, ok := m.managedRealtimeReducers[key]; ok {
		delete(m.managedRealtimeReducers, key)
		delete(m.managedRealtimeSnapshots, key)
	}
	return nil
}
func (s *PgStore) PutManagedRealtimeReducer(ctx context.Context, row ManagedRealtimeReducerState) (ManagedRealtimeReducerState, error) {
	if validateManagedRealtimeHistoryRequest(row.EndpointID, row.Channel) != nil || row.Sequence < 0 {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	entities, err := decodeReducerEntities(row.Entities)
	if err != nil {
		return row, err
	}
	row.Entities, _ = json.Marshal(entities)
	row.EntityVersions = seedReducerVersions(entities)
	row.EntityExpirations = map[string]time.Time{}
	if len(row.Entities) > 65536 {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return row, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, row.EndpointID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return row, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2)`, row.EndpointID, row.Channel).Scan(&exists); err != nil {
		return row, err
	}
	if !exists {
		var count int
		if err = tx.QueryRow(ctx, `select count(*) from managed_realtime_channel_heads where endpoint_id=$1`, row.EndpointID).Scan(&count); err != nil {
			return row, err
		}
		if count >= 32 {
			return row, ErrManagedRealtimeHistoryLimit
		}
	}
	if _, err = tx.Exec(ctx, `insert into managed_realtime_channel_heads(endpoint_id,channel) values($1,$2) on conflict do nothing`, row.EndpointID, row.Channel); err != nil {
		return row, err
	}
	var next int64
	if err = tx.QueryRow(ctx, `select next_sequence from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2 for update`, row.EndpointID, row.Channel).Scan(&next); err != nil {
		return row, err
	}
	old, err := readReducerPG(ctx, tx, row.EndpointID, row.Channel)
	if err != nil {
		return row, err
	}
	if old != nil {
		if old.Sequence != row.Sequence || !bytes.Equal(old.Entities, row.Entities) {
			return row, ErrConflict
		}
		return *old, tx.Commit(ctx)
	}
	if row.Sequence != next-1 {
		return row, ErrConflict
	}
	row.UpdatedAt = time.Now().UTC()
	_, err = tx.Exec(ctx, `insert into managed_realtime_channel_reducers(endpoint_id,channel,sequence,entities,entity_versions,entity_expirations,updated_at) values($1,$2,$3,$4,$5,$6,$7)`, row.EndpointID, row.Channel, row.Sequence, row.Entities, row.EntityVersions, row.EntityExpirations, row.UpdatedAt)
	if err != nil {
		return row, err
	}
	if err = applyReducerPG(ctx, tx, row.EndpointID, row.Channel, nil); err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}
func (s *PgStore) GetManagedRealtimeReducer(ctx context.Context, ep, ch string) (ManagedRealtimeReducerState, error) {
	row := ManagedRealtimeReducerState{EndpointID: ep, Channel: ch}
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	err := s.pool.QueryRow(ctx, `select sequence,entities,entity_versions,entity_expirations,updated_at from managed_realtime_channel_reducers where endpoint_id=$1 and channel=$2`, ep, ch).Scan(&row.Sequence, &row.Entities, &row.EntityVersions, &row.EntityExpirations, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return row, err
}
func (s *PgStore) DeleteManagedRealtimeReducer(ctx context.Context, ep, ch string) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `select next_sequence from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2 for update`, ep, ch); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `delete from managed_realtime_channel_reducers where endpoint_id=$1 and channel=$2`, ep, ch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if _, err = tx.Exec(ctx, `delete from managed_realtime_channel_snapshots where endpoint_id=$1 and channel=$2`, ep, ch); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
