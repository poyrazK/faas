package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteHealthRecoveryStore = (*PgStore)(nil)

// APID owns this operation. The lease/account/app/candidate/sibling lock order
// matches advancement, and the current policy and telemetry authorize recovery.
func (s *PgStore) RecoverCanaryRouteHealth(ctx context.Context, accountID, appID, deploymentID string, expectedStep int) (RouteHealthRecoveryResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RouteHealthRecoveryResult{}, fmt.Errorf("begin route health recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	lease, err := q.LockRouteHealthRecoveryLease(ctx, tx)
	if err != nil {
		return RouteHealthRecoveryResult{}, fmt.Errorf("%w: lock route health recovery lease: %w", ErrSafeReleaseLeaseUnavailable, err)
	}
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, true)
	if err != nil {
		return RouteHealthRecoveryResult{}, err
	}
	d, err := pgLockRouteHealthRecoveryCandidate(ctx, tx, appID, deploymentID)
	result := RouteHealthRecoveryResult{Deployment: d}
	if err != nil {
		return result, err
	}
	if err := validateRouteHealthRecoveryCandidate(d, expectedStep); err != nil {
		return result, err
	}
	if _, err := pgRouteHealthRecoveryClock(ctx, tx, lease.Time); err != nil {
		return result, err
	}
	gate, err := pgRouteHealthGate(ctx, tx, accountID, appID)
	if err != nil {
		return result, err
	}
	if gate.Mode != "enforce" || gate.OnRegression != "abort" || !snapshot.Account.MayDeploy() {
		return result, nil
	}
	if err := pgLockRouteHealthRecoveryPredecessor(ctx, tx, d); err != nil {
		return result, err
	}
	now, err := pgRouteHealthRecoveryClock(ctx, tx, lease.Time)
	if err != nil {
		return result, err
	}
	report, err := pgRouteHealthReport(ctx, tx, snapshot, d, now)
	if err != nil {
		return result, err
	}
	decision := routehealth.Decision(report)
	result.Decision = &decision
	if !routehealth.AbortEligible(report) {
		return result, nil
	}
	entry, record, err := buildRouteHealthAbortHistory(report, d)
	if err != nil {
		return result, err
	}
	entry, err = pgPersistRouteHealthHistory(ctx, tx, accountID, d, entry, record)
	if err != nil {
		return result, err
	}
	audit, err := routeHealthRecoveryAudit(accountID, entry)
	if err != nil {
		return result, err
	}
	updated, auditID, err := s.recoverRolloutTx(ctx, tx, appID, d.ID, report.StableDeploymentID, "abort", "critical route server error regression", nil, &audit)
	if err != nil {
		return result, err
	}
	if err := pgRouteHealthNotification(ctx, tx, snapshot, entry); err != nil {
		return result, err
	}
	payload, err := json.Marshal(map[string]any{"kind": "traffic", "app_id": appID, "deployment_id": d.ID, "traffic_percent": 0})
	if err != nil {
		return result, fmt.Errorf("encode route health recovery notification: %w", err)
	}
	if err := q.NotifyRouteHealthRecovery(ctx, tx, string(payload)); err != nil {
		return result, fmt.Errorf("notify route health recovery: %w", err)
	}
	if _, err := pgRouteHealthRecoveryClock(ctx, tx, lease.Time); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit route health recovery: %w", err)
	}
	return RouteHealthRecoveryResult{Deployment: updated, Aborted: true, Decision: &entry.Decision, AuditID: auditID}, nil
}

func pgRouteHealthRecoveryClock(ctx context.Context, tx pgx.Tx, expiresAt time.Time) (time.Time, error) {
	now, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return time.Time{}, fmt.Errorf("read route health recovery clock: %w", err)
	}
	if !expiresAt.After(now.Time) {
		return now.Time, ErrSafeReleaseLeaseUnavailable
	}
	return now.Time, nil
}

func pgLockRouteHealthRecoveryCandidate(ctx context.Context, tx pgx.Tx, appID, deploymentID string) (Deployment, error) {
	row, err := sqlc.New().LockRouteHealthRecoveryCandidate(ctx, tx, sqlc.LockRouteHealthRecoveryCandidateParams{AppID: appID, DeploymentID: deploymentID})
	if err != nil {
		return Deployment{}, routePolicyReadError(err)
	}
	d := Deployment{ID: row.ID, AppID: row.AppID, Status: DeploymentStatus(row.Status), CommitSHA: row.CommitSha.String,
		TrafficPercent: int(row.TrafficPercent), CanaryStep: int(row.CanaryStep), CanaryTotalSteps: int(row.CanaryTotalSteps), RolloutState: row.RolloutState, Scope: row.Scope, CreatedAt: row.CreatedAt.Time}
	if row.CanaryStepStartedAt.Valid {
		d.CanaryStepStartedAt = &row.CanaryStepStartedAt.Time
	}
	return d, nil
}

func pgLockRouteHealthRecoveryPredecessor(ctx context.Context, tx pgx.Tx, d Deployment) error {
	siblings, err := sqlc.New().LockRouteHealthRecoverySiblings(ctx, tx, sqlc.LockRouteHealthRecoverySiblingsParams{AppID: d.AppID, DeploymentID: d.ID, Scope: d.Scope})
	if err != nil {
		return fmt.Errorf("lock route health recovery predecessors: %w", err)
	}
	if len(siblings) != 1 || !siblings[0].CreatedAt.Time.Before(d.CreatedAt) {
		return ErrCanaryStateInvalid
	}
	stable := siblings[0]
	if stable.CanaryTotalSteps > 0 && (NormalizeRolloutState(stable.RolloutState) == "pending" || NormalizeRolloutState(stable.RolloutState) == "rolling_out") {
		return ErrCanaryStateInvalid
	}
	return nil
}
