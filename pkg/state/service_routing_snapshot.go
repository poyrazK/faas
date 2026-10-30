// adr: 570
package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ServiceDeploymentWeight struct {
	ID             string
	TrafficPercent int
}

// This optional extension belongs to the same read-only transaction as access
// policy. It cannot fall back to pool reads with a different committed view.
//
// A service call stays in its caller's environment. ServiceCallerEnvironment
// names it: "" for the production lane (no, default or production scope),
// otherwise the caller deployment's stage scope. Override and weight reads
// then only see target deployments in that environment, so production never
// reaches a stage and a stage never reaches production.
type ServiceRoutingPolicyReader interface {
	ResolveServiceRelease(context.Context, string, string, string, string) (string, string, error)
	ServiceCallerEnvironment(ctx context.Context, callerApp, callerDeployment string) (string, error)
	ServiceDeploymentOverrideAllowed(ctx context.Context, app, deployment, environment string) (bool, error)
	ServiceDeploymentWeights(ctx context.Context, app, environment string) ([]ServiceDeploymentWeight, error)
}

// serviceEnvironmentScopes lists the deployment scopes an environment owns.
func serviceEnvironmentScopes(environment string) []string {
	if environment == "" {
		return []string{"", DefaultEnvScope, "production"}
	}
	return []string{environment}
}

func (s servicePolicyReader) ResolveServiceRelease(ctx context.Context, caller, source, target, requested string) (string, string, error) {
	if requested != "" {
		if _, err := uuid.Parse(requested); err != nil {
			return "", "", ErrInvalidArgument
		}
	}
	if source == "" {
		return s.serviceReleaseWithoutMembership(ctx, caller, target, requested)
	}
	rows, err := sqlc.New().ReadServicePolicyReleaseCandidates(ctx, s.tx, sqlc.ReadServicePolicyReleaseCandidatesParams{
		CallerAppID: uuidToPgtype(caller), CallerDeploymentID: uuidToPgtype(source),
		TargetAppID: uuidToPgtype(target), RequestedReleaseID: uuidToPgtype(requested)})
	if err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		if requested != "" {
			return "", "", ErrNotFound
		}
		return s.serviceReleaseWithoutMembership(ctx, caller, target, requested)
	}
	if len(rows) != 1 || !rows[0].TargetLive {
		return "", "", ErrConflict
	}
	return pgUUIDString(rows[0].ID), pgUUIDString(rows[0].DeploymentID), nil
}

func (s servicePolicyReader) serviceReleaseWithoutMembership(ctx context.Context, caller, target, requested string) (string, string, error) {
	active, err := sqlc.New().ReadServicePolicyActiveRelease(ctx, s.tx, sqlc.ReadServicePolicyActiveReleaseParams{
		CallerAppID: uuidToPgtype(caller), TargetAppID: uuidToPgtype(target)})
	if err != nil {
		return "", "", err
	}
	if active || requested != "" {
		return "", "", ErrConflict
	}
	return "", "", nil
}

func (s servicePolicyReader) ServiceCallerEnvironment(ctx context.Context, callerApp, callerDeployment string) (string, error) {
	if callerDeployment == "" {
		return "", nil
	}
	scope, err := sqlc.New().ReadServicePolicyCallerScope(ctx, s.tx, sqlc.ReadServicePolicyCallerScopeParams{
		AppID: uuidToPgtype(callerApp), DeploymentID: uuidToPgtype(callerDeployment)})
	if err != nil {
		return "", mapErr(err)
	}
	if IsProductionRoutingScope(scope) {
		return "", nil
	}
	return scope, nil
}

func (s servicePolicyReader) ServiceDeploymentOverrideAllowed(ctx context.Context, app, deployment, environment string) (bool, error) {
	return sqlc.New().ReadServicePolicyDeploymentOverride(ctx, s.tx, sqlc.ReadServicePolicyDeploymentOverrideParams{
		AppID: uuidToPgtype(app), DeploymentID: uuidToPgtype(deployment), Scopes: serviceEnvironmentScopes(environment)})
}

// ServiceDeploymentWeights returns the target's traffic-bearing deployments in
// one environment. Production uses the public edge's single lane: named
// production once it carries traffic, the legacy default lane before that.
func (s servicePolicyReader) ServiceDeploymentWeights(ctx context.Context, app, environment string) ([]ServiceDeploymentWeight, error) {
	rows, err := sqlc.New().ReadServicePolicyDeploymentWeights(ctx, s.tx, sqlc.ReadServicePolicyDeploymentWeightsParams{
		AppID: uuidToPgtype(app), Scopes: serviceEnvironmentScopes(environment), RowLimit: int32(api.TrafficPolicyMaxDeployments + 1)})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.TrafficPolicyMaxDeployments {
		return nil, errors.New("service policy deployment limit exceeded")
	}
	deployments := make([]Deployment, 0, len(rows))
	for _, row := range rows {
		deployments = append(deployments, Deployment{ID: pgUUIDString(row.ID), Scope: row.Scope, TrafficPercent: int(row.TrafficPercent)})
	}
	if environment == "" {
		deployments = ProductionRoutingDeployments(deployments)
	}
	weights := make([]ServiceDeploymentWeight, 0, len(deployments))
	for _, deployment := range deployments {
		weights = append(weights, ServiceDeploymentWeight{ID: deployment.ID, TrafficPercent: deployment.TrafficPercent})
	}
	return weights, nil
}
