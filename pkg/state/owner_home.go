package state

// adr: 20261121203000 — an app displaced by a drain or a dead-node ownership
// recovery remembers its home node (apps.owner_home_node_id) so the control
// plane can return it once that node is active again.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OwnerHomeReturn is a parked app displaced off HomeNodeID onto NodeID at
// DisplacedAt. The embedded candidate carries the snapshot the home node must
// be able to restore.
type OwnerHomeReturn struct {
	OwnerBalanceCandidate
	NodeID      string
	HomeNodeID  string
	DisplacedAt time.Time
}

// OwnerHomeReader reads displaced apps' home nodes.
type OwnerHomeReader interface {
	// ListOwnerHomeReturns returns up to limit apps away from their home
	// node that were displaced at or after displacedAfter, have no resident
	// instance, and either have no live deployment or a live deployment with
	// a stored non-stale snapshot, longest displaced first.
	ListOwnerHomeReturns(ctx context.Context, displacedAfter time.Time, limit int) ([]OwnerHomeReturn, error)
	// AppOwnerHome is the app's home node ID, "" when it is not displaced.
	AppOwnerHome(ctx context.Context, appID string) (string, error)
}

func (s *PgStore) ListOwnerHomeReturns(ctx context.Context, displacedAfter time.Time, limit int) ([]OwnerHomeReturn, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select `+ownerBalanceCandidateColumns+`,
		       a.node_id::text, a.owner_home_node_id::text, a.reassigned_at
		  from apps a `+ownerBalanceCandidateJoins+`
		 where a.owner_home_node_id is not null
		   and a.node_id is not null
		   and a.node_id <> a.owner_home_node_id
		   and a.status in ('active', 'evicted_cold')
		   and a.reassigned_at >= $1
		   and (ld.id is null or sn.id is not null)
		   and not exists (select 1 from instances i where i.app_id = a.id and i.state = any($2::text[]))
		 order by a.reassigned_at, a.id
		 limit $3`, displacedAfter, appResidentStates, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list owner home returns: %w", err)
	}
	defer rows.Close()
	var out []OwnerHomeReturn
	for rows.Next() {
		var r OwnerHomeReturn
		if err := rows.Scan(append(r.scanTargets(), &r.NodeID, &r.HomeNodeID, &r.DisplacedAt)...); err != nil {
			return nil, fmt.Errorf("state: scan owner home return: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list owner home returns: %w", err)
	}
	return out, nil
}

func (s *PgStore) AppOwnerHome(ctx context.Context, appID string) (string, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return "", ErrInvalidArgument
	}
	var home string
	err := s.pool.QueryRow(ctx, `select coalesce(owner_home_node_id::text, '') from apps where id = $1`, appID).Scan(&home)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: app owner home: %w", err)
	}
	return home, nil
}

// recordDisplacementLocked mirrors ReassignOrphanedAppOwner's home rule: keep
// an older home across a chain of displacements, remember the source
// otherwise, and forget it once the app lands back home. Caller holds m.mu.
func (m *MemStore) recordDisplacementLocked(appID, fromNodeID, toNodeID string) {
	home, ok := m.appOwnerHome[appID]
	switch {
	case ok && home == toNodeID:
		delete(m.appOwnerHome, appID)
	case ok:
	default:
		if m.appOwnerHome == nil {
			m.appOwnerHome = map[string]string{}
		}
		m.appOwnerHome[appID] = fromNodeID
	}
}

func (m *MemStore) ListOwnerHomeReturns(_ context.Context, displacedAfter time.Time, limit int) ([]OwnerHomeReturn, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OwnerHomeReturn
	for appID, home := range m.appOwnerHome {
		a, ok := m.apps[appID]
		if !ok || a.NodeID == "" || a.NodeID == home || (a.Status != AppActive && a.Status != AppEvictedCold) {
			continue
		}
		if a.ReassignedAt == nil || a.ReassignedAt.Before(displacedAfter) {
			continue
		}
		c, ok := m.ownerBalanceCandidateLocked(a)
		if !ok {
			continue
		}
		out = append(out, OwnerHomeReturn{OwnerBalanceCandidate: c, NodeID: a.NodeID, HomeNodeID: home, DisplacedAt: *a.ReassignedAt})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DisplacedAt.Equal(out[j].DisplacedAt) {
			return out[i].DisplacedAt.Before(out[j].DisplacedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) AppOwnerHome(_ context.Context, appID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return "", ErrNotFound
	}
	return m.appOwnerHome[appID], nil
}
