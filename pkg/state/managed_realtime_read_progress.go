package state

import (
	"context"
	"time"
)

const ManagedRealtimeMaxReadMarkers = 1024

type ManagedRealtimeReadProgress struct {
	Sequence           int64     `json:"sequence"`
	Unread             int64     `json:"unread"`
	OldestSequence     int64     `json:"oldest_sequence"`
	LatestSequence     int64     `json:"latest_sequence"`
	HistoryUnavailable bool      `json:"history_unavailable"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ManagedRealtimeReadProgressStore interface {
	GetManagedRealtimeReadProgress(context.Context, string, string, string, bool) (ManagedRealtimeReadProgress, error)
	AdvanceManagedRealtimeReadProgress(context.Context, string, string, string, bool, int64) (ManagedRealtimeReadProgress, error)
}

type managedRealtimeReadKey struct {
	endpointID, principal, stream string
	inbox                         bool
}
type managedRealtimeReadMarker struct {
	sequence int64
	updated  time.Time
}

func ManagedRealtimeReadPrincipalKey(principal string) (string, error) {
	return managedRealtimeInboxKey(principal)
}
func readProgressKey(ep, principal, channel string, inbox bool) (managedRealtimeReadKey, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return managedRealtimeReadKey{}, err
	}
	if inbox {
		channel = key
	}
	if validateManagedRealtimeHistoryRequest(ep, channel) != nil {
		return managedRealtimeReadKey{}, ErrManagedRealtimeHistoryInvalid
	}
	return managedRealtimeReadKey{endpointID: ep, principal: key, stream: channel, inbox: inbox}, nil
}

func (m *MemStore) readProgressLocked(key managedRealtimeReadKey) ManagedRealtimeReadProgress {
	marker := m.managedRealtimeReadProgress[key]
	result := ManagedRealtimeReadProgress{Sequence: marker.sequence, OldestSequence: 1, UpdatedAt: marker.updated}
	var rows []ManagedRealtimeChannelMessage
	streamKey := managedRealtimeHistoryKey{endpointID: key.endpointID, channel: key.stream}
	if key.inbox {
		if s := m.managedRealtimeInboxStreams[streamKey]; s != nil {
			rows = s.messages
			result.OldestSequence = s.oldest
			result.LatestSequence = s.next - 1
		}
	} else {
		if s := m.managedRealtimeHistory[streamKey]; s != nil {
			rows = s.messages
			result.OldestSequence = s.oldest
			result.LatestSequence = s.next - 1
		}
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
	for _, row := range rows {
		if row.CreatedAt.Before(cutoff) && row.Sequence >= result.OldestSequence {
			result.OldestSequence = row.Sequence + 1
		}
	}
	unique := map[string]bool{}
	for _, row := range rows {
		if !row.Deleted && row.Sequence >= result.OldestSequence && row.Sequence > marker.sequence {
			if row.TargetMessageID != "" {
				unique[row.TargetMessageID] = true
			} else {
				result.Unread++
			}
		}
	}
	result.Unread += int64(len(unique))
	result.HistoryUnavailable = result.Sequence < result.OldestSequence-1
	return result
}
func (m *MemStore) GetManagedRealtimeReadProgress(ctx context.Context, ep, principal, channel string, inbox bool) (ManagedRealtimeReadProgress, error) {
	key, err := readProgressKey(ep, principal, channel, inbox)
	if err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	if err = ctx.Err(); err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return ManagedRealtimeReadProgress{}, ErrNotFound
	}
	return m.readProgressLocked(key), nil
}
func (m *MemStore) AdvanceManagedRealtimeReadProgress(ctx context.Context, ep, principal, channel string, inbox bool, sequence int64) (ManagedRealtimeReadProgress, error) {
	key, err := readProgressKey(ep, principal, channel, inbox)
	if err != nil || sequence < 0 {
		return ManagedRealtimeReadProgress{}, ErrManagedRealtimeHistoryInvalid
	}
	if err = ctx.Err(); err != nil {
		return ManagedRealtimeReadProgress{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return ManagedRealtimeReadProgress{}, ErrNotFound
	}
	current := m.readProgressLocked(key)
	if sequence > current.LatestSequence {
		return ManagedRealtimeReadProgress{}, ErrManagedRealtimeHistoryInvalid
	}
	if sequence <= current.Sequence {
		return current, nil
	}
	if _, exists := m.managedRealtimeReadProgress[key]; !exists {
		count := 0
		for existing := range m.managedRealtimeReadProgress {
			if existing.endpointID == ep {
				count++
			}
		}
		if count >= ManagedRealtimeMaxReadMarkers {
			return ManagedRealtimeReadProgress{}, ErrManagedRealtimeDurableCursorLimit
		}
	}
	m.managedRealtimeReadProgress[key] = managedRealtimeReadMarker{sequence: sequence, updated: time.Now().UTC()}
	result := m.readProgressLocked(key)
	channelName := key.stream
	if inbox {
		channelName = ""
	}
	m.enqueueRealtimeInboxWebhookLocked(managedRealtimeDurableCursorKey{endpointID: ep, principal: key.principal, channel: key.stream}, AppWebhookEventRealtimeMessageRead, map[string]any{"sequence": sequence, "previous_sequence": current.Sequence, "unread": result.Unread, "inbox": inbox, "channel": channelName})
	return result, nil
}

var _ ManagedRealtimeReadProgressStore = (*MemStore)(nil)
var _ ManagedRealtimeReadProgressStore = (*PgStore)(nil)
