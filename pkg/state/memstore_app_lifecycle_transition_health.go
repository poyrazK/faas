package state

import "context"

func (m *MemStore) AppLifecycleTransitionHealth(_ context.Context) (AppLifecycleTransitionHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var health AppLifecycleTransitionHealth
	for _, transition := range m.appParkTransitions {
		if transition.CompletedAt != nil || transition.SupersededAt != nil {
			continue
		}
		health.ParkPendingCount++
		if health.ParkOldestPendingAt == nil || transition.RequestedAt.Before(*health.ParkOldestPendingAt) {
			at := transition.RequestedAt
			health.ParkOldestPendingAt = &at
		}
	}
	for _, transition := range m.appWakeTransitions {
		if transition.CompletedAt != nil || transition.SupersededAt != nil {
			continue
		}
		health.WakePendingCount++
		if health.WakeOldestPendingAt == nil || transition.RequestedAt.Before(*health.WakeOldestPendingAt) {
			at := transition.RequestedAt
			health.WakeOldestPendingAt = &at
		}
	}
	return health, nil
}
