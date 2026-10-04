package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) QueueAutomaticRouteCheck(ctx context.Context, accountID, appID, deploymentID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin route check queue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return routePolicyReadError(err)
	}
	if _, err := pgSavedRouteRequirements(ctx, tx, accountID, appID); err != nil {
		return err
	}
	if _, err := q.ReadRoutePolicyDeployment(ctx, tx, sqlc.ReadRoutePolicyDeploymentParams{AppID: appID, DeploymentID: deploymentID}); err != nil {
		return routePolicyReadError(err)
	}
	if err := q.QueueAutomaticRouteCheck(ctx, tx, sqlc.QueueAutomaticRouteCheckParams{AppID: appID, DeploymentID: deploymentID}); err != nil {
		return fmt.Errorf("queue automatic route check: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ClaimAutomaticRouteCheck(ctx context.Context, lease time.Duration) (AutomaticRouteCheckClaim, error) {
	if lease <= 0 || lease > api.RouteCheckClaimLease {
		return AutomaticRouteCheckClaim{}, ErrInvalidArgument
	}
	q := &sqlc.Queries{}
	body, err := q.ClaimAutomaticRouteCheck(ctx, s.pool, sqlc.ClaimAutomaticRouteCheckParams{LeaseToken: uuid.NewString(), LeaseMs: max(lease.Milliseconds(), 1), MaxAttempts: api.RouteCheckMaxAttempts})
	if err != nil {
		return AutomaticRouteCheckClaim{}, routePolicyReadError(err)
	}
	var claim AutomaticRouteCheckClaim
	err = json.Unmarshal(body, &claim)
	return claim, err
}

func (s *PgStore) CompleteAutomaticRouteCheck(ctx context.Context, claim AutomaticRouteCheckClaim, check api.RouteRequirementsCheck, captureSHA string, truncated bool) (bool, error) {
	body, err := encodeAutomaticRouteCheck(claim, check, captureSHA, truncated)
	if err != nil {
		return false, err
	}
	q := &sqlc.Queries{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin route check completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Parent locks precede the job lock and notification FK writes. Do not lock
	// rules here: rule updates already fence this claim through the queue trigger.
	accountBody, err := q.LockRouteCheckCompletionAccount(ctx, tx, claim.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock route check account: %w", err)
	}
	if _, err := q.LockRouteCheckCompletionApp(ctx, tx, sqlc.LockRouteCheckCompletionAppParams{AppID: claim.AppID, AccountID: claim.AccountID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock route check app: %w", err)
	}
	var account Account
	if err := json.Unmarshal(accountBody, &account); err != nil {
		return false, fmt.Errorf("decode route check account: %w", err)
	}
	return pgCompleteRouteCheckHistory(ctx, tx, claim, check, body, captureSHA, truncated, account)

}

func pgCompleteRouteCheckHistory(ctx context.Context, tx pgx.Tx, claim AutomaticRouteCheckClaim, check api.RouteRequirementsCheck, body []byte, captureSHA string, truncated bool, account Account) (bool, error) {
	q := &sqlc.Queries{}
	previous, err := q.LockRouteFindingBaseline(ctx, tx, sqlc.LockRouteFindingBaselineParams{AppID: claim.AppID, AccountID: claim.AccountID, DeploymentID: claim.DeploymentID, RequestID: claim.RequestID, LeaseToken: claim.LeaseToken})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock route finding baseline: %w", err)
	}
	var baseline api.RouteCheckFindingBaseline
	if err := json.Unmarshal(previous, &baseline); err != nil {
		return false, fmt.Errorf("decode route finding baseline: %w", err)
	}
	eligible := account.MayDeploy() && account.Plan.OpenAPIDocsPerDeployment() > 0
	at := time.Now().UTC().Truncate(time.Microsecond)
	entry, historyBody, known, err := buildRouteCheckHistory(claim, check, baseline, eligible, at)
	if err != nil {
		return false, err
	}
	changes, err := json.Marshal(entry.Changes)
	if err != nil {
		return false, fmt.Errorf("encode route finding changes: %w", err)
	}
	count, err := q.CompleteAutomaticRouteCheck(ctx, tx, sqlc.CompleteAutomaticRouteCheckParams{LatestCheck: body, LatestChanges: changes, FindingBaseline: known, CheckedAt: pgtype.Timestamptz{Time: at, Valid: true}, CaptureSha256: captureSHA, CaptureTruncated: truncated, AppID: claim.AppID, AccountID: claim.AccountID, DeploymentID: claim.DeploymentID, RequestID: claim.RequestID, LeaseToken: claim.LeaseToken, NotificationAllowed: eligible})
	if err != nil {
		return false, fmt.Errorf("complete automatic route check: %w", err)
	}
	if count != 1 {
		return false, nil
	}
	if err := q.InsertRouteCheckHistory(ctx, tx, sqlc.InsertRouteCheckHistoryParams{ID: entry.ID, DeploymentID: claim.DeploymentID, AppID: claim.AppID, AccountID: claim.AccountID, CheckedAt: pgtype.Timestamptz{Time: at, Valid: true}, EncodedBytes: int32(len(historyBody)), Entry: historyBody}); err != nil {
		return false, fmt.Errorf("retain route check history: %w", err)
	}
	if err := q.PruneRouteCheckHistory(ctx, tx, sqlc.PruneRouteCheckHistoryParams{DeploymentID: claim.DeploymentID, MaxEntries: api.RouteCheckHistoryMaxEntries, MaxBytes: api.RouteCheckHistoryMaxBytes}); err != nil {
		return false, fmt.Errorf("prune route check history: %w", err)
	}
	return true, tx.Commit(ctx)
}

func (s *PgStore) FailAutomaticRouteCheck(ctx context.Context, claim AutomaticRouteCheckClaim) (bool, error) {
	q := &sqlc.Queries{}
	count, err := q.FailAutomaticRouteCheck(ctx, s.pool, sqlc.FailAutomaticRouteCheckParams{RetryMs: routeCheckRetry(claim.Attempts).Milliseconds(), AppID: claim.AppID, AccountID: claim.AccountID, DeploymentID: claim.DeploymentID, RequestID: claim.RequestID, LeaseToken: claim.LeaseToken})
	return count == 1, err
}

func (s *PgStore) GetAutomaticRouteCheck(ctx context.Context, accountID, appID, deploymentID string, fingerprint RouteCheckFingerprinter) (api.AutomaticRouteCheck, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.AutomaticRouteCheck{}, fmt.Errorf("begin route check lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	saved, err := pgSavedRouteRequirements(ctx, tx, accountID, appID)
	if err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	if err := pgRoutePolicyContract(ctx, tx, &snapshot, deploymentID, false); err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	q := &sqlc.Queries{}
	body, err := q.ReadAutomaticRouteCheck(ctx, tx, sqlc.ReadAutomaticRouteCheckParams{AppID: appID, AccountID: accountID, DeploymentID: deploymentID})
	if err != nil {
		return api.AutomaticRouteCheck{}, routePolicyReadError(err)
	}
	var record automaticRouteCheckRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return api.AutomaticRouteCheck{}, fmt.Errorf("decode automatic route result: %w", err)
	}
	result, err := automaticRouteCheckView(record, snapshot, saved, fingerprint)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
