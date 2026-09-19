package state

import (
	"context"
	"time"
)

func (m *MemStore) RecordTriggerConsumerHealth(_ context.Context, triggerID string, observation TriggerConsumerHealthObservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.triggerConsumerHealth == nil {
		m.triggerConsumerHealth = map[string]TriggerConsumerHealth{}
	}
	h := m.triggerConsumerHealth[triggerID]
	poll := observation.LastPollAt.UTC()
	h.LastPollAt = &poll
	if observation.Success {
		success := poll
		h.LastSuccessAt = &success
	} else {
		errAt := poll
		h.LastErrorAt = &errAt
		h.LastError = observation.Error
	}
	h.LagMessages = cloneHealthInt64(observation.LagMessages)
	h.LagAgeSeconds = cloneHealthFloat64(observation.LagAgeSeconds)
	m.triggerConsumerHealth[triggerID] = h
	return nil
}

func (m *MemStore) TriggerConsumerHealth(_ context.Context, triggerID string) (TriggerConsumerHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.triggerConsumerHealth[triggerID]
	if !ok {
		return TriggerConsumerHealth{}, ErrNotFound
	}
	return TriggerConsumerHealth{
		LastPollAt:    cloneHealthTime(h.LastPollAt),
		LastSuccessAt: cloneHealthTime(h.LastSuccessAt),
		LastErrorAt:   cloneHealthTime(h.LastErrorAt),
		LastError:     h.LastError,
		LagMessages:   cloneHealthInt64(h.LagMessages),
		LagAgeSeconds: cloneHealthFloat64(h.LagAgeSeconds),
	}, nil
}

func cloneHealthTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := *v
	return &t
}

func cloneHealthInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

func cloneHealthFloat64(v *float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}
