package state

import (
	"context"
	"time"
)

type ManagedRealtimeMutationStore interface {
	MutateManagedRealtimeMessage(context.Context, string, string, bool, string, int64, []byte, bool, bool) (ManagedRealtimeChannelMessage, error)
}

// stream is a channel, or the raw verified principal for inbox=true.
func mutationStream(stream string, inbox bool, id string, version int64, data []byte, remove bool) (string, error) {
	if version < 1 || validateManagedRealtimeInboxMessageID(id) != nil || len(data) > ManagedRealtimeHistoryMaxPayloadBytes || (remove && len(data) > 0) {
		return "", ErrManagedRealtimeHistoryInvalid
	}
	if inbox {
		return managedRealtimeInboxKey(stream)
	}
	if validateManagedRealtimeHistoryRead("endpoint", stream, 0, 1) != nil {
		return "", ErrManagedRealtimeHistoryInvalid
	}
	return stream, nil
}

func (m *MemStore) MutateManagedRealtimeMessage(ctx context.Context, ep, stream string, inbox bool, id string, version int64, data []byte, binary, remove bool) (ManagedRealtimeChannelMessage, error) {
	channel, err := mutationStream(stream, inbox, id, version, data, remove)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return ManagedRealtimeChannelMessage{}, ErrNotFound
	}
	key := managedRealtimeHistoryKey{endpointID: ep, channel: channel}
	var rows *[]ManagedRealtimeChannelMessage
	var next, oldest *int64
	maxRows := ManagedRealtimeHistoryMaxMessages
	if inbox {
		s := m.managedRealtimeInboxStreams[key]
		if s == nil {
			return ManagedRealtimeChannelMessage{}, ErrNotFound
		}
		rows, next, oldest = &s.messages, &s.next, &s.oldest
		maxRows = ManagedRealtimeInboxMaxMessages
	} else {
		s := m.managedRealtimeHistory[key]
		if s == nil {
			return ManagedRealtimeChannelMessage{}, ErrNotFound
		}
		rows, next, oldest = &s.messages, &s.next, &s.oldest
	}
	now := time.Now().UTC()
	floor := *oldest
	cutoff := now.Add(-ManagedRealtimeHistoryRetention)
	for _, msg := range *rows {
		if msg.CreatedAt.Before(cutoff) && msg.Sequence >= floor {
			floor = msg.Sequence + 1
		}
	}
	var current *ManagedRealtimeChannelMessage
	for i := range *rows {
		msg := &(*rows)[i]
		if msg.TargetMessageID == id && msg.Sequence >= floor {
			current = msg
		}
	}
	if current == nil {
		return ManagedRealtimeChannelMessage{}, ErrNotFound
	}
	if current.Version != version {
		return ManagedRealtimeChannelMessage{}, ErrConflict
	}
	if current.Deleted {
		if remove {
			return cloneManagedRealtimeChannelMessage(*current), nil
		}
		return ManagedRealtimeChannelMessage{}, ErrConflict
	}
	if !remove && !inbox {
		if err := m.validateEventSchemaLocked(ep, channel, data, binary, current.Metadata); err != nil {
			return ManagedRealtimeChannelMessage{}, err
		}
	}
	if !inbox {
		if _, ok := m.managedRealtimeReducers[key]; ok {
			return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeReducerActive
		}
	}
	event := "updated"
	if remove {
		event = "deleted"
		binary = false
		data = nil
	}
	updated := ManagedRealtimeChannelMessage{Metadata: cloneRealtimeMetadata(current.Metadata), EndpointID: ep, Channel: channel, Sequence: *next, Data: append([]byte(nil), data...), Binary: binary, CreatedAt: now, TargetMessageID: id, Version: version + 1, MessageEvent: event, Deleted: remove}
	for i := range *rows {
		msg := &(*rows)[i]
		if msg.TargetMessageID == id {
			msg.Data = append([]byte(nil), data...)
			msg.Binary = binary
			msg.Version = updated.Version
			msg.Deleted = remove
			msg.MessageEvent = event
		}
	}
	if remove && inbox {
		for jobID, job := range m.managedRealtimePushDeliveries {
			if job.EndpointID == ep && job.Principal == channel && job.MessageID == id {
				m.cancelPushLocked(jobID, job, "message_deleted")
			}
		}
		for key, pending := range m.managedRealtimeInboxFallbacks {
			if key.endpointID == ep && key.principal == channel && pending.messageID == id {
				m.deleteNotificationFallbackLocked(key)
			}
		}
	}
	*rows = append(*rows, updated)
	*next++
	newFloor := floor
	if limit := updated.Sequence - int64(maxRows) + 1; limit > newFloor {
		newFloor = limit
	}
	n := 0
	for n < len(*rows) && (*rows)[n].Sequence < newFloor {
		(*rows)[n] = ManagedRealtimeChannelMessage{}
		n++
	}
	*rows = (*rows)[n:]
	*oldest = newFloor
	return cloneManagedRealtimeChannelMessage(updated), nil
}

var _ ManagedRealtimeMutationStore = (*MemStore)(nil)
var _ ManagedRealtimeMutationStore = (*PgStore)(nil)
