package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type SnapshotPrimeClass int

const (
	SnapshotPrimeWaiting SnapshotPrimeClass = iota
	SnapshotPrimeReleaseBlocked
	SnapshotPrimeActive
	SnapshotPrimePublicationPending
	SnapshotPrimeRecorded
	SnapshotPrimeUnknown
)

func (c SnapshotPrimeClass) String() string {
	switch c {
	case SnapshotPrimeWaiting:
		return "waiting"
	case SnapshotPrimeReleaseBlocked:
		return "release_blocked"
	case SnapshotPrimeActive:
		return "active"
	case SnapshotPrimePublicationPending:
		return "publication_pending"
	case SnapshotPrimeRecorded:
		return "snapshot_recorded"
	default:
		return "unknown"
	}
}

type SnapshotPrimeCohort struct {
	Pending, UnknownAge int64
	OldestAt            time.Time
}

// SnapshotPrimeSnapshot covers all snapshot_prepare attempts in this owner
// scope, including work younger than recovery's grace and beyond its page cap.
// Age is the current attempt's age, bounded below by release completion,
// runtime invalidation and known instance activity; it is not continuous idle
// time. UnknownAge preserves missing evidence. Never sum overlapping scopes.
type SnapshotPrimeSnapshot [6]SnapshotPrimeCohort

type SnapshotPrimeObservationStore interface {
	SnapshotPrimeSnapshot(context.Context, string) (SnapshotPrimeSnapshot, error)
}

var (
	_ SnapshotPrimeObservationStore = (*PgStore)(nil)
	_ SnapshotPrimeObservationStore = (*MemStore)(nil)
)

func (s *PgStore) SnapshotPrimeSnapshot(ctx context.Context, node string) (SnapshotPrimeSnapshot, error) {
	rows, err := sqlc.New().OperatorSnapshotPrimeSnapshot(ctx, s.pool, sqlc.OperatorSnapshotPrimeSnapshotParams{NodeID: node, InstanceLookback: api.ObsSnapshotPrimeInstanceLookback})
	if err != nil {
		return SnapshotPrimeSnapshot{}, fmt.Errorf("state: observe snapshot prime handoff: %w", err)
	}
	var out SnapshotPrimeSnapshot
	for _, r := range rows {
		found := false
		for i := range out {
			if SnapshotPrimeClass(i).String() != r.Class {
				continue
			}
			if r.OldestAt.Valid && r.OldestAt.InfinityModifier != pgtype.Finite {
				return SnapshotPrimeSnapshot{}, fmt.Errorf("state: invalid prime handoff timing")
			}
			out[i] = SnapshotPrimeCohort{Pending: r.Pending, UnknownAge: r.UnknownAge}
			if r.OldestAt.Valid {
				out[i].OldestAt = r.OldestAt.Time
			}
			found = true
		}
		if !found {
			return SnapshotPrimeSnapshot{}, fmt.Errorf("state: invalid prime handoff class")
		}
	}
	return out, nil
}

func (m *MemStore) SnapshotPrimeSnapshot(ctx context.Context, node string) (SnapshotPrimeSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SnapshotPrimeSnapshot{}, err
	}
	var out SnapshotPrimeSnapshot
	now := time.Now().UTC()
	for _, d := range m.deployments {
		a, exists := m.apps[d.AppID]
		if !exists || a.Status == AppDeleted || d.Status != DeploySnapshotting || d.EnvironmentWorkloadHeld() ||
			(node != "" && a.NodeID != "" && a.NodeID != node) {
			continue
		}
		var stage StageState
		err := json.Unmarshal(d.StageState, &stage)
		if err == nil && stage.Current == StageReadiness {
			continue
		}
		class, at := m.snapshotPrimeClassLocked(d, stage, err, now)
		p := &out[class]
		p.Pending++
		if at.IsZero() {
			p.UnknownAge++
		} else if p.OldestAt.IsZero() || at.Before(p.OldestAt) {
			p.OldestAt = at
		}
	}
	return out, nil
}

func (m *MemStore) snapshotPrimeClassLocked(d Deployment, stage StageState, stageErr error, now time.Time) (SnapshotPrimeClass, time.Time) {
	if stageErr != nil || stage.Current != StageSnapshotPrepare || stage.CurrentStartedAt == nil ||
		!stage.CurrentStartedAt.After(time.Unix(0, 0)) || stage.CurrentStartedAt.After(now) || d.RootfsKey == "" {
		return SnapshotPrimeUnknown, time.Time{}
	}
	at := *stage.CurrentStartedAt
	allowed, releaseAt := m.snapshotPrimeReleaseLocked(d)
	if !allowed {
		return SnapshotPrimeReleaseBlocked, time.Time{}
	}
	unknown := len(d.ReleaseCommand) > 0 && (!releaseAt.After(time.Unix(0, 0)) || releaseAt.After(now))
	at = primeLater(at, releaseAt)
	boundary, _ := m.environmentRuntimeChangedAtLocked(d.AppID, d.Scope)
	at = primeLater(at, boundary)
	unknown = unknown || boundary.After(now)
	if snapshotAt := m.snapshotPrimeLatestLocked(d.ID); snapshotAt.After(now) {
		return SnapshotPrimeUnknown, time.Time{}
	} else if snapshotAt.After(*stage.CurrentStartedAt) {
		if unknown {
			at = time.Time{}
		}
		return SnapshotPrimeRecorded, at
	}
	active, parked, bad, activityUnknown, activityAt := m.snapshotPrimeInstancesLocked(d, *stage.CurrentStartedAt, boundary, now)
	if bad {
		return SnapshotPrimeUnknown, time.Time{}
	}
	at = primeLater(at, activityAt)
	if unknown || activityUnknown {
		at = time.Time{}
	}
	if active {
		return SnapshotPrimeActive, at
	}
	if parked {
		return SnapshotPrimePublicationPending, at
	}
	return SnapshotPrimeWaiting, at
}

func primeLater(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (m *MemStore) snapshotPrimeReleaseLocked(d Deployment) (bool, time.Time) {
	if len(d.ReleaseCommand) == 0 {
		return true, time.Time{}
	}
	for _, t := range m.appTasks {
		if t.DeploymentID != d.ID || t.Kind != AppTaskKindRelease {
			continue
		}
		valid := t.Status == AppTaskSucceeded && t.ArtifactKey == d.RootfsKey && t.ImageDigest == d.ImageDigest &&
			t.DeploymentScope == normalizedDeploymentScope(d.Scope) && ReleaseTaskRunsDeploymentRelease(t, d)
		if t.FinishedAt == nil {
			return valid, time.Time{}
		}
		return valid, *t.FinishedAt
	}
	return false, time.Time{}
}

func (m *MemStore) snapshotPrimeLatestLocked(id string) time.Time {
	var best Snapshot
	for _, s := range m.snapshots {
		if s.DeploymentID != id || s.Stale {
			continue
		}
		if best.ID == "" || (s.Tier == SnapshotTierWarm && best.Tier != SnapshotTierWarm) || ((s.Tier == SnapshotTierWarm) == (best.Tier == SnapshotTierWarm) && s.CreatedAt.After(best.CreatedAt)) {
			best = s
		}
	}
	return best.CreatedAt
}

func (m *MemStore) snapshotPrimeInstancesLocked(d Deployment, stage, boundary, now time.Time) (active, parked, bad, unknown bool, activity time.Time) {
	var instances []Instance
	for _, i := range m.instances {
		if i.AppID == d.AppID {
			instances = append(instances, i)
		}
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].StartedAt.IsZero() != instances[j].StartedAt.IsZero() {
			return instances[i].StartedAt.IsZero()
		}
		return instances[i].StartedAt.After(instances[j].StartedAt)
	})
	if len(instances) > api.ObsSnapshotPrimeInstanceLookback {
		instances = instances[:api.ObsSnapshotPrimeInstanceLookback]
	}
	for _, i := range instances {
		if i.DeploymentID == d.ID && i.StartedAt.IsZero() {
			bad = true
			continue
		}
		if i.DeploymentID != d.ID || !i.StartedAt.After(stage) {
			continue
		}
		bad = bad || i.StartedAt.After(now) || i.ParkedAt.After(now) || (i.TerminalAt != nil && i.TerminalAt.After(now))
		active = active || State(i.State).CountsForRAM()
		if i.State == string(StateParked) {
			parked = parked || m.snapshotPrimeParkedFreshLocked(i, boundary, d.Scope)
			unknown = unknown || i.ParkedAt.IsZero()
		} else if !State(i.State).CountsForRAM() {
			unknown = unknown || i.TerminalAt == nil
		}
		activity = primeLater(activity, i.ParkedAt)
		if i.TerminalAt != nil {
			activity = primeLater(activity, *i.TerminalAt)
		}
	}
	return
}

func (m *MemStore) snapshotPrimeParkedFreshLocked(i Instance, boundary time.Time, scope string) bool {
	inputs, exists := m.instanceRuntimeConfigInputsLocked(i.ID)
	if exists {
		return inputs.Scope == normalizedDeploymentScope(scope) && !inputs.Boundary.Before(boundary) && m.runtimeConfigInputsFreshLocked(i.AppID, inputs)
	}
	resolved := i.StartedAt
	if id, err := uuid.Parse(i.WakeID); err == nil && id.Version() == 7 {
		sec, nsec := id.Time().UnixTime()
		resolved = resolvedAtEarlier(resolved, time.Unix(sec, nsec))
	}
	return !m.runtimeConfigReceiptRequiredLocked(i.AppID, scope) && resolved.After(boundary)
}

func resolvedAtEarlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
