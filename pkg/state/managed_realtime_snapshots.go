package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

const ManagedRealtimeSnapshotMaxBytes = 64 << 10

type ManagedRealtimeChannelSnapshot struct {
	EntityExpirations    map[string]time.Time `json:"entity_expirations,omitempty"`
	EndpointID, Channel  string
	Sequence             int64
	EntityVersions       map[string]int64 `json:"entity_versions,omitempty"`
	Data                 []byte
	Binary               bool
	UpdatedAt, ExpiresAt time.Time
}
type ManagedRealtimeSnapshotStore interface {
	PutManagedRealtimeChannelSnapshot(context.Context, ManagedRealtimeChannelSnapshot) (ManagedRealtimeChannelSnapshot, error)
	GetManagedRealtimeChannelSnapshot(context.Context, string, string) (ManagedRealtimeChannelSnapshot, error)
	DeleteManagedRealtimeChannelSnapshot(context.Context, string, string) error
}

func validateSnapshot(j ManagedRealtimeChannelSnapshot) error {
	if validateManagedRealtimeHistoryRequest(j.EndpointID, j.Channel) != nil || j.Sequence < 0 || len(j.Data) > ManagedRealtimeSnapshotMaxBytes {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}
func snapshotFloor(h *managedRealtimeHistoryState, now time.Time) int64 {
	floor := h.oldest
	for _, msg := range h.messages {
		if msg.CreatedAt.Before(now.Add(-ManagedRealtimeHistoryRetention)) && msg.Sequence >= floor {
			floor = msg.Sequence + 1
		}
	}
	return floor
}
func (m *MemStore) PutManagedRealtimeChannelSnapshot(ctx context.Context, j ManagedRealtimeChannelSnapshot) (ManagedRealtimeChannelSnapshot, error) {
	if err := validateSnapshot(j); err != nil {
		return j, err
	}
	if err := ctx.Err(); err != nil {
		return j, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managedRealtimeHistoryKey{endpointID: j.EndpointID, channel: j.Channel}
	if _, active := m.managedRealtimeReducers[key]; active {
		return j, ErrManagedRealtimeReducerActive
	}
	h := m.managedRealtimeHistory[key]
	if h == nil {
		return j, ErrNotFound
	}
	now := time.Now().UTC()
	if j.Sequence > h.next-1 {
		return j, ErrManagedRealtimeHistoryInvalid
	}
	if j.Sequence < snapshotFloor(h, now)-1 {
		return j, ErrManagedRealtimeDurableCursorExpired
	}
	if m.managedRealtimeSnapshots == nil {
		m.managedRealtimeSnapshots = map[managedRealtimeHistoryKey]ManagedRealtimeChannelSnapshot{}
	}
	if old, ok := m.managedRealtimeSnapshots[key]; ok && old.Sequence > j.Sequence {
		return j, ErrManagedRealtimeHistoryInvalid
	}
	j.UpdatedAt = now
	j.ExpiresAt = now.Add(24 * time.Hour)
	j.Data = append([]byte(nil), j.Data...)
	m.managedRealtimeSnapshots[key] = j
	j.Data = append([]byte(nil), j.Data...)
	return j, nil
}
func (m *MemStore) GetManagedRealtimeChannelSnapshot(ctx context.Context, ep, ch string) (ManagedRealtimeChannelSnapshot, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return ManagedRealtimeChannelSnapshot{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managedRealtimeHistoryKey{endpointID: ep, channel: ch}
	if row, active := m.managedRealtimeReducers[key]; active {
		return reducerSnapshot(row, time.Now().UTC()), nil
	}
	j, ok := m.managedRealtimeSnapshots[key]
	if !ok {
		return j, ErrNotFound
	}
	now := time.Now().UTC()
	h := m.managedRealtimeHistory[key]
	if !j.ExpiresAt.After(now) || h == nil || j.Sequence < snapshotFloor(h, now)-1 {
		return j, ErrManagedRealtimeDurableCursorExpired
	}
	j.Data = append([]byte(nil), j.Data...)
	return j, nil
}
func (m *MemStore) DeleteManagedRealtimeChannelSnapshot(ctx context.Context, ep, ch string) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, active := m.managedRealtimeReducers[managedRealtimeHistoryKey{endpointID: ep, channel: ch}]; active {
		return ErrManagedRealtimeReducerActive
	}
	delete(m.managedRealtimeSnapshots, managedRealtimeHistoryKey{endpointID: ep, channel: ch})
	return nil
}
func (s *PgStore) PutManagedRealtimeChannelSnapshot(ctx context.Context, j ManagedRealtimeChannelSnapshot) (ManagedRealtimeChannelSnapshot, error) {
	if err := validateSnapshot(j); err != nil {
		return j, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return j, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var next, floor int64
	err = tx.QueryRow(ctx, `select next_sequence,oldest_sequence from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2 for update`, j.EndpointID, j.Channel).Scan(&next, &floor)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if reducer, e := readReducerPG(ctx, tx, j.EndpointID, j.Channel); e != nil {
		return j, e
	} else if reducer != nil {
		return j, ErrManagedRealtimeReducerActive
	}
	var expiredFloor int64
	err = tx.QueryRow(ctx, `select coalesce(max(sequence)+1,0) from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2 and created_at<clock_timestamp()-interval '24 hours'`, j.EndpointID, j.Channel).Scan(&expiredFloor)
	if err != nil {
		return j, err
	}
	if expiredFloor > floor {
		floor = expiredFloor
	}
	if j.Sequence > next-1 {
		return j, ErrManagedRealtimeHistoryInvalid
	}
	if j.Sequence < floor-1 {
		return j, ErrManagedRealtimeDurableCursorExpired
	}
	err = tx.QueryRow(ctx, `insert into managed_realtime_channel_snapshots(endpoint_id,channel,sequence,data,is_binary) values($1,$2,$3,$4,$5) on conflict(endpoint_id,channel) do update set sequence=excluded.sequence,data=excluded.data,is_binary=excluded.is_binary,updated_at=clock_timestamp(),expires_at=clock_timestamp()+interval '24 hours' where managed_realtime_channel_snapshots.sequence<=excluded.sequence returning updated_at,expires_at`, j.EndpointID, j.Channel, j.Sequence, j.Data, j.Binary).Scan(&j.UpdatedAt, &j.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrManagedRealtimeHistoryInvalid
	}
	if err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) GetManagedRealtimeChannelSnapshot(ctx context.Context, ep, ch string) (ManagedRealtimeChannelSnapshot, error) {
	j := ManagedRealtimeChannelSnapshot{EndpointID: ep, Channel: ch}
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return j, ErrManagedRealtimeHistoryInvalid
	}
	if reducer, e := s.GetManagedRealtimeReducer(ctx, ep, ch); e == nil {
		return reducerSnapshot(reducer, time.Now().UTC()), nil
	} else if !errors.Is(e, ErrNotFound) {
		return j, e
	}
	err := s.pool.QueryRow(ctx, `select sequence,data,is_binary,updated_at,expires_at from managed_realtime_channel_snapshots where endpoint_id=$1 and channel=$2`, ep, ch).Scan(&j.Sequence, &j.Data, &j.Binary, &j.UpdatedAt, &j.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if !j.ExpiresAt.After(time.Now()) {
		return j, ErrManagedRealtimeDurableCursorExpired
	}
	h, err := s.ReadManagedRealtimeChannelHistory(ctx, ep, ch, j.Sequence, 1)
	if err != nil {
		return j, err
	}
	if h.HistoryUnavailable {
		return j, ErrManagedRealtimeDurableCursorExpired
	}
	return j, nil
}
func (s *PgStore) DeleteManagedRealtimeChannelSnapshot(ctx context.Context, ep, ch string) error {
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
	row, err := readReducerPG(ctx, tx, ep, ch)
	if err != nil {
		return err
	}
	if row != nil {
		return ErrManagedRealtimeReducerActive
	}
	if _, err = tx.Exec(ctx, `delete from managed_realtime_channel_snapshots where endpoint_id=$1 and channel=$2`, ep, ch); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
