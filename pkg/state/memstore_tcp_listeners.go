package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (m *MemStore) CreateTCPListener(_ context.Context, in TCPListener) (TCPListener, error) {
	in, err := normalizeTCPListener(in)
	if err != nil {
		return TCPListener{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted || app.AccountID != in.AccountID {
		return TCPListener{}, ErrNotFound
	}
	if m.tcpListeners == nil {
		m.tcpListeners = make(map[string]TCPListener)
	}
	for _, existing := range m.tcpListeners {
		if existing.ID == in.ID && in.ID != "" {
			return TCPListener{}, ErrConflict
		}
		if existing.AppID == in.AppID && existing.ListenerName == in.ListenerName {
			return TCPListener{}, ErrConflict
		}
		if existing.PublicPort == in.PublicPort {
			return TCPListener{}, ErrConflict
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	in.UpdatedAt = in.CreatedAt
	m.tcpListeners[in.ID] = in
	return in, nil
}

func (m *MemStore) TCPListenerByID(_ context.Context, id string) (TCPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listener, ok := m.tcpListeners[id]
	if !ok {
		return TCPListener{}, ErrNotFound
	}
	return listener, nil
}

func (m *MemStore) TCPListenerByAppAndName(_ context.Context, appID, listenerName string) (TCPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listenerName = strings.ToLower(strings.TrimSpace(listenerName))
	for _, listener := range m.tcpListeners {
		if listener.AppID == appID && listener.ListenerName == listenerName {
			return listener, nil
		}
	}
	return TCPListener{}, ErrNotFound
}

func (m *MemStore) TCPListenerByPublicPort(_ context.Context, publicPort int) (TCPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, listener := range m.tcpListeners {
		if listener.PublicPort == publicPort && listener.Enabled {
			return listener, nil
		}
	}
	return TCPListener{}, ErrNotFound
}

func (m *MemStore) ListTCPListenersForApp(_ context.Context, appID string) ([]TCPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listeners := make([]TCPListener, 0)
	for _, listener := range m.tcpListeners {
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

// ListEnabledTCPListeners implements the fleet-wide source used by tcpd's
// listener supervisor.
func (m *MemStore) ListEnabledTCPListeners(_ context.Context) ([]TCPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listeners := make([]TCPListener, 0)
	for _, listener := range m.tcpListeners {
		if listener.Enabled {
			listeners = append(listeners, listener)
		}
	}
	sort.Slice(listeners, func(i, j int) bool {
		return listeners[i].PublicPort < listeners[j].PublicPort
	})
	return listeners, nil
}

func (m *MemStore) SetTCPListenerEnabled(_ context.Context, id string, enabled bool) (TCPListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listener, ok := m.tcpListeners[id]
	if !ok {
		return TCPListener{}, ErrNotFound
	}
	listener.Enabled = enabled
	listener.UpdatedAt = time.Now()
	m.tcpListeners[id] = listener
	return listener, nil
}

func (m *MemStore) DeleteTCPListener(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tcpListeners[id]; !ok {
		return ErrNotFound
	}
	delete(m.tcpListeners, id)
	return nil
}
