// adr: 20261110210000
package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// AppOwnerReleaseRequest is a node's request that the control plane move a
// parked app it owns elsewhere (ADR-20261110210000 §4). Under control-plane
// owner authority a compute node cannot write apps.node_id; it can only ask
// to give an app away, and the control plane chooses where it goes.
type AppOwnerReleaseRequest struct {
	AppID       string
	FromNodeID  string
	Reason      string
	RequestedAt time.Time
}

// AppOwnerReleaseStore is implemented by stores that carry
// app_owner_release_requests.
type AppOwnerReleaseStore interface {
	// RequestAppOwnerRelease records or refreshes the open request for appID.
	RequestAppOwnerRelease(ctx context.Context, appID, fromNodeID string) error
	// ListAppOwnerReleaseRequests returns up to limit open requests, oldest
	// first.
	ListAppOwnerReleaseRequests(ctx context.Context, limit int) ([]AppOwnerReleaseRequest, error)
	// DeleteAppOwnerReleaseRequest removes appID's request if it is still the
	// one requested at requestedAt, so a newer request survives.
	DeleteAppOwnerReleaseRequest(ctx context.Context, appID string, requestedAt time.Time) error
}

const appOwnerReleaseReasonPressure = "pressure"

func (s *PgStore) RequestAppOwnerRelease(ctx context.Context, appID, fromNodeID string) error {
	if appID == "" || fromNodeID == "" {
		return fmt.Errorf("state: request app owner release: empty app or node")
	}
	_, err := s.pool.Exec(ctx, `
		insert into app_owner_release_requests (app_id, from_node_id, reason)
		values ($1, $2, 'pressure')
		on conflict (app_id) do update
		   set from_node_id = excluded.from_node_id, requested_at = now()`,
		appID, fromNodeID)
	if err != nil {
		return fmt.Errorf("state: request app owner release: %w", err)
	}
	return nil
}

func (s *PgStore) ListAppOwnerReleaseRequests(ctx context.Context, limit int) ([]AppOwnerReleaseRequest, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select app_id::text, from_node_id::text, reason, requested_at
		  from app_owner_release_requests
		 order by requested_at, app_id
		 limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list app owner release requests: %w", err)
	}
	defer rows.Close()
	var out []AppOwnerReleaseRequest
	for rows.Next() {
		var r AppOwnerReleaseRequest
		if err := rows.Scan(&r.AppID, &r.FromNodeID, &r.Reason, &r.RequestedAt); err != nil {
			return nil, fmt.Errorf("state: list app owner release requests: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list app owner release requests: %w", err)
	}
	return out, nil
}

func (s *PgStore) DeleteAppOwnerReleaseRequest(ctx context.Context, appID string, requestedAt time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`delete from app_owner_release_requests where app_id = $1 and requested_at = $2`,
		appID, requestedAt); err != nil {
		return fmt.Errorf("state: delete app owner release request: %w", err)
	}
	return nil
}

func (m *MemStore) RequestAppOwnerRelease(_ context.Context, appID, fromNodeID string) error {
	if appID == "" || fromNodeID == "" {
		return fmt.Errorf("state: request app owner release: empty app or node")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return ErrNotFound
	}
	if m.ownerReleaseRequests == nil {
		m.ownerReleaseRequests = map[string]AppOwnerReleaseRequest{}
	}
	requested := time.Now()
	if prev, ok := m.ownerReleaseRequests[appID]; ok && !requested.After(prev.RequestedAt) {
		requested = prev.RequestedAt.Add(time.Microsecond)
	}
	m.ownerReleaseRequests[appID] = AppOwnerReleaseRequest{
		AppID: appID, FromNodeID: fromNodeID, Reason: appOwnerReleaseReasonPressure, RequestedAt: requested,
	}
	return nil
}

func (m *MemStore) ListAppOwnerReleaseRequests(_ context.Context, limit int) ([]AppOwnerReleaseRequest, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppOwnerReleaseRequest, 0, len(m.ownerReleaseRequests))
	for id, r := range m.ownerReleaseRequests {
		if _, ok := m.apps[id]; !ok { // ON DELETE CASCADE
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RequestedAt.Equal(out[j].RequestedAt) {
			return out[i].RequestedAt.Before(out[j].RequestedAt)
		}
		return out[i].AppID < out[j].AppID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) DeleteAppOwnerReleaseRequest(_ context.Context, appID string, requestedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.ownerReleaseRequests[appID]; ok && r.RequestedAt.Equal(requestedAt) {
		delete(m.ownerReleaseRequests, appID)
	}
	return nil
}
