package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type PostCaptureClass int

const (
	PostCapturePublicationPending PostCaptureClass = iota
	PostCaptureActivationPending
	PostCaptureDependencyHeld
	PostCaptureCompatibilityHeld
	PostCaptureHostingVerifying
	PostCaptureHostingRetry
	PostCaptureGateOverdue
	PostCaptureUnknown
)

func (c PostCaptureClass) String() string {
	switch c {
	case PostCapturePublicationPending:
		return "publication_pending"
	case PostCaptureActivationPending:
		return "activation_pending"
	case PostCaptureDependencyHeld:
		return "dependency_held"
	case PostCaptureCompatibilityHeld:
		return "compatibility_held"
	case PostCaptureHostingVerifying:
		return "hosting_verifying"
	case PostCaptureHostingRetry:
		return "hosting_retry"
	case PostCaptureGateOverdue:
		return "gate_overdue"
	default:
		return "unknown"
	}
}

// PostCaptureSnapshot is a shared fleet view, matching imaged's unscoped
// snapshot_written consumer. Never sum replicas. Times measure the known
// capture/publication/receipt boundary, not continuous residence in a class.
// Held classes describe retained gate evidence, not a new eligibility verdict.
type PostCaptureSnapshot [8]SnapshotPrimeCohort

type PostCaptureObservationStore interface {
	PostCaptureSnapshot(context.Context) (PostCaptureSnapshot, error)
}

var (
	_ PostCaptureObservationStore = (*MemStore)(nil)
	_ PostCaptureObservationStore = (*PgStore)(nil)
)

func (s *PgStore) PostCaptureSnapshot(ctx context.Context) (PostCaptureSnapshot, error) {
	rows, err := sqlc.New().OperatorPostCaptureSnapshot(ctx, s.pool, api.ObsSnapshotPrimeInstanceLookback)
	if err != nil {
		return PostCaptureSnapshot{}, fmt.Errorf("state: observe post-capture work: %w", err)
	}
	var out PostCaptureSnapshot
	for _, r := range rows {
		found := false
		for i := range out {
			if PostCaptureClass(i).String() != r.Class {
				continue
			}
			if r.OldestAt.Valid && r.OldestAt.InfinityModifier != pgtype.Finite {
				return PostCaptureSnapshot{}, fmt.Errorf("state: invalid post-capture time")
			}
			out[i] = SnapshotPrimeCohort{Pending: r.Pending, UnknownAge: r.UnknownAge, OldestAt: r.OldestAt.Time}
			found = true
		}
		if !found {
			return PostCaptureSnapshot{}, fmt.Errorf("state: invalid post-capture class")
		}
	}
	return out, nil
}

func (m *MemStore) PostCaptureSnapshot(ctx context.Context) (PostCaptureSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PostCaptureSnapshot{}, err
	}
	var out PostCaptureSnapshot
	now := time.Now().UTC()
	for _, d := range m.deployments {
		a, exists := m.apps[d.AppID]
		if !exists || a.Status == AppDeleted || d.Status != DeploySnapshotting || d.EnvironmentWorkloadHeld() {
			continue
		}
		var stage StageState
		err := json.Unmarshal(d.StageState, &stage)
		if stage.Current == StageReadiness {
			continue
		}
		class, at, include := m.postCaptureClassLocked(d, stage, err, now)
		if !include {
			continue
		}
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

func postCaptureTime(at, now time.Time) bool {
	return at.After(time.Unix(0, 0)) && !at.After(now)
}

func (m *MemStore) postCaptureClassLocked(d Deployment, stage StageState, stageErr error, now time.Time) (PostCaptureClass, time.Time, bool) {
	unknown := func() (PostCaptureClass, time.Time, bool) { return PostCaptureUnknown, time.Time{}, true }
	for _, s := range m.secrets {
		if s.AppID == d.AppID && s.Scope == normalizedDeploymentScope(d.Scope) && s.SecretClass == SecretClassEphemeral {
			return 0, time.Time{}, false
		}
	}
	if stageErr != nil || stage.Current != StageSnapshotPrepare || stage.CurrentStartedAt == nil || !postCaptureTime(*stage.CurrentStartedAt, now) || d.RootfsKey == "" {
		return unknown()
	}
	start := *stage.CurrentStartedAt
	var published time.Time
	for _, s := range m.snapshots {
		if s.DeploymentID == d.ID && !s.Stale && (s.Tier == SnapshotTierInit || s.Tier == "") && s.CreatedAt.After(published) {
			published = s.CreatedAt
		}
	}
	if published.After(now) {
		return unknown()
	}
	if !published.After(start) {
		return m.postCaptureParkedLocked(d, start, now)
	}
	if h, exists := m.mcpTaskPromotionHolds[d.ID]; exists && (!h.recovery.HeldAt.Before(start) || !h.ObservedAt.Before(start)) {
		if h.recovery.HeldAt.After(now) || h.ObservedAt.After(now) {
			return unknown()
		}
		if h.recovery.HeldAt.Before(start) {
			// A re-observation can renew a hold after rollback while retaining
			// the first-held time from the previous attempt. Its new age is unknown.
			return PostCaptureCompatibilityHeld, time.Time{}, true
		}
		return PostCaptureCompatibilityHeld, primeLater(published, h.recovery.HeldAt), true
	}
	if gate, exists := m.deploymentDependencyGates[d.ID]; exists && gate.StartedAt != nil && !gate.StartedAt.Before(start) {
		if !postCaptureTime(*gate.StartedAt, now) || gate.DeadlineAt == nil || !gate.DeadlineAt.After(*gate.StartedAt) {
			return unknown()
		}
		if gate.Status == "waiting" || gate.Status == "failed" {
			if gate.Status == "failed" {
				return PostCaptureGateOverdue, primeLater(published, *gate.StartedAt), true
			}
			if !gate.DeadlineAt.After(now) {
				return PostCaptureGateOverdue, primeLater(published, *gate.DeadlineAt), true
			}
			return PostCaptureDependencyHeld, primeLater(published, *gate.StartedAt), true
		}
	}
	if p := stage.HostingVerification; p != nil && (!p.StartedAt.After(time.Unix(0, 0)) || !p.StartedAt.Before(start)) {
		if !postCaptureTime(p.StartedAt, now) || !p.DeadlineAt.After(p.StartedAt) || p.Attempts <= 0 ||
			(p.CompletedAt != nil && (!postCaptureTime(*p.CompletedAt, now) || p.CompletedAt.Before(p.StartedAt) || p.LastErrorCode != "" || p.RetryNotBefore != nil)) {
			return unknown()
		}
		if p.CompletedAt == nil {
			if apihostingreceipt.IsVerificationRecoveryCode(p.LastErrorCode) {
				if p.RetryNotBefore != nil && (p.RetryNotBefore.Before(p.StartedAt) || p.RetryNotBefore.After(p.DeadlineAt)) {
					return unknown()
				}
				if !p.DeadlineAt.After(now) {
					return PostCaptureGateOverdue, primeLater(published, p.DeadlineAt), true
				}
				return PostCaptureHostingRetry, primeLater(published, p.StartedAt), true
			}
			if p.LastErrorCode != "" {
				return unknown()
			}
			return PostCaptureHostingVerifying, primeLater(published, p.StartedAt), true
		}
	}
	return PostCaptureActivationPending, published, true
}

func (m *MemStore) postCaptureParkedLocked(d Deployment, start, now time.Time) (PostCaptureClass, time.Time, bool) {
	var rows []Instance
	for _, i := range m.instances {
		if i.AppID == d.AppID {
			rows = append(rows, i)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartedAt.IsZero() != rows[j].StartedAt.IsZero() {
			return rows[i].StartedAt.IsZero()
		}
		return rows[i].StartedAt.After(rows[j].StartedAt)
	})
	if len(rows) > api.ObsSnapshotPrimeInstanceLookback {
		rows = rows[:api.ObsSnapshotPrimeInstanceLookback]
	}
	boundary, _ := m.environmentRuntimeChangedAtLocked(d.AppID, d.Scope)
	var oldest time.Time
	found, missing := false, false
	for _, i := range rows {
		if i.DeploymentID != d.ID || i.State != string(StateParked) || i.Mode == string(InstanceModeWorker) || i.Mode == string(InstanceModeJob) {
			continue
		}
		if i.StartedAt.IsZero() {
			return PostCaptureUnknown, time.Time{}, true
		}
		if !i.StartedAt.After(start) {
			continue
		}
		if i.StartedAt.After(now) || i.ParkedAt.After(now) || boundary.After(now) {
			return PostCaptureUnknown, time.Time{}, true
		}
		if !m.snapshotPrimeParkedFreshLocked(i, boundary, d.Scope) {
			continue
		}
		found = true
		if !postCaptureTime(i.ParkedAt, now) || i.ParkedAt.Before(i.StartedAt) {
			missing = true
		} else if oldest.IsZero() || i.ParkedAt.Before(oldest) {
			oldest = i.ParkedAt
		}
	}
	if missing {
		oldest = time.Time{}
	}
	return PostCapturePublicationPending, oldest, found
}
