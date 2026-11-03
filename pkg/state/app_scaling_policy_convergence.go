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

// AppScalingPolicyObservationRefreshAge is how old a scheduler's observation
// of an unchanged revision may grow before the periodic pass writes it again
// (ADR 20261103094355). The pass runs every
// AppScalingPolicyObservationInterval, so a row is rewritten about every
// 60 s, inside AppScalingPolicyObservationFreshness; it used to be rewritten
// for every app on every pass.
const AppScalingPolicyObservationRefreshAge = 45 * time.Second

// AppScalingPolicyObservation is one app revision a scheduler loaded.
type AppScalingPolicyObservation struct {
	AppID    string
	Revision int64
}

// AppScalingPolicyObservationBatchStore records a scheduler's periodic
// observations in one statement (ADR 20261103094355). The periodic pass ran
// one statement per app on schedd's main loop, which held the one-second
// scale-up, targets and floor triggers for a round trip per app.
type AppScalingPolicyObservationBatchStore interface {
	RecordAppScalingPolicyObservations(ctx context.Context, schedulerNodeID string, observations []AppScalingPolicyObservation) (int64, error)
}

var _ AppScalingPolicyObservationBatchStore = (*PgStore)(nil)

// RecordAppScalingPolicyObservations records each observation whose revision
// is still the app's, for apps owned by schedulerNodeID ("" for every app),
// as RecordAppScalingPolicyObserved does. A row already at that revision for
// that scheduler is rewritten only once it is
// AppScalingPolicyObservationRefreshAge old by the database clock, which
// stamped it. It returns how many rows it wrote.
func (s *PgStore) RecordAppScalingPolicyObservations(ctx context.Context, schedulerNodeID string, observations []AppScalingPolicyObservation) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: scaling policy has nil pool")
	}
	ids := make([]string, 0, len(observations))
	revisions := make([]int64, 0, len(observations))
	for _, o := range observations {
		if id := strings.TrimSpace(o.AppID); validUUID(id) && o.Revision > 0 {
			ids = append(ids, id)
			revisions = append(revisions, o.Revision)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO app_scaling_policy_scheduler_status AS p
		    (app_id, scheduler_node_id, observed_revision, observed_at)
		SELECT a.id, a.node_id, a.scaling_policy_revision, now()
		FROM unnest($2::uuid[], $3::bigint[]) AS o(app_id, revision)
		JOIN apps a ON a.id = o.app_id AND a.scaling_policy_revision = o.revision
		WHERE ($1 = '' OR a.node_id::text = $1)
		ON CONFLICT (app_id) DO UPDATE SET
		    scheduler_node_id = EXCLUDED.scheduler_node_id,
		    observed_revision = EXCLUDED.observed_revision,
		    observed_at = EXCLUDED.observed_at
		WHERE EXCLUDED.observed_revision > p.observed_revision
		   OR (EXCLUDED.observed_revision = p.observed_revision
		       AND (p.scheduler_node_id IS DISTINCT FROM EXCLUDED.scheduler_node_id
		            OR p.observed_at <= now() - make_interval(secs => $4::double precision)))
	`, strings.TrimSpace(schedulerNodeID), ids, revisions, AppScalingPolicyObservationRefreshAge.Seconds())
	if err != nil {
		return 0, fmt.Errorf("state: record app scaling policy observations: %w", err)
	}
	return tag.RowsAffected(), nil
}
