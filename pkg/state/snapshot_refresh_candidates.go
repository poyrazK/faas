// adr: 20261106214916

package state

import (
	"context"
	"sort"
	"time"
)

// SnapshotRefreshCandidate is an app whose most recent park, fleet-wide, was
// on the node a refresh pass is for: its newest snapshot was captured there,
// so a wake restores it there (ADR-20261106214916).
type SnapshotRefreshCandidate struct {
	AppID        string
	DeploymentID string
	ParkedAt     time.Time
	// StartedAt is when that parked instance started (zero if unrecorded).
	// It booted, or restored, at the guest vCPU count schedd chose then, so
	// the snapshot it captured or restored from has that count.
	StartedAt time.Time
	// VCPUs is the guest vCPU count that parked instance booked with
	// (instances.capacity_vcpu); the snapshot it captured or restored from
	// has that count. Zero when unrecorded.
	VCPUs int
	// Live is true when the app has an instance in a live state anywhere.
	// Such an app is recaptured when it parks; a pass skips it.
	Live bool
}

// SnapshotRefreshCandidateReader lists a node's snapshot refresh candidates.
type SnapshotRefreshCandidateReader interface {
	// ListSnapshotRefreshCandidates returns the apps whose latest park at or
	// after since was a normal wake on nodeID, most recent park first, at
	// most limit of them. Wake-history rows a migration lease owns are not
	// counted, as the parked-row retention sweep does not count them.
	ListSnapshotRefreshCandidates(ctx context.Context, nodeID string, since time.Time, limit int) ([]SnapshotRefreshCandidate, error)
}

// ListSnapshotRefreshCandidates reads instances_parked_at_retention_idx over
// the window; a release pass runs once per node per release.
func (s *PgStore) ListSnapshotRefreshCandidates(ctx context.Context, nodeID string, since time.Time, limit int) ([]SnapshotRefreshCandidate, error) {
	if !validUUID(nodeID) || limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`select p.app_id::text, p.deployment_id::text, p.parked_at, p.started_at, p.capacity_vcpu,
		        exists (select 1 from instances l where l.app_id = p.app_id and l.state = any($4::text[]))
		   from (select distinct on (app_id) app_id, deployment_id, node_id, parked_at, started_at, capacity_vcpu
		           from instances
		          where app_id is not null
		            and state = 'parked'
		            and lease_token is null
		            and migration_started_at is null
		            and parked_at >= $2
		            and kind = 'wake'
		            and mode = 'normal'
		            and deployment_id is not null
		          order by app_id, parked_at desc) p
		  where p.node_id = $1::uuid
		  order by p.parked_at desc
		  limit $3`, nodeID, since.UTC(), limit, LiveStates())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotRefreshCandidate
	for rows.Next() {
		var c SnapshotRefreshCandidate
		var startedAt *time.Time
		if err := rows.Scan(&c.AppID, &c.DeploymentID, &c.ParkedAt, &startedAt, &c.VCPUs, &c.Live); err != nil {
			return nil, err
		}
		if startedAt != nil {
			c.StartedAt = *startedAt
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListSnapshotRefreshCandidates mirrors PgStore.ListSnapshotRefreshCandidates.
func (m *MemStore) ListSnapshotRefreshCandidates(_ context.Context, nodeID string, since time.Time, limit int) ([]SnapshotRefreshCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	latest := map[string]Instance{}
	live := map[string]bool{}
	for _, ins := range m.instances {
		if ins.AppID == "" {
			continue
		}
		if IsLive(ins.State) {
			live[ins.AppID] = true
		}
		if ins.State != string(StateParked) || ins.LeaseToken != "" || ins.MigrationStartedAt != nil ||
			ins.ParkedAt.Before(since) || ins.DeploymentID == "" ||
			(ins.Kind != "" && ins.Kind != "wake") || (ins.Mode != "" && ins.Mode != string(InstanceModeNormal)) {
			continue
		}
		if prev, ok := latest[ins.AppID]; !ok || ins.ParkedAt.After(prev.ParkedAt) {
			latest[ins.AppID] = ins
		}
	}
	var out []SnapshotRefreshCandidate
	for appID, ins := range latest {
		if ins.NodeID == nodeID {
			out = append(out, SnapshotRefreshCandidate{AppID: appID, DeploymentID: ins.DeploymentID, ParkedAt: ins.ParkedAt, StartedAt: ins.StartedAt,
				VCPUs: m.instanceBookings[ins.ID].vcpu, Live: live[appID]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ParkedAt.Equal(out[j].ParkedAt) {
			return out[i].ParkedAt.After(out[j].ParkedAt)
		}
		return out[i].AppID < out[j].AppID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
