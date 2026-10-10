package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// Candidates are advisory. ExpireManagedRealtimeEntity rechecks them under the
// same channel lock used for retained publishing before committing a delete.
type ManagedRealtimeEntityExpiration struct {
	EndpointID, Channel, Key string
	Version                  int64
	ExpiresAt                time.Time
}
type ManagedRealtimeEntityExpirationStore interface {
	ListManagedRealtimeEntityExpirations(context.Context, int) ([]ManagedRealtimeEntityExpiration, error)
	ExpireManagedRealtimeEntity(context.Context, ManagedRealtimeEntityExpiration) (ManagedRealtimeChannelMessage, error)
}

func cloneReducerExpirations(values map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
func nextReducerExpiry(values map[string]time.Time) *time.Time {
	var next *time.Time
	for _, deadline := range values {
		if next == nil || deadline.Before(*next) {
			d := deadline
			next = &d
		}
	}
	return next
}
func reducerExpirationMatches(row ManagedRealtimeReducerState, candidate ManagedRealtimeEntityExpiration, now time.Time) bool {
	deadline, ok := row.EntityExpirations[candidate.Key]
	return ok && row.EntityVersions[candidate.Key] == candidate.Version && deadline.Equal(candidate.ExpiresAt) && !deadline.After(now)
}
func expirationPayload(candidate ManagedRealtimeEntityExpiration) []byte {
	data, _ := json.Marshal(struct {
		Op              string `json:"op"`
		Key             string `json:"key"`
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}{"delete", candidate.Key, candidate.Version, "expired"})
	return data
}
func (m *MemStore) ListManagedRealtimeEntityExpirations(ctx context.Context, limit int) ([]ManagedRealtimeEntityExpiration, error) {
	if limit < 1 || limit > 256 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	// Keep a bounded earliest-deadline queue even when many entities are due.
	out := make([]ManagedRealtimeEntityExpiration, 0, limit)
	for _, row := range m.managedRealtimeReducers {
		for key, deadline := range row.EntityExpirations {
			if deadline.After(now) {
				continue
			}
			candidate := ManagedRealtimeEntityExpiration{row.EndpointID, row.Channel, key, row.EntityVersions[key], deadline}
			index := sort.Search(len(out), func(i int) bool { return !out[i].ExpiresAt.Before(deadline) })
			if index >= limit {
				continue
			}
			out = append(out, candidate)
			copy(out[index+1:], out[index:len(out)-1])
			out[index] = candidate
			if len(out) > limit {
				out = out[:limit]
			}
		}
	}
	return out, nil
}
func (s *PgStore) ListManagedRealtimeEntityExpirations(ctx context.Context, limit int) ([]ManagedRealtimeEntityExpiration, error) {
	if limit < 1 || limit > 256 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	// Limit reducers first so JSON expansion stays bounded (128 live entities each).
	rows, err := s.pool.Query(ctx, `with due as (
 select endpoint_id,channel,entity_versions,entity_expirations from managed_realtime_channel_reducers
 where next_expiry<=clock_timestamp() order by next_expiry,endpoint_id,channel limit $1
 ) select endpoint_id,channel,e.key,(entity_versions->>e.key)::bigint,(e.value#>>'{}')
 from due cross join lateral json_each(entity_expirations) e
 where (e.value#>>'{}')::timestamptz<=clock_timestamp()
 order by (e.value#>>'{}')::timestamptz,endpoint_id,channel,e.key limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ManagedRealtimeEntityExpiration, 0)
	for rows.Next() {
		var candidate ManagedRealtimeEntityExpiration
		var deadline string
		if err := rows.Scan(&candidate.EndpointID, &candidate.Channel, &candidate.Key, &candidate.Version, &deadline); err != nil {
			return nil, err
		}
		candidate.ExpiresAt, err = time.Parse(time.RFC3339Nano, deadline)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}
func (m *MemStore) ExpireManagedRealtimeEntity(ctx context.Context, candidate ManagedRealtimeEntityExpiration) (ManagedRealtimeChannelMessage, error) {
	return m.appendManagedRealtimeChannel(ctx, candidate.EndpointID, candidate.Channel, expirationPayload(candidate), false, "", map[string]string{"event_type": "reducer.expired"}, nil, &candidate, nil)
}
func (s *PgStore) ExpireManagedRealtimeEntity(ctx context.Context, candidate ManagedRealtimeEntityExpiration) (ManagedRealtimeChannelMessage, error) {
	return s.appendManagedRealtimeChannel(ctx, candidate.EndpointID, candidate.Channel, expirationPayload(candidate), false, "", map[string]string{"event_type": "reducer.expired"}, nil, &candidate, nil)
}
