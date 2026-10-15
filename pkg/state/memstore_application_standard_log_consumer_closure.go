package state

import (
	"context"
	"time"
)

var _ ApplicationStandardLogConsumerClosureStore = (*MemStore)(nil)

func (m *MemStore) CloseApplicationStandardLogConsumer(ctx context.Context, s ApplicationStandardLogConsumerSession) (ApplicationStandardLogConsumerClosure, error) {
	if !validStandardLogSession(s) {
		return ApplicationStandardLogConsumerClosure{}, ErrInvalidArgument
	}
	s = canonicalStandardLogHealthSession(s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogConsumerClosure{}, err
	}
	if !m.standardLogSessionRegisteredLocked(s) {
		return ApplicationStandardLogConsumerClosure{}, ErrApplicationStandardLogConsumerFenced
	}
	if c, ok := m.applicationStandardLogConsumerClosures[s.NodeID]; ok {
		return c, nil
	}
	c := ApplicationStandardLogConsumerClosure{ApplicationStandardLogConsumerSession: s, StoppedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if m.applicationStandardLogConsumerClosures == nil {
		m.applicationStandardLogConsumerClosures = map[string]ApplicationStandardLogConsumerClosure{}
	}
	m.applicationStandardLogConsumerClosures[s.NodeID] = c
	return c, nil
}

func (m *MemStore) standardLogSessionCurrentLocked(s ApplicationStandardLogConsumerSession) bool {
	_, closed := m.applicationStandardLogConsumerClosures[canonicalStandardUUID(s.NodeID)]
	return !closed && m.standardLogSessionRegisteredLocked(s)
}
