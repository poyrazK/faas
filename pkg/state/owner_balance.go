package state

// adr: 20261118160000

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// OwnerBalanceCandidate is a parked app the ownership balance may move off
// its owner: no instance resident, not woken since the idle cutoff.
type OwnerBalanceCandidate struct {
	ID           string
	RAMMB        int
	LastActiveAt time.Time
	// HasLiveDeployment is false for an app with nothing to wake; it moves
	// freely. Otherwise Snapshot is the live deployment's stored, non-stale
	// snapshot (ID, StorageKey and CPU identity set), which the destination
	// must be able to restore.
	HasLiveDeployment bool
	Snapshot          Snapshot
}

// OwnerBalanceCandidateReader lists balance candidates, coldest first.
type OwnerBalanceCandidateReader interface {
	// ListOwnerBalanceCandidates returns up to limit apps nodeID owns that
	// have no resident instance, were last active before idleBefore, were
	// last moved before idleBefore unless that move was a displacement
	// (ADR-20261121203000), and either have no live deployment or a live deployment
	// with a stored non-stale snapshot, least recently active first.
	ListOwnerBalanceCandidates(ctx context.Context, nodeID string, idleBefore time.Time, limit int) ([]OwnerBalanceCandidate, error)
}

// ownerBalanceCandidateColumns and ownerBalanceCandidateJoins select an
// OwnerBalanceCandidate for apps a; scanTargets reads the columns.
const ownerBalanceCandidateColumns = `a.id::text, a.ram_mb, coalesce(act.last_active, a.created_at),
		       ld.id is not null,
		       coalesce(sn.id::text, ''), coalesce(sn.storage_key, ''),
		       coalesce(sn.cpu_template, ''), coalesce(sn.cpu_model, '')`

const ownerBalanceCandidateJoins = `
		  left join lateral (
		        select max(greatest(i.started_at, coalesce(i.last_request_at, i.started_at),
		                            coalesce(i.parked_at, i.started_at))) as last_active
		          from instances i
		         where i.app_id = a.id) act on true
		  left join lateral (
		        select d.id from deployments d
		         where d.app_id = a.id and d.status = 'live'
		         order by d.created_at desc limit 1) ld on true
		  left join lateral (
		        select s.id, s.storage_key, s.cpu_template, s.cpu_model from snapshots s
		         where s.deployment_id = ld.id and not s.stale and s.storage_key <> ''
		         order by s.created_at desc limit 1) sn on true`

func (c *OwnerBalanceCandidate) scanTargets() []any {
	return []any{&c.ID, &c.RAMMB, &c.LastActiveAt, &c.HasLiveDeployment,
		&c.Snapshot.ID, &c.Snapshot.StorageKey, &c.Snapshot.CPUTemplate, &c.Snapshot.CPUModel}
}

func (s *PgStore) ListOwnerBalanceCandidates(ctx context.Context, nodeID string, idleBefore time.Time, limit int) ([]OwnerBalanceCandidate, error) {
	if nodeID == "" || limit <= 0 {
		return nil, nil
	}
	if _, err := uuid.Parse(nodeID); err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		select `+ownerBalanceCandidateColumns+`
		  from apps a `+ownerBalanceCandidateJoins+`
		 where a.node_id = $1::uuid
		   and a.status in ('active', 'evicted_cold')
		   and coalesce(act.last_active, a.created_at) < $2
		   and (a.reassigned_at is null or a.reassigned_at < $2 or a.owner_home_node_id is not null)
		   and (ld.id is null or sn.id is not null)
		   and not exists (select 1 from instances i where i.app_id = a.id and i.state = any($3::text[]))
		 order by coalesce(act.last_active, a.created_at), a.id
		 limit $4`, nodeID, idleBefore, appResidentStates, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list owner balance candidates: %w", err)
	}
	defer rows.Close()
	var out []OwnerBalanceCandidate
	for rows.Next() {
		var c OwnerBalanceCandidate
		if err := rows.Scan(c.scanTargets()...); err != nil {
			return nil, fmt.Errorf("state: scan owner balance candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list owner balance candidates: %w", err)
	}
	return out, nil
}

func (m *MemStore) ListOwnerBalanceCandidates(_ context.Context, nodeID string, idleBefore time.Time, limit int) ([]OwnerBalanceCandidate, error) {
	if nodeID == "" || limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OwnerBalanceCandidate
	for _, a := range m.apps {
		if a.NodeID != nodeID || (a.Status != AppActive && a.Status != AppEvictedCold) {
			continue
		}
		// A displacement (drain or dead node) is not a balance move: it does
		// not hold the app on the node it was pushed to (ADR-20261121203000).
		if _, displaced := m.appOwnerHome[a.ID]; !displaced && a.ReassignedAt != nil && !a.ReassignedAt.Before(idleBefore) {
			continue
		}
		c, ok := m.ownerBalanceCandidateLocked(a)
		if ok && c.LastActiveAt.Before(idleBefore) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastActiveAt.Equal(out[j].LastActiveAt) {
			return out[i].LastActiveAt.Before(out[j].LastActiveAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ownerBalanceCandidateLocked builds a's candidate; false when an instance is
// resident or its live deployment has no stored snapshot. Caller holds m.mu.
func (m *MemStore) ownerBalanceCandidateLocked(a App) (OwnerBalanceCandidate, bool) {
	c := OwnerBalanceCandidate{ID: a.ID, RAMMB: a.RAMMB}
	for _, ins := range m.instances {
		if ins.AppID != a.ID {
			continue
		}
		if State(ins.State).CountsForRAM() {
			return OwnerBalanceCandidate{}, false
		}
		for _, t := range []time.Time{ins.StartedAt, ins.LastRequestAt, ins.ParkedAt} {
			if t.After(c.LastActiveAt) {
				c.LastActiveAt = t
			}
		}
	}
	var live *Deployment
	for _, d := range m.deployments {
		if d.AppID == a.ID && d.Status == DeployLive && (live == nil || d.CreatedAt.After(live.CreatedAt)) {
			d := d
			live = &d
		}
	}
	if live == nil {
		return c, true
	}
	c.HasLiveDeployment = true
	for _, s := range m.snapshots {
		if s.DeploymentID == live.ID && !s.Stale && s.StorageKey != "" && (c.Snapshot.ID == "" || s.CreatedAt.After(c.Snapshot.CreatedAt)) {
			c.Snapshot = s
		}
	}
	return c, c.Snapshot.ID != ""
}
