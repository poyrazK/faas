package state

import "context"

// RolloutRecoveryActorOperator is the deployment_audit actor of a rollout
// recovery a person asked for (`gregale rollouts recover` or the API).
const RolloutRecoveryActorOperator = "operator:cli:recover_rollout"

// Actors of rollout recoveries the platform decided on its own. The release
// timeline prints the actor, so an automatic abort must not read as an
// operator's command (Hunt 32 H32-58).
const (
	RolloutRecoveryActorSafeDeploy      = "system:safe_deploy"
	RolloutRecoveryActorRouteHealth     = "system:route_health_deadline"
	RolloutRecoveryActorReleaseApproval = "system:release_approval"
)

type rolloutRecoveryActorKey struct{}

// WithRolloutRecoveryActor names who asked for the rollout recoveries the
// store runs under ctx.
func WithRolloutRecoveryActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, rolloutRecoveryActorKey{}, actor)
}

// rolloutRecoveryActor is the actor on ctx; a call without one is an
// operator's, as before callers could name themselves.
func rolloutRecoveryActor(ctx context.Context) string {
	if actor, ok := ctx.Value(rolloutRecoveryActorKey{}).(string); ok && actor != "" {
		return actor
	}
	return RolloutRecoveryActorOperator
}
