package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TCPListenerTLSObservationStore separates edge publication from apid intent.
// Put rejects stale intent and non-increasing timestamps with ErrConflict.
type TCPListenerTLSObservationStore interface {
	PutTCPListenerTLSObservation(context.Context, TCPListenerTLSObservation) error
	ListTCPListenerTLSObservations(context.Context, string) ([]TCPListenerTLSObservation, error)
	PruneTCPListenerTLSObservations(context.Context, time.Time) (int64, error)
}

func (s *PgStore) PutTCPListenerTLSObservation(ctx context.Context, observation TCPListenerTLSObservation) error {
	if err := observation.Validate(); err != nil {
		return err
	}
	id, err := uuid.Parse(observation.ListenerID)
	if err != nil {
		return ErrInvalidTCPListenerTLSObservation
	}
	count, err := sqlc.New().PutTCPListenerTLSObservation(ctx, s.pool, sqlc.PutTCPListenerTLSObservationParams{
		ListenerID: pgtypeFromUUID(id), EdgeID: observation.EdgeID, Hostname: observation.Hostname,
		IntentUpdatedAt: pgtypeFromTime(observation.IntentUpdatedAt), ObservedAt: pgtypeFromTime(observation.ObservedAt),
		Ready: observation.Ready, NotAfter: pgtypeFromTime(observation.NotAfter),
	})
	if err != nil {
		return fmt.Errorf("state: publish TCP TLS observation: %w", err)
	}
	if count == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ListTCPListenerTLSObservations(ctx context.Context, listenerID string) ([]TCPListenerTLSObservation, error) {
	id, err := uuid.Parse(listenerID)
	if err != nil {
		return nil, ErrInvalidTCPListenerTLSObservation
	}
	rows, err := sqlc.New().ListTCPListenerTLSObservations(ctx, s.pool, pgtypeFromUUID(id))
	if err != nil {
		return nil, fmt.Errorf("state: list TCP TLS observations: %w", err)
	}
	out := make([]TCPListenerTLSObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, TCPListenerTLSObservation{
			ListenerID: pgUUIDString(row.ListenerID), EdgeID: row.EdgeID, Hostname: row.Hostname,
			IntentUpdatedAt: timeFromPgtype(row.IntentUpdatedAt), ObservedAt: timeFromPgtype(row.ObservedAt),
			Ready: row.Ready, NotAfter: timeFromPgtype(row.NotAfter),
		})
	}
	return out, nil
}

func (s *PgStore) PruneTCPListenerTLSObservations(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, ErrInvalidTCPListenerTLSObservation
	}
	count, err := sqlc.New().PruneTCPListenerTLSObservations(ctx, s.pool, pgtypeFromTime(before))
	if err != nil {
		return 0, fmt.Errorf("state: prune TCP TLS observations: %w", err)
	}
	return count, nil
}

func (m *MemStore) PutTCPListenerTLSObservation(ctx context.Context, observation TCPListenerTLSObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := observation.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	listener, ok := m.tcpListeners[observation.ListenerID]
	if !ok || !listener.Enabled || listener.TLSMode != api.TCPListenerTLSTerminate || listener.TLSHostname != observation.Hostname || !listener.UpdatedAt.Equal(observation.IntentUpdatedAt) {
		return ErrConflict
	}
	edges := m.tcpTLSObservations[observation.ListenerID]
	if prior, ok := edges[observation.EdgeID]; ok && !observation.ObservedAt.After(prior.ObservedAt) {
		return ErrConflict
	}
	if edges == nil {
		edges = make(map[string]TCPListenerTLSObservation)
		m.tcpTLSObservations[observation.ListenerID] = edges
	}
	edges[observation.EdgeID] = observation
	return nil
}

func (m *MemStore) ListTCPListenerTLSObservations(ctx context.Context, listenerID string) ([]TCPListenerTLSObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TCPListenerTLSObservation, 0, len(m.tcpTLSObservations[listenerID]))
	for _, observation := range m.tcpTLSObservations[listenerID] {
		out = append(out, observation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out, nil
}

func (m *MemStore) PruneTCPListenerTLSObservations(ctx context.Context, before time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, ErrInvalidTCPListenerTLSObservation
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for listenerID, edges := range m.tcpTLSObservations {
		for edgeID, observation := range edges {
			if !observation.ObservedAt.After(before) {
				delete(edges, edgeID)
				count++
			}
		}
		if len(edges) == 0 {
			delete(m.tcpTLSObservations, listenerID)
		}
	}
	return count, nil
}
