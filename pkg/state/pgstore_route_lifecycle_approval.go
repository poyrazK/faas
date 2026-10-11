package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteLifecycleApprovalStore = (*PgStore)(nil)

func (s *PgStore) ApproveRouteLifecycle(ctx context.Context, accountID, appID, actor string, r api.ApproveRouteLifecycleRequest, fingerprint RouteCheckFingerprinter, validator RouteLifecycleCompatibilityValidator) (api.RouteLifecycleApproval, error) {
	var a api.RouteLifecycleApproval
	if validateLifecycleApprovalRequest(r) != nil || actor == "" || fingerprint == nil || validator == nil {
		return a, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return a, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, true)
	if err != nil {
		return a, err
	}
	if snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0 || !snapshot.Account.Plan.TrafficSplitAllowed() || !snapshot.Account.MayDeploy() {
		return a, ErrRouteGatePlan
	}
	gate, err := pgCanaryRouteGate(ctx, tx, accountID, appID)
	if err != nil {
		return a, err
	}
	saved, err := pgSavedRouteRequirements(ctx, tx, accountID, appID)
	if err != nil {
		return a, err
	}
	removal, err := pgRouteRemovalPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return a, err
	}
	before := snapshot
	if err = pgRoutePolicyContract(ctx, tx, &before, r.BaselineDeploymentID, true); err != nil {
		return a, err
	}
	if err = pgRoutePolicyContract(ctx, tx, &snapshot, r.CandidateDeploymentID, true); err != nil {
		return a, err
	}
	// The shared app lock serializes traffic and captures while checking applicability.
	q := sqlc.New()
	scope, err := q.ReadLifecycleDeploymentScope(ctx, tx, sqlc.ReadLifecycleDeploymentScopeParams{DeploymentID: r.CandidateDeploymentID, AppID: appID})
	if err != nil {
		return a, err
	}
	baselineScope, err := q.ReadLifecycleDeploymentScope(ctx, tx, sqlc.ReadLifecycleDeploymentScopeParams{DeploymentID: r.BaselineDeploymentID, AppID: appID})
	if err != nil {
		return a, err
	}
	if !routeRemovalProductionScope(scope) || !routeRemovalProductionScope(baselineScope) {
		return a, &RouteLifecycleReviewBlockedError{"baseline_not_applicable_to_candidate"}
	}
	baselines, err := q.ReadLifecycleCanaryBaselines(ctx, tx, sqlc.ReadLifecycleCanaryBaselinesParams{AppID: appID, CandidateID: r.CandidateDeploymentID, Scope: scope})
	if err != nil {
		return a, err
	}
	applicable := false
	for _, id := range baselines {
		if id == r.BaselineDeploymentID {
			applicable = true
		}
	}
	if !applicable {
		return a, &RouteLifecycleReviewBlockedError{"baseline_not_applicable_to_candidate"}
	}
	clock, err := q.LifecycleApprovalClock(ctx, tx)
	if err != nil {
		return a, err
	}
	mappings, _, _ := normalizeLifecycleMappings(r.Mappings)
	inputs, _ := json.Marshal(api.RouteLifecycleApproval{CandidateDeploymentID: r.CandidateDeploymentID, Mappings: mappings})
	successorSnapshot, err := q.ReadLifecycleApprovalSuccessorBindings(ctx, tx, sqlc.ReadLifecycleApprovalSuccessorBindingsParams{AppID: appID, Receipt: inputs})
	if err != nil {
		return a, err
	}
	if err = pgLoadLifecycleSuccessors(ctx, tx, &snapshot, r.CandidateDeploymentID, mappings); err != nil {
		return a, err
	}
	binding := lifecycleBinding(snapshot, gate, saved, removal, fingerprint)
	a, err = lifecycleApproval(before.Contract, snapshot.Contract, r, binding, actor, appID, clock.Time, validator, snapshot)
	if err != nil {
		return a, err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return a, err
	}
	inserted, err := q.InsertRouteLifecycleApproval(ctx, tx, sqlc.InsertRouteLifecycleApprovalParams{ID: a.ID, AccountID: accountID, AppID: appID, BaselineID: a.BaselineDeploymentID, CandidateID: a.CandidateDeploymentID, Receipt: body, SuccessorSnapshot: successorSnapshot, ApprovedAt: pgtype.Timestamptz{Time: a.ApprovedAt, Valid: true}, ValidUntil: pgtype.Timestamptz{Time: a.ValidUntil, Valid: true}})
	if err != nil {
		return a, err
	}
	if inserted != 1 {
		return a, ErrRouteLifecycleReviewChanged
	}
	return a, tx.Commit(ctx)
}
func (s *PgStore) GetRouteLifecycleApproval(ctx context.Context, accountID, appID, id string) (api.RouteLifecycleApproval, error) {
	var a api.RouteLifecycleApproval
	body, err := sqlc.New().ReadRouteLifecycleApproval(ctx, s.pool, sqlc.ReadRouteLifecycleApprovalParams{ID: id, AccountID: accountID, AppID: appID})
	if err != nil {
		return a, routePolicyReadError(err)
	}
	err = json.Unmarshal(body, &a)
	return a, err
}
