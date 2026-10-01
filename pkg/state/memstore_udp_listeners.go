package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateUDPListener(_ context.Context, in UDPListener) (UDPListener, error) {
	in, err := normalizeUDPListener(in)
	if err != nil {
		return UDPListener{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted || app.AccountID != in.AccountID {
		return UDPListener{}, ErrNotFound
	}
	if m.udpListeners == nil {
		m.udpListeners = make(map[string]UDPListener)
	}
	count := 0
	for _, existing := range m.udpListeners {
		if existing.AppID == in.AppID {
			count++
		}
		if existing.ID == in.ID && in.ID != "" {
			return UDPListener{}, ErrConflict
		}
		if existing.AppID == in.AppID && existing.ListenerName == in.ListenerName {
			return UDPListener{}, ErrConflict
		}
		if existing.PublicPort == in.PublicPort {
			return UDPListener{}, ErrConflict
		}
	}
	if count >= api.UDPListenerReservationsPerAppMax {
		return UDPListener{}, &UDPListenerLimitError{Limit: api.UDPListenerReservationsPerAppMax, Observed: count + 1}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	in.UpdatedAt = in.CreatedAt
	m.udpListeners[in.ID] = in
	return in, nil
}

func (m *MemStore) UDPListenerByID(_ context.Context, id string) (UDPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listener, ok := m.udpListeners[id]
	if !ok {
		return UDPListener{}, ErrNotFound
	}
	return listener, nil
}

func (m *MemStore) UDPListenerByAppAndName(_ context.Context, appID, listenerName string) (UDPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listenerName = strings.ToLower(strings.TrimSpace(listenerName))
	for _, listener := range m.udpListeners {
		if listener.AppID == appID && listener.ListenerName == listenerName {
			return listener, nil
		}
	}
	return UDPListener{}, ErrNotFound
}

func (m *MemStore) UDPListenerByPublicPort(_ context.Context, publicPort int) (UDPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, listener := range m.udpListeners {
		app, exists := m.apps[listener.AppID]
		if listener.PublicPort == publicPort && listener.Enabled && exists && app.Status != AppDeleted && app.AccountID == listener.AccountID {
			return listener, nil
		}
	}
	return UDPListener{}, ErrNotFound
}

func (m *MemStore) ListUDPListenersForApp(_ context.Context, appID string) ([]UDPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listeners := make([]UDPListener, 0)
	for _, listener := range m.udpListeners {
		if listener.AppID == appID {
			listeners = append(listeners, listener)
		}
	}
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].CreatedAt.Equal(listeners[j].CreatedAt) {
			return listeners[i].ID > listeners[j].ID
		}
		return listeners[i].CreatedAt.After(listeners[j].CreatedAt)
	})
	return listeners, nil
}

// ListEnabledUDPListeners implements the fleet-wide source used by udpd's
// listener supervisor.
func (m *MemStore) ListEnabledUDPListeners(_ context.Context) ([]UDPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listeners := make([]UDPListener, 0)
	for _, listener := range m.udpListeners {
		app, exists := m.apps[listener.AppID]
		if listener.Enabled && exists && app.Status != AppDeleted && app.AccountID == listener.AccountID {
			listeners = append(listeners, listener)
		}
	}
	sort.Slice(listeners, func(i, j int) bool {
		return listeners[i].PublicPort < listeners[j].PublicPort
	})
	return listeners, nil
}

func (m *MemStore) SetUDPListenerEnabled(_ context.Context, id string, enabled bool) (UDPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listener, ok := m.udpListeners[id]
	if !ok {
		return UDPListener{}, ErrNotFound
	}
	listener.Enabled = enabled
	listener.UpdatedAt = time.Now()
	m.udpListeners[id] = listener
	return listener, nil
}

func (m *MemStore) DeleteUDPListener(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.udpListeners[id]; !ok {
		return ErrNotFound
	}
	delete(m.udpListeners, id)
	return nil
}
