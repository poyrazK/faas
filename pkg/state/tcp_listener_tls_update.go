package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// TCPListenerTLSStore changes TLS intent and disables serving in one mutation.
// apid validates domain ownership; the edge independently rechecks it on traffic.
type TCPListenerTLSStore interface {
	SetTCPListenerTLS(context.Context, string, api.TCPListenerTLSConfig) (TCPListener, error)
}

func (s *PgStore) SetTCPListenerTLS(ctx context.Context, id string, policy api.TCPListenerTLSConfig) (TCPListener, error) {
	policy, err := policy.Normalize()
	if err != nil {
		return TCPListener{}, fmt.Errorf("%w: %w", ErrInvalidTCPListener, err)
	}
	listener, err := scanTCPListener(s.pool.QueryRow(ctx, `update app_tcp_listeners set tls_mode=$2, tls_hostname=$3, enabled=false, updated_at=now() where id=$1 returning `+tcpListenerColumns, id, policy.Mode, policy.Hostname))
	if errors.Is(err, pgx.ErrNoRows) {
		return TCPListener{}, ErrNotFound
	}
	if err != nil {
		return TCPListener{}, fmt.Errorf("state: update TCP listener TLS: %w", err)
	}
	return listener, nil
}

func (m *MemStore) SetTCPListenerTLS(_ context.Context, id string, policy api.TCPListenerTLSConfig) (TCPListener, error) {
	policy, err := policy.Normalize()
	if err != nil {
		return TCPListener{}, fmt.Errorf("%w: %w", ErrInvalidTCPListener, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	listener, ok := m.tcpListeners[id]
	if !ok {
		return TCPListener{}, ErrNotFound
	}
	listener.TLSMode, listener.TLSHostname = policy.Mode, policy.Hostname
	listener.Enabled = false
	listener.UpdatedAt = time.Now()
	m.tcpListeners[id] = listener
	return listener, nil
}
