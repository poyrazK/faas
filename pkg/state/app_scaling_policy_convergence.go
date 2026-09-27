package state

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AppScalingPolicyObservationFreshness bounds how long runtime status trusts
// a scheduler's last successful read of the app's desired scaling policy.
const AppScalingPolicyObservationFreshness = 90 * time.Second

// AppScalingPolicyObservationInterval keeps an idle scheduler's status fresh
// while remaining coarse relative to the one-second scale-decision loops.
const AppScalingPolicyObservationInterval = 30 * time.Second

// AppScalingPolicyStatus is the desired revision and the latest durable
// scheduler observation. Observed means the owning schedd loaded the current
// app row; it does not claim that a metric-driven target replica count has
// already been reached.
type AppScalingPolicyStatus struct {
	DesiredRevision  int64
	ObservedRevision int64
	SchedulerNodeID  string
	ObservedAt       time.Time
}

// AppScalingPolicyConvergenceStore is additive to Store so the runtime
// scheduler watermark remains a PostgreSQL-only capability.
type AppScalingPolicyConvergenceStore interface {
	LatestAppScalingPolicyRevision(context.Context, string) (int64, error)
	RecordAppScalingPolicyObserved(context.Context, string, string, int64) error
	GetAppScalingPolicyStatus(context.Context, string) (AppScalingPolicyStatus, error)
}

var _ AppScalingPolicyConvergenceStore = (*PgStore)(nil)

func (s *PgStore) LatestAppScalingPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: scaling policy has nil pool")
	}
	var revision int64
	if err := s.pool.QueryRow(ctx, `
		SELECT scaling_policy_revision
		FROM apps
		WHERE id = $1
	`, appID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("state: latest app scaling policy revision: %w", err)
	}
	return revision, nil
}

// RecordAppScalingPolicyObserved advances the scheduler watermark only when
// the revision it loaded is still current. A sharded schedd can acknowledge
// only apps assigned to itself; the empty owner preserves the single-box
// posture. Periodic calls refresh liveness and repair missed app_changed
// notifications.
func (s *PgStore) RecordAppScalingPolicyObserved(ctx context.Context, appID, schedulerNodeID string, revision int64) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: scaling policy has nil pool")
	}
	appID = strings.TrimSpace(appID)
	schedulerNodeID = strings.TrimSpace(schedulerNodeID)
	if appID == "" || revision <= 0 {
		return fmt.Errorf("state: invalid app scaling policy observation")
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO app_scaling_policy_scheduler_status
		    (app_id, scheduler_node_id, observed_revision, observed_at)
		SELECT a.id,
		       a.node_id,
		       a.scaling_policy_revision,
		       now()
		FROM apps a
		WHERE a.id = $1
		  AND a.scaling_policy_revision = $3
		  AND ($2 = '' OR a.node_id::text = $2)
		ON CONFLICT (app_id) DO UPDATE SET
		    scheduler_node_id = EXCLUDED.scheduler_node_id,
		    observed_revision = EXCLUDED.observed_revision,
		    observed_at = EXCLUDED.observed_at
		WHERE EXCLUDED.observed_revision >= app_scaling_policy_scheduler_status.observed_revision
	`, appID, schedulerNodeID, revision)
	if err != nil {
		return fmt.Errorf("state: record app scaling policy observation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("state: app scaling policy revision changed or app is not owned by scheduler")
	}
	return nil
}

func (s *PgStore) GetAppScalingPolicyStatus(ctx context.Context, appID string) (AppScalingPolicyStatus, error) {
	if s == nil || s.pool == nil {
		return AppScalingPolicyStatus{}, fmt.Errorf("state: scaling policy status has nil pool")
	}
	var status AppScalingPolicyStatus
	if err := s.pool.QueryRow(ctx, `
		SELECT a.scaling_policy_revision,
		       COALESCE(p.observed_revision, 0),
		       COALESCE(p.scheduler_node_id::text, ''),
		       COALESCE(p.observed_at, 'epoch'::timestamptz)
		FROM apps a
		LEFT JOIN app_scaling_policy_scheduler_status p ON p.app_id = a.id
		WHERE a.id = $1
	`, appID).Scan(&status.DesiredRevision, &status.ObservedRevision, &status.SchedulerNodeID, &status.ObservedAt); err != nil {
		return AppScalingPolicyStatus{}, fmt.Errorf("state: get app scaling policy status: %w", err)
	}
	return status, nil
}
