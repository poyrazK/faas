package state

import (
	"bytes"
	"context"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type managedRealtimeInboxStreamState struct {
	next     int64
	oldest   int64
	messages []ManagedRealtimeChannelMessage
}

func (m *MemStore) loadManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, subscription, channel string, initialSequence int64) (int64, error) {
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
	cutoff := now.Add(-ManagedRealtimeInboxCursorRetention)
	for candidate, cursor := range m.managedRealtimeInboxCursors {
		if candidate.endpointID == endpointID && cursor.updatedAt.Before(cutoff) {
			delete(m.managedRealtimeInboxCursors, candidate)
		}
	}
	if cursor, ok := m.managedRealtimeInboxCursors[key]; ok {
		cursor.updatedAt = now
		m.managedRealtimeInboxCursors[key] = cursor
		return cursor.sequence, nil
	}
	latest := int64(0)
	if history := m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]; history != nil {
		latest = history.next - 1
	}
	if initialSequence > latest {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	// A baseline below the retention floor is stored; the following history
	// read returns the current gap bounds instead of silently skipping data.
	count, principalCount := 0, 0
	for candidate := range m.managedRealtimeInboxCursors {
		if candidate.endpointID == endpointID {
			count++
			if candidate.principal == principal {
				principalCount++
			}
		}
	}
	if count >= ManagedRealtimeInboxMaxConsumers || principalCount >= ManagedRealtimeInboxMaxConsumersPerPrincipal {
		return 0, ErrManagedRealtimeDurableCursorLimit
	}
	m.managedRealtimeInboxCursors[key] = managedRealtimeDurableCursorState{sequence: initialSequence, updatedAt: now}
	return initialSequence, nil
}

func (m *MemStore) advanceManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
	if err := validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel, sequence); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managedRealtimeDurableCursorKey{endpointID: endpointID, principal: principal, subscription: subscription, channel: channel}
	cursor, ok := m.managedRealtimeInboxCursors[key]
	if !ok {
		return 0, ErrNotFound
	}
	if sequence > cursor.sequence {
		m.enqueueRealtimeInboxWebhookLocked(key, AppWebhookEventRealtimeInboxAcknowledged, map[string]any{"previous_sequence": cursor.sequence, "sequence": sequence})
		for pending := range m.managedRealtimeInboxFallbacks {
			if pending.endpointID == endpointID && pending.principal == principal && pending.sequence > cursor.sequence && pending.sequence <= sequence {
				m.deleteNotificationFallbackLocked(pending)
			}
		}
		for id, job := range m.managedRealtimePushDeliveries {
			if job.EndpointID == endpointID && job.Principal == principal && job.Sequence > cursor.sequence && job.Sequence <= sequence {
				m.cancelPushLocked(id, job, "acknowledged")
			}
		}
		cursor.sequence = sequence
	}
	if stream := m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]; stream != nil {
		oldest := stream.oldest
		for _, message := range stream.messages {
			if message.CreatedAt.Before(time.Now().UTC().Add(-ManagedRealtimeInboxRetention)) && message.Sequence >= oldest {
				oldest = message.Sequence + 1
			}
		}
		if cursor.sequence >= oldest-1 {
			cursor.gapReported = false
		}
	}
	cursor.updatedAt = time.Now().UTC()
	m.managedRealtimeInboxCursors[key] = cursor
	return cursor.sequence, nil
}

func (m *MemStore) resetManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, subscription, channel string, sequence int64) (int64, error) {
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
	cursor, ok := m.managedRealtimeInboxCursors[key]
	if !ok {
		return 0, ErrNotFound
	}
	oldest, latest := int64(1), int64(0)
	if history := m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]; history != nil {
		oldest, latest = history.oldest, history.next-1
		cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxRetention)
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
	cursor.gapReported = false
	cursor.updatedAt = time.Now().UTC()
	m.managedRealtimeInboxCursors[key] = cursor
	return cursor.sequence, nil
}

func (m *MemStore) PruneExpiredManagedRealtimeInboxCursors(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeDurableCursorInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxCursorRetention)
	var removed int64
	for key, cursor := range m.managedRealtimeInboxCursors {
		if cursor.updatedAt.Before(cutoff) {
			delete(m.managedRealtimeInboxCursors, key)
			removed++
			if removed == int64(batch) {
				break
			}
		}
	}
	return removed, nil
}

func (m *MemStore) appendManagedRealtimeInboxMessage(_ context.Context, endpointID, channel string, data []byte, binary bool, idempotencyKey string, fallbackAfter int, categories ...string) (ManagedRealtimeChannelMessage, error) {
	category := "notifications"
	if len(categories) > 0 {
		category = categories[0]
	}
	groupKey, groupLabel := "", ""
	if len(categories) > 1 {
		groupKey = categories[1]
	}
	if len(categories) > 2 {
		groupLabel = categories[2]
	}
	notBeforeRaw := ""
	if len(categories) > 6 {
		notBeforeRaw = categories[6]
	}
	notBefore, scheduleErr := api.ParseRealtimeNotificationNotBefore(notBeforeRaw, time.Now().UTC())
	if scheduleErr != nil || notBeforeRaw != "" && fallbackAfter == 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if !notBefore.IsZero() {
		notBeforeRaw = notBefore.Format(time.RFC3339Nano)
	}
	collapseKey := ""
	if len(categories) > 5 {
		collapseKey = categories[5]
	}
	if api.ValidateRealtimeNotificationGroup(collapseKey, "") != nil || collapseKey != "" && fallbackAfter == 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	ttl := 0
	if len(categories) > 4 {
		var err error
		ttl, err = strconv.Atoi(categories[4])
		if err != nil || ttl < 0 || ttl > 259200 || ttl > 0 && (fallbackAfter == 0 || ttl <= fallbackAfter) {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
		}
	}
	priority := "normal"
	if len(categories) > 3 && categories[3] != "" {
		priority = categories[3]
	}
	if api.ValidateRealtimeNotificationPriority(priority) != nil {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if api.ValidateRealtimeNotificationGroup(groupKey, groupLabel) != nil {
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
	key := managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}
	state := m.managedRealtimeInboxStreams[key]
	if state == nil {
		channels := 0
		for existing := range m.managedRealtimeInboxStreams {
			if existing.endpointID == endpointID {
				channels++
			}
		}
		if channels >= ManagedRealtimeInboxMaxPrincipals {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryLimit
		}
		state = &managedRealtimeInboxStreamState{next: 1, oldest: 1}
	}
	state.trimExpired(time.Now().UTC().Add(-ManagedRealtimeInboxRetention))
	if idempotencyKey != "" {
		for _, existing := range state.messages {
			if existing.TargetMessageID == idempotencyKey {
				if existing.Version > 1 || existing.Deleted || existing.Binary != binary || !bytes.Equal(existing.Data, data) || existing.FallbackAfterSeconds != fallbackAfter || existing.NotificationCategory != category || existing.NotificationGroupKey != groupKey || existing.NotificationGroupLabel != groupLabel || existing.NotificationPriority != priority || existing.NotificationTTLSeconds != ttl || existing.NotificationCollapseKey != collapseKey || existing.NotificationNotBefore != notBeforeRaw {
					return ManagedRealtimeChannelMessage{}, ErrConflict
				}
				return cloneManagedRealtimeInboxMessage(existing), nil
			}
		}
	}
	if fallbackAfter > 0 {
		ep := m.managedRealtimeEndpoints[endpointID]
		available := false
		for _, hook := range m.appWebhooks {
			if hook.Scope == AppWebhookScopeApp && hook.AppID == ep.AppID && hook.AccountID == ep.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, AppWebhookEventRealtimeInboxFallbackRequired) {
				available = true
				break
			}
		}
		for key, provider := range m.managedRealtimePushProviders {
			if key.endpointID == endpointID && provider.Enabled {
				available = true
			}
		}
		if !available {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeFallbackSubscription
		}
		count := 0
		for candidate := range m.managedRealtimeInboxFallbacks {
			if candidate.endpointID == endpointID && candidate.principal == channel {
				count++
			}
		}
		if count >= ManagedRealtimeInboxMaxMessages {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryLimit
		}
	}
	if ttl > 0 && !notBefore.Before(time.Now().UTC().Add(time.Duration(ttl)*time.Second)) {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	message := ManagedRealtimeChannelMessage{
		TargetMessageID: idempotencyKey, Version: 1, MessageEvent: "created",
		EndpointID: endpointID, Channel: channel, Sequence: state.next,
		Data: append([]byte(nil), data...), Binary: binary,
		IdempotencyKey: idempotencyKey, FallbackAfterSeconds: fallbackAfter, NotificationCategory: category, NotificationGroupKey: groupKey, NotificationGroupLabel: groupLabel, NotificationPriority: priority, NotificationTTLSeconds: ttl, NotificationCollapseKey: collapseKey, NotificationNotBefore: notBeforeRaw, CreatedAt: time.Now().UTC(),
	}
	if fallbackAfter > 0 {
		m.setNotificationFallbackLocked(managedRealtimeFallbackKey{endpointID: endpointID, principal: channel, sequence: message.Sequence}, managedRealtimeFallback{notBefore: notBefore, collapseKey: collapseKey, ttlSeconds: ttl, expiresAt: message.CreatedAt.Add(time.Duration(ttl) * time.Second), priority: priority, category: category, groupKey: groupKey, groupLabel: groupLabel, messageID: idempotencyKey, deadline: message.CreatedAt.Add(time.Duration(fallbackAfter) * time.Second)})
	}
	m.managedRealtimeInboxStreams[key] = state
	state.messages = append(state.messages, message)
	state.next++
	if len(state.messages) > ManagedRealtimeInboxMaxMessages {
		state.messages[0] = ManagedRealtimeChannelMessage{}
		state.messages = state.messages[1:]
		state.oldest++
	}
	return cloneManagedRealtimeInboxMessage(message), nil
}

func (m *MemStore) readManagedRealtimeInbox(_ context.Context, endpointID, channel string, after int64, limit int) (ManagedRealtimeChannelHistory, error) {
	if err := validateManagedRealtimeHistoryRead(endpointID, channel, after, limit); err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return ManagedRealtimeChannelHistory{}, ErrNotFound
	}
	history := ManagedRealtimeChannelHistory{OldestSequence: 1}
	state := m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}]
	if state == nil {
		if after > 0 {
			return ManagedRealtimeChannelHistory{}, ErrManagedRealtimeHistoryInvalid
		}
		return history, nil
	}
	cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxRetention)
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
			history.Messages = append(history.Messages, cloneManagedRealtimeInboxMessage(message))
			if len(history.Messages) == limit {
				break
			}
		}
	}
	return history, nil
}

func (state *managedRealtimeInboxStreamState) trimExpired(cutoff time.Time) int64 {
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

func (m *MemStore) PruneExpiredManagedRealtimeInboxMessages(_ context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ManagedRealtimeInboxRetention)
	var removed int64
	processed := 0
	for _, state := range m.managedRealtimeInboxStreams {
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

func cloneManagedRealtimeInboxMessage(message ManagedRealtimeChannelMessage) ManagedRealtimeChannelMessage {
	message.Data = append([]byte(nil), message.Data...)
	return message
}

func (m *MemStore) GetManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	if err := validateManagedRealtimeDurableCursor(endpointID, key, consumer, key, 0); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cursor, ok := m.managedRealtimeInboxCursors[managedRealtimeDurableCursorKey{endpointID: endpointID, principal: key, subscription: consumer, channel: key}]
	if !ok || cursor.updatedAt.Before(time.Now().UTC().Add(-ManagedRealtimeInboxCursorRetention)) {
		return 0, ErrNotFound
	}
	return cursor.sequence, nil
}
