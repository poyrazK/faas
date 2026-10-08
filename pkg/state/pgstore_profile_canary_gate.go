package state

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func pgProfileGateDecision(ctx context.Context, db sqlc.DBTX, accountID string, d Deployment, now time.Time) (api.ProfileCanaryGateDecision, error) {
	body, err := sqlc.New().ReadProfileGateContext(ctx, db, sqlc.ReadProfileGateContextParams{AccountID: accountID, AppID: d.AppID, DeploymentID: d.ID})
	if err != nil {
		return api.ProfileCanaryGateDecision{}, routePolicyReadError(err)
	}
	var facts struct {
		Service     bool                         `json:"service"`
		Policy      *api.ProfileDeploymentPolicy `json:"policy"`
		Signal      *api.CanaryProfileSignal     `json:"signal"`
		StableID    *string                      `json:"stable_id"`
		ActiveCount int                          `json:"active_count"`
		StableCount int                          `json:"stable_count"`
	}
	if err := json.Unmarshal(body, &facts); err != nil {
		return api.ProfileCanaryGateDecision{}, err
	}
	p := api.ProfileDeploymentPolicy{}
	stableID := ""
	if facts.Policy != nil {
		p = *facts.Policy
	}
	if facts.StableID != nil {
		stableID = *facts.StableID
	}
	if facts.ActiveCount != 2 {
		facts.StableCount = 0
	}
	out := decideProfileCanaryGate(d, p, facts.Signal, stableID, facts.StableCount, now)
	if facts.Service {
		out.AutoRollback = false
	}
	return out, nil
}

func (s *PgStore) ReadProfileCanaryGate(ctx context.Context, accountID, appID, deploymentID string) (api.ProfileCanaryGateDecision, error) {
	d, err := s.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return api.ProfileCanaryGateDecision{}, err
	}
	if d.AppID != appID {
		return api.ProfileCanaryGateDecision{}, ErrNotFound
	}
	return pgProfileGateDecision(ctx, s.pool, accountID, d, time.Now().UTC())
}

func pgAbortProfileGatedCanary(ctx context.Context, tx pgx.Tx, accountID string, d Deployment, params CanaryAdvanceParams, now, leaseUntil time.Time) (Deployment, int64, error) {
	if err := pgAuthorizeBindingRelease(ctx, tx); err != nil {
		return Deployment{}, 0, err
	}
	clock, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return Deployment{}, 0, err
	}
	now = clock.Time
	if !leaseUntil.After(now) {
		return Deployment{}, 0, ErrSafeReleaseLeaseUnavailable
	}
	if err := profileGateRollbackAudit(&params); err != nil {
		return Deployment{}, 0, err
	}
	q := sqlc.New()
	changed, err := q.RestoreProfileGateStableTraffic(ctx, tx, sqlc.RestoreProfileGateStableTrafficParams{DeploymentID: params.ProfileGateDecision.StableDeploymentID, AppID: d.AppID, Scope: normalizedDeploymentScope(d.Scope)})
	if err != nil {
		return Deployment{}, 0, err
	}
	if changed != 1 {
		return Deployment{}, 0, ErrCanaryStateInvalid
	}
	changed, err = q.AbortProfileGatedCanary(ctx, tx, sqlc.AbortProfileGatedCanaryParams{DeploymentID: d.ID, AppID: d.AppID, CanaryStep: int32(d.CanaryStep), CanaryStepStartedAt: profileCheckTime(*d.CanaryStepStartedAt), ObservedAt: profileCheckTime(now), Reason: params.ProfileGateDecision.Reason})
	if err != nil {
		return Deployment{}, 0, err
	}
	if changed != 1 {
		return Deployment{}, 0, ErrCanaryStateInvalid
	}
	auditID, err := q.AppendProfileGateRollbackAudit(ctx, tx, sqlc.AppendProfileGateRollbackAuditParams{DeploymentID: d.ID, AccountID: accountID, Actor: params.Audit.Actor, ObservedAt: profileCheckTime(now), Data: params.Audit.Data})
	if err != nil {
		return Deployment{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, 0, err
	}
	d.TrafficPercent = 0
	d.RolloutState = "aborted"
	d.RolloutAbortedAt = &now
	d.RolloutAbortedReason = params.ProfileGateDecision.Reason
	return d, auditID, nil
}
