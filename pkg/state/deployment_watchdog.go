package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// StuckDeploymentReason says why the deployment watchdog may fail an
// in-flight deployment (ADR-20261024140100).
type StuckDeploymentReason string

const (
	// StuckDeploymentQueued: a build waited in the builder queue past the
	// queue bound.
	StuckDeploymentQueued StuckDeploymentReason = "queue"
	// StuckDeploymentStalled: no build is queued or running and the
	// deployment made no stage progress within the stall bound.
	StuckDeploymentStalled StuckDeploymentReason = "stall"
)

// StuckDeployment is an in-flight deployment that can no longer finish on
// its own. Since is when its build was queued (queue) or its last progress
// (stall).
type StuckDeployment struct {
	ID     string
	AppID  string
	Status DeploymentStatus
	Reason StuckDeploymentReason
	Since  time.Time
}

// DeploymentWatchdogStore finds and fails stuck deployments. Deployments in
// flight by design are never listed: a prepared rollback target, an MCP task
// promotion hold, a parked deployment, an environment workload runtime, or a
// dependency, build-dependency or hosting-verification gate. A running build
// stays with builderd's reaper.
type DeploymentWatchdogStore interface {
	ListStuckDeployments(ctx context.Context, queuedBefore, stalledBefore time.Time, limit int) ([]StuckDeployment, error)
	// FailStuckDeployment re-checks d under its row lock and, when it is
	// still stuck for the same reason, cancels its queued builds and fails
	// it with code and message. ok is false when it moved on meanwhile.
	FailStuckDeployment(ctx context.Context, d StuckDeployment, queuedBefore, stalledBefore time.Time, code, message string) (Deployment, bool, error)
}

var (
	_ DeploymentWatchdogStore = (*PgStore)(nil)
	_ DeploymentWatchdogStore = (*MemStore)(nil)
)

func listStuckDeploymentsDB(ctx context.Context, db sqlc.DBTX, onlyID string, queuedBefore, stalledBefore time.Time, limit int) ([]StuckDeployment, error) {
	rows, err := sqlc.New().ListStuckDeployments(ctx, db, sqlc.ListStuckDeploymentsParams{
		RowLimit:      int32(limit), //nolint:gosec // callers pass api.DeploymentWatchdogBatch or 1
		OnlyID:        onlyID,
		QueuedBefore:  pgtype.Timestamptz{Time: queuedBefore, Valid: true},
		StalledBefore: pgtype.Timestamptz{Time: stalledBefore, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("state: list stuck deployments: %w", err)
	}
	out := make([]StuckDeployment, 0, len(rows))
	for _, row := range rows {
		out = append(out, StuckDeployment{ID: uuidString(row.ID), AppID: uuidString(row.AppID),
			Status: DeploymentStatus(row.Status), Reason: StuckDeploymentReason(row.Reason), Since: row.Since.Time})
	}
	return out, nil
}

func (s *PgStore) ListStuckDeployments(ctx context.Context, queuedBefore, stalledBefore time.Time, limit int) ([]StuckDeployment, error) {
	return listStuckDeploymentsDB(ctx, s.pool, "", queuedBefore, stalledBefore, limit)
}

func (s *PgStore) FailStuckDeployment(ctx context.Context, d StuckDeployment, queuedBefore, stalledBefore time.Time, code, message string) (Deployment, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, false, fmt.Errorf("state: begin stuck deployment failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	q := sqlc.New()
	if _, err := q.LockDeploymentForWatchdog(ctx, tx, uuidToPgtype(d.ID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, false, nil
		}
		return Deployment{}, false, fmt.Errorf("state: lock stuck deployment: %w", err)
	}
	still, err := listStuckDeploymentsDB(ctx, tx, d.ID, queuedBefore, stalledBefore, 1)
	if err != nil {
		return Deployment{}, false, err
	}
	if len(still) != 1 || still[0].Reason != d.Reason {
		return Deployment{}, false, nil
	}
	if _, err := q.CancelQueuedBuildsForDeployment(ctx, tx, uuidToPgtype(d.ID)); err != nil {
		return Deployment{}, false, fmt.Errorf("state: cancel stuck deployment builds: %w", err)
	}
	failed, err := setDeploymentFailedTx(ctx, tx, d.ID, code, message)
	if err != nil {
		return Deployment{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, false, fmt.Errorf("state: commit stuck deployment failure: %w", err)
	}
	return failed, true, nil
}

// stuckDeploymentLocked mirrors ListStuckDeployments for one deployment.
func (m *MemStore) stuckDeploymentLocked(d Deployment, queuedBefore, stalledBefore time.Time) (StuckDeployment, bool) {
	switch d.Status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
	default:
		return StuckDeployment{}, false
	}
	if d.DeletedAt != nil || d.ParkedReason != "" || d.EnvironmentWorkloadRuntime != "" {
		return StuckDeployment{}, false
	}
	if _, prepared := m.rollbackPrepared[d.ID]; prepared {
		return StuckDeployment{}, false
	}
	if _, held := m.mcpTaskPromotionHolds[d.ID]; held {
		return StuckDeployment{}, false
	}
	var stage struct {
		CurrentStartedAt    *time.Time      `json:"current_started_at"`
		DependencyGate      json.RawMessage `json:"dependency_gate"`
		BuildDependencyGate json.RawMessage `json:"build_dependency_gate"`
		HostingVerification json.RawMessage `json:"hosting_verification"`
	}
	if len(d.StageState) > 0 {
		if err := json.Unmarshal(d.StageState, &stage); err != nil {
			return StuckDeployment{}, false
		}
	}
	if len(stage.DependencyGate) > 0 || len(stage.BuildDependencyGate) > 0 || len(stage.HostingVerification) > 0 {
		return StuckDeployment{}, false
	}
	var oldestQueued, lastFinished time.Time
	running := false
	for _, b := range m.builds {
		if b.DeploymentID != d.ID {
			continue
		}
		switch b.Status {
		case BuildQueued:
			if oldestQueued.IsZero() || b.EnqueuedAt.Before(oldestQueued) {
				oldestQueued = b.EnqueuedAt
			}
		case BuildRunning:
			running = true
		}
		if b.FinishedAt.After(lastFinished) {
			lastFinished = b.FinishedAt
		}
	}
	if !oldestQueued.IsZero() {
		if oldestQueued.Before(queuedBefore) {
			return StuckDeployment{ID: d.ID, AppID: d.AppID, Status: d.Status, Reason: StuckDeploymentQueued, Since: oldestQueued}, true
		}
		return StuckDeployment{}, false
	}
	if running {
		return StuckDeployment{}, false
	}
	progress := d.CreatedAt
	if stage.CurrentStartedAt != nil && stage.CurrentStartedAt.After(progress) {
		progress = *stage.CurrentStartedAt
	}
	if lastFinished.After(progress) {
		progress = lastFinished
	}
	if progress.Before(stalledBefore) {
		return StuckDeployment{ID: d.ID, AppID: d.AppID, Status: d.Status, Reason: StuckDeploymentStalled, Since: progress}, true
	}
	return StuckDeployment{}, false
}

func (m *MemStore) ListStuckDeployments(_ context.Context, queuedBefore, stalledBefore time.Time, limit int) ([]StuckDeployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StuckDeployment
	for _, d := range m.deployments {
		if stuck, ok := m.stuckDeploymentLocked(d, queuedBefore, stalledBefore); ok {
			out = append(out, stuck)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) FailStuckDeployment(_ context.Context, d StuckDeployment, queuedBefore, stalledBefore time.Time, code, message string) (Deployment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.deployments[d.ID]
	if !ok {
		return Deployment{}, false, nil
	}
	still, ok := m.stuckDeploymentLocked(current, queuedBefore, stalledBefore)
	if !ok || still.Reason != d.Reason {
		return Deployment{}, false, nil
	}
	now := time.Now().UTC()
	for id, b := range m.builds {
		if b.DeploymentID == d.ID && b.Status == BuildQueued {
			b.Status = BuildCancelled
			b.CancelledAt = &now
			m.builds[id] = b
		}
	}
	failed, err := m.setDeploymentFailedLocked(d.ID, code, message)
	if err != nil {
		return Deployment{}, false, err
	}
	return failed, true, nil
}
