package state

import "context"

// EnvironmentOwnsScope reports whether a deployment in scope belongs to a
// project environment. Production is one serving lane with two scope names
// (servingLaneScopes): a deploy writes the legacy default scope and a
// release-graph promotion writes "production", so production owns both. Any
// other environment owns only its own slug.
//
// Promotion and its rollback compared scopes by string equality, so for a
// project deployed the normal way (default scope) production looked empty:
// the promotion recorded no previous target, and rolling it back cleared the
// promoted deployment and left the app with nothing serving (prod CLI e2e
// 2026-11-14, 503 traffic_policy_unavailable until a redeploy).
func EnvironmentOwnsScope(environment, scope string) bool {
	if environment == "production" {
		return IsProductionScope(scope)
	}
	return normalizedDeploymentScope(scope) == environment
}

// EnvironmentDeploymentScope is the scope a direct write into a project
// environment uses, for both a new deployment and the values it reads:
// production resolves to the lane actually serving it, so the write lands
// where the serving deployment reads (ProjectEnvironmentCloneValueScope, the
// rule a clone already follows); any other environment is its own slug.
//
// Runtime values are read by exact scope. A promotion that created its
// deployment in "production" while the project's values lived in the default
// lane served without them (API_URL dropped), and a value written to
// "production" for an app serving from the default lane was never delivered
// (DATABASE_URL from `add postgres --env production`).
func EnvironmentDeploymentScope(ctx context.Context, store Store, accountID, appID, environment string) (string, error) {
	return ProjectEnvironmentCloneValueScope(ctx, store, accountID, appID, environment)
}
