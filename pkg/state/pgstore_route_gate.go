package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CanaryRouteGateStore = (*PgStore)(nil)

func pgCanaryRouteGate(ctx context.Context, db sqlc.DBTX, accountID, appID string) (api.CanaryRouteGate, error) {
	body, err := (&sqlc.Queries{}).ReadCanaryRouteGate(ctx, db, sqlc.ReadCanaryRouteGateParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return api.CanaryRouteGate{}, routePolicyReadError(err)
	}
	var gate api.CanaryRouteGate
	if err := json.Unmarshal(body, &gate); err != nil {
		return gate, fmt.Errorf("decode route gate: %w", err)
	}
	return gate, nil
}

func (s *PgStore) GetCanaryRouteGate(ctx context.Context, accountID, appID string) (api.CanaryRouteGate, error) {
	return pgCanaryRouteGate(ctx, s.pool, accountID, appID)
}

func (s *PgStore) SetCanaryRouteGate(ctx context.Context, accountID, appID string, request api.SetCanaryRouteGateRequest) (api.CanaryRouteGate, error) {
	if err := ValidateCanaryRouteGate(request); err != nil {
		return api.CanaryRouteGate{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.CanaryRouteGate{}, fmt.Errorf("begin route gate update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, true)
	if err != nil {
		return api.CanaryRouteGate{}, err
	}
	gate, err := pgCanaryRouteGate(ctx, tx, accountID, appID)
	if err != nil {
		return gate, err
	}
	if gate.Revision != *request.ExpectedRevision {
		return gate, ErrRouteGateRevision
	}
	if request.Mode == "enforce" {
		if !snapshot.Account.Plan.TrafficSplitAllowed() || snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0 {
			return gate, ErrRouteGatePlan
		}
		if _, err := pgSavedRouteRequirements(ctx, tx, accountID, appID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return gate, ErrRouteGateRequirements
			}
			return gate, err
		}
	}
	if gate.Mode == request.Mode {
		return gate, tx.Commit(ctx)
	}
	if gate.Revision >= api.RouteRequirementsMaxRevision {
		return gate, ErrRouteGateRevision
	}
	if err := (&sqlc.Queries{}).WriteCanaryRouteGate(ctx, tx, sqlc.WriteCanaryRouteGateParams{AppID: appID, AccountID: accountID, Mode: request.Mode, Revision: gate.Revision + 1}); err != nil {
		return gate, fmt.Errorf("write route gate: %w", err)
	}
	gate, err = pgCanaryRouteGate(ctx, tx, accountID, appID)
	if err != nil {
		return gate, err
	}
	return gate, tx.Commit(ctx)
}

// Match reviewed-policy lock order before any deployment locks. Existing rule
// rows and parent FK locks fence updates, deletes and new rules until commit.
func pgLockCanaryRouteSnapshot(ctx context.Context, tx pgx.Tx, deploymentID string) (RoutePolicySnapshot, error) {
	owner, err := (&sqlc.Queries{}).ReadCanaryRouteGateOwner(ctx, tx, deploymentID)
	if err != nil {
		return RoutePolicySnapshot{}, routePolicyReadError(err)
	}
	return pgRoutePolicySnapshot(ctx, tx, owner.AccountID, owner.AppID, true)
}

func pgCheckCanaryRouteGate(ctx context.Context, tx pgx.Tx, snapshot RoutePolicySnapshot, deployment Deployment, params CanaryAdvanceParams) error {
	gate, err := pgCanaryRouteGate(ctx, tx, snapshot.Account.ID, snapshot.App.ID)
	if err != nil {
		return err
	}
	if gate.Mode == "report" && params.RouteGateDecision == nil {
		return nil
	}
	saved, savedErr := pgSavedRouteRequirements(ctx, tx, snapshot.Account.ID, snapshot.App.ID)
	if savedErr != nil && !errors.Is(savedErr, ErrNotFound) {
		return savedErr
	}
	if err := pgRoutePolicyContract(ctx, tx, &snapshot, deployment.ID, true); err != nil {
		return err
	}
	q := &sqlc.Queries{}
	body, readErr := q.ReadAutomaticRouteCheck(ctx, tx, sqlc.ReadAutomaticRouteCheckParams{AppID: snapshot.App.ID, AccountID: snapshot.Account.ID, DeploymentID: deployment.ID})
	if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
		return fmt.Errorf("read canary route evidence: %w", readErr)
	}
	var result api.AutomaticRouteCheck
	unavailable := savedErr != nil || params.RouteCheckFingerprint == nil || snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0
	if readErr == nil && !unavailable {
		var record automaticRouteCheckRecord
		if err := json.Unmarshal(body, &record); err != nil {
			return fmt.Errorf("decode canary route evidence: %w", err)
		}
		result, err = automaticRouteCheckView(record, snapshot, saved, params.RouteCheckFingerprint)
		if err != nil {
			unavailable = true
		}
	}
	decision, refresh := decideCanaryRouteGate(gate, deployment.ID, result, readErr != nil, unavailable)
	if refresh && savedErr == nil && !unavailable {
		if err := q.QueueAutomaticRouteCheck(ctx, tx, sqlc.QueueAutomaticRouteCheckParams{AppID: snapshot.App.ID, DeploymentID: deployment.ID}); err != nil {
			return fmt.Errorf("queue canary route evidence: %w", err)
		}
		decision.CheckQueued = true
	}
	return requireCanaryRouteGate(gate, decision, params.RouteGateDecision)
}
