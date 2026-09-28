package state

import (
	"bytes"
	"context"
	"time"
)

type managedRealtimeHistoryKey struct {
	endpointID string
	channel    string
}

type managedRealtimeHistoryState struct {
	next     int64
	oldest   int64
	messages []ManagedRealtimeChannelMessage
}

func (m *MemStore) AppendManagedRealtimeChannelMessage(_ context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string) (ManagedRealtimeChannelMessage, error) {
	if err := validateManagedRealtimeHistoryAppend(endpointID, channel, data, idempotencyKey); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return ManagedRealtimeChannelMessage{}, ErrNotFound
	}
	key := managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}
	state := m.managedRealtimeHistory[key]
	if state == nil {
		channels := 0
		for existing := range m.managedRealtimeHistory {
			if existing.endpointID == endpointID {
				channels++
			}
		}
		if channels >= ManagedRealtimeHistoryMaxChannels {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryLimit
		}
		state = &managedRealtimeHistoryState{next: 1, oldest: 1}
		m.managedRealtimeHistory[key] = state
	}
	state.trimExpired(time.Now().UTC().Add(-ManagedRealtimeHistoryRetention))
	if idempotencyKey != "" {
		for _, existing := range state.messages {
			if existing.IdempotencyKey == idempotencyKey {
				if existing.Binary != binary || !bytes.Equal(existing.Data, data) {
					return ManagedRealtimeChannelMessage{}, ErrConflict
				}
				return cloneManagedRealtimeChannelMessage(existing), nil
			}
		}
	}
	message := ManagedRealtimeChannelMessage{
		EndpointID: endpointID, Channel: channel, Sequence: state.next,
		Data: append([]byte(nil), data...), Binary: binary,
		IdempotencyKey: idempotencyKey, CreatedAt: time.Now().UTC(),
	}
	state.messages = append(state.messages, message)
	state.next++
	if len(state.messages) > ManagedRealtimeHistoryMaxMessages {
		state.messages[0] = ManagedRealtimeChannelMessage{}
		state.messages = state.messages[1:]
		state.oldest++
	}
	return cloneManagedRealtimeChannelMessage(message), nil
}

func (m *MemStore) ReadManagedRealtimeChannelHistory(_ context.Context, endpointID, channel string, after int64, limit int) (ManagedRealtimeChannelHistory, error) {
	if err := validateManagedRealtimeHistoryRead(endpointID, channel, after, limit); err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return ManagedRealtimeChannelHistory{}, ErrNotFound
	}
	history := ManagedRealtimeChannelHistory{OldestSequence: 1}
	state := m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]
	if state == nil {
		if after > 0 {
			return ManagedRealtimeChannelHistory{}, ErrManagedRealtimeHistoryInvalid
		}
		return history, nil
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
	history.OldestSequence = state.oldest
	history.LatestSequence = state.next - 1
	for _, message := range state.messages {
		if message.CreatedAt.Before(cutoff) {
			history.OldestSequence = message.Sequence + 1
		}
	}
	if after > history.LatestSequence {
		return ManagedRealtimeChannelHistory{}, ErrManagedRealtimeHistoryInvalid
	}
	if after < history.OldestSequence-1 {
		history.HistoryUnavailable = true
		return history, nil
	}
	for _, message := range state.messages {
		if message.Sequence > after && message.Sequence >= history.OldestSequence {
			history.Messages = append(history.Messages, cloneManagedRealtimeChannelMessage(message))
			if len(history.Messages) == limit {
				break
			}
		}
	}
	return history, nil
}

func (state *managedRealtimeHistoryState) trimExpired(cutoff time.Time) int64 {
	// The highest expired sequence defines a prefix. Timestamps need not be
	// perfectly ordered under concurrent publishers or clock adjustments.
	removeCount := 0
	for i, message := range state.messages {
		if message.CreatedAt.Before(cutoff) {
			removeCount = i + 1
		}
	}
	for i := range removeCount {
		state.messages[i] = ManagedRealtimeChannelMessage{}
	}
	state.messages = state.messages[removeCount:]
	state.oldest += int64(removeCount)
	return int64(removeCount)
}

func (m *MemStore) PruneExpiredManagedRealtimeChannelMessages(_ context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
	var removed int64
	processed := 0
	for _, state := range m.managedRealtimeHistory {
		expired := false
		for _, message := range state.messages {
			if message.CreatedAt.Before(cutoff) {
				expired = true
				break
			}
		}
		if !expired {
			continue
		}
		removed += state.trimExpired(cutoff)
		processed++
		if processed == batch {
			break
		}
	}
	return removed, nil
}

func cloneManagedRealtimeChannelMessage(message ManagedRealtimeChannelMessage) ManagedRealtimeChannelMessage {
	message.Data = append([]byte(nil), message.Data...)
	return message
}
