package state

import (
	"bytes"
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
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

type managedRealtimeDurableCursorKey struct {
	endpointID   string
	principal    string
	subscription string
	channel      string
}

type managedRealtimeDurableCursorState struct {
	gapReported bool
	sequence    int64
	updatedAt   time.Time
}

func (m *MemStore) LoadManagedRealtimeDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, initialSequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, initialSequence); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return 0, ErrNotFound
	}
	key := managedRealtimeDurableCursorKey{endpointID: endpointID, principal: principal, subscription: subscription, channel: channel}
	now := time.Now().UTC()
	cutoff := now.Add(-ManagedRealtimeDurableCursorRetention)
	for candidate, cursor := range m.managedRealtimeDurableCursors {
		if candidate.endpointID == endpointID && cursor.updatedAt.Before(cutoff) {
			delete(m.managedRealtimeDurableCursors, candidate)
		}
	}
	if cursor, ok := m.managedRealtimeDurableCursors[key]; ok {
		cursor.updatedAt = now
		m.managedRealtimeDurableCursors[key] = cursor
		return cursor.sequence, nil
	}
	latest := int64(0)
	if history := m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]; history != nil {
		latest = history.next - 1
	}
	if initialSequence > latest {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	// A baseline below the retention floor is stored; the following history
	// read returns the current gap bounds instead of silently skipping data.
	count := 0
	for candidate := range m.managedRealtimeDurableCursors {
		if candidate.endpointID == endpointID {
			count++
		}
	}
	if count >= ManagedRealtimeDurableCursorMaxPerEndpoint {
		return 0, ErrManagedRealtimeDurableCursorLimit
	}
	m.managedRealtimeDurableCursors[key] = managedRealtimeDurableCursorState{sequence: initialSequence, updatedAt: now}
	return initialSequence, nil
}

func (m *MemStore) AdvanceManagedRealtimeDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, sequence); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managedRealtimeDurableCursorKey{endpointID: endpointID, principal: principal, subscription: subscription, channel: channel}
	cursor, ok := m.managedRealtimeDurableCursors[key]
	if !ok {
		return 0, ErrNotFound
	}
	if sequence > cursor.sequence {
		cursor.sequence = sequence
	}
	cursor.updatedAt = time.Now().UTC()
	m.managedRealtimeDurableCursors[key] = cursor
	return cursor.sequence, nil
}

func (m *MemStore) ResetManagedRealtimeDurableCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, sequence); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return 0, ErrNotFound
	}
	key := managedRealtimeDurableCursorKey{endpointID: endpointID, principal: principal, subscription: subscription, channel: channel}
	cursor, ok := m.managedRealtimeDurableCursors[key]
	if !ok {
		return 0, ErrNotFound
	}
	oldest, latest := int64(1), int64(0)
	if history := m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]; history != nil {
		oldest, latest = history.oldest, history.next-1
		cutoff := time.Now().UTC().Add(-ManagedRealtimeHistoryRetention)
		for _, message := range history.messages {
			if message.CreatedAt.Before(cutoff) && message.Sequence >= oldest {
				oldest = message.Sequence + 1
			}
		}
	}
	if sequence > latest {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	if sequence < oldest-1 {
		return 0, ErrManagedRealtimeDurableCursorExpired
	}
	cursor.sequence = sequence
	cursor.updatedAt = time.Now().UTC()
	m.managedRealtimeDurableCursors[key] = cursor
	return cursor.sequence, nil
}

func (m *MemStore) PruneExpiredManagedRealtimeDurableCursors(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeDurableCursorInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ManagedRealtimeDurableCursorRetention)
	var removed int64
	for key, cursor := range m.managedRealtimeDurableCursors {
		if cursor.updatedAt.Before(cutoff) {
			delete(m.managedRealtimeDurableCursors, key)
			removed++
			if removed == int64(batch) {
				break
			}
		}
	}
	return removed, nil
}

func (m *MemStore) ReadManagedRealtimeHistoryUsage(ctx context.Context, accountID string) (ManagedRealtimeHistoryUsage, error) {
	if accountID == "" {
		return ManagedRealtimeHistoryUsage{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeHistoryUsage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	usage := ManagedRealtimeHistoryUsage{ObservedAt: time.Now().UTC()}
	cutoff := usage.ObservedAt.Add(-ManagedRealtimeHistoryRetention)
	endpoints := make(map[string]struct{})
	for key, channel := range m.managedRealtimeHistory {
		endpoint, exists := m.managedRealtimeEndpoints[key.endpointID]
		if !exists || endpoint.AccountID != accountID {
			continue
		}
		endpoints[key.endpointID] = struct{}{}
		usage.ChannelCount++
		floor := channel.oldest
		for _, message := range channel.messages {
			if message.CreatedAt.Before(cutoff) && message.Sequence >= floor {
				floor = message.Sequence + 1
			}
		}
		for _, message := range channel.messages {
			usage.StoredMessageCount++
			usage.StoredPayloadBytes += int64(len(message.Data))
			if message.Sequence >= floor {
				usage.ReplayableMessageCount++
				usage.ReplayablePayloadBytes += int64(len(message.Data))
			}
		}
	}
	usage.EndpointCount = int64(len(endpoints))
	return usage, nil
}

func (m *MemStore) AppendManagedRealtimeChannelMessage(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string) (ManagedRealtimeChannelMessage, error) {
	return m.AppendManagedRealtimeChannelMetadata(ctx, endpointID, channel, data, binary, idempotencyKey, nil)
}
func (m *MemStore) AppendManagedRealtimeChannelMetadata(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, metadata map[string]string) (ManagedRealtimeChannelMessage, error) {
	return m.AppendManagedRealtimeChannelConditional(ctx, endpointID, channel, data, binary, idempotencyKey, metadata, nil)
}
func (m *MemStore) AppendManagedRealtimeChannelConditional(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, metadata map[string]string, expected *int64) (ManagedRealtimeChannelMessage, error) {
	return m.appendManagedRealtimeChannel(ctx, endpointID, channel, data, binary, idempotencyKey, metadata, expected, nil, nil)
}
func (m *MemStore) appendManagedRealtimeChannel(ctx context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, metadata map[string]string, expected *int64, expiration *ManagedRealtimeEntityExpiration, schedule *ManagedRealtimeSchedule) (ManagedRealtimeChannelMessage, error) {
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if expected != nil && *expected < 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if api.ValidateRealtimeMetadata(metadata) != nil {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := validateManagedRealtimeHistoryAppend(endpointID, channel, data, idempotencyKey); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return ManagedRealtimeChannelMessage{}, ErrNotFound
	}
	if schedule != nil {
		row, ok := m.managedRealtimeSchedules[scheduleKey(*schedule)]
		if !ok || !scheduleMatches(row, *schedule, time.Now().UTC()) {
			return ManagedRealtimeChannelMessage{}, ErrConflict
		}
		reducer, active := m.managedRealtimeReducers[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]
		var reducerState *ManagedRealtimeReducerState
		if active {
			reducerState = &reducer
		}
		if err := checkScheduleConditions(row, reducerState); err != nil {
			var failure *ManagedRealtimeScheduleConditionFailure
			if row.OnConditionFailure == "skip" && errors.As(err, &failure) {
				updated, eventRow := finishRealtimeScheduleOccurrence(row, 0, time.Now().UTC(), failure.Error())
				m.managedRealtimeSchedules[scheduleKey(row)] = updated
				m.recordRealtimeScheduleHistoryLocked(eventRow, "skipped")
				return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeScheduleSkipped
			}
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	if schedule != nil {
		endpoint := m.managedRealtimeEndpoints[endpointID]
		if endpoint.MaxMessageBytes > 0 && int64(len(data)) > endpoint.MaxMessageBytes {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
		}
	}
	key := managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}
	state := m.managedRealtimeHistory[key]
	if state == nil {
		if err := checkExpectedRealtimeSequence(expected, 0); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}
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
			if existing.TargetMessageID == idempotencyKey {
				if !equalRealtimeMetadata(existing.Metadata, metadata) || existing.Version > 1 || existing.Deleted || existing.Binary != binary || !bytes.Equal(existing.Data, data) {
					return ManagedRealtimeChannelMessage{}, ErrConflict
				}
				return cloneManagedRealtimeChannelMessage(existing), nil
			}
		}
	}
	if err := checkExpectedRealtimeSequence(expected, state.next-1); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if expiration != nil {
		row, ok := m.managedRealtimeReducers[key]
		if !ok || !reducerExpirationMatches(row, *expiration, time.Now().UTC()) {
			return ManagedRealtimeChannelMessage{}, ErrConflict
		}
	}
	if expiration == nil {
		if err := m.validateEventSchemaLocked(endpointID, channel, data, binary, metadata); err != nil {
			if state.next == 1 && len(state.messages) == 0 {
				delete(m.managedRealtimeHistory, key)
			}
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	message := ManagedRealtimeChannelMessage{
		Metadata: cloneRealtimeMetadata(metadata), TargetMessageID: idempotencyKey, Version: 1, MessageEvent: "created",
		EndpointID: endpointID, Channel: channel, Sequence: state.next,
		Data: append([]byte(nil), data...), Binary: binary,
		IdempotencyKey: idempotencyKey, CreatedAt: time.Now().UTC(),
	}
	reducer, err := m.prepareReducerLocked(endpointID, channel, []ManagedRealtimeChannelMessage{message})
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	state.messages = append(state.messages, message)
	m.saveReducerLocked(reducer)
	if schedule != nil {
		row := m.managedRealtimeSchedules[scheduleKey(*schedule)]
		row, eventRow := completeRealtimeSchedule(row, message.Sequence, message.CreatedAt)
		m.managedRealtimeSchedules[scheduleKey(*schedule)] = row
		m.recordRealtimeScheduleHistoryLocked(eventRow, "published")
	}
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
	message.Metadata = cloneRealtimeMetadata(message.Metadata)
	return message
}
