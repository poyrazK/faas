package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

var cronDeploymentScopeRE = lazyregexp.New(`^[a-z][a-z0-9-]{0,62}$`)

// NormalizeCronDeploymentScope returns the stored scope of a cron created for
// an environment (ADR-20261104120000): production, by any of its names, is the
// empty default scope; any other environment keeps its slug.
func NormalizeCronDeploymentScope(environment string) string {
	if IsProductionScope(environment) {
		return ""
	}
	return environment
}

// ValidCronDeploymentScope reports whether a cron may be written for this
// environment slug. The database also requires the environment to exist in
// the app's project.
func ValidCronDeploymentScope(environment string) bool {
	scope := NormalizeCronDeploymentScope(environment)
	return scope == "" || cronDeploymentScopeRE.MatchString(scope)
}

// CronInScope reports whether a cron fires into the deployment scope of a
// live deployment: production crons into either production scope, any other
// cron into its own environment.
func CronInScope(c Cron, deploymentScope string) bool {
	if c.DeploymentScope == "" {
		return IsProductionScope(deploymentScope)
	}
	return c.DeploymentScope == deploymentScope
}

// cronScopeDeploymentSQL is the predicate that keeps a cron's live deployment
// d in the cron's environment: a production cron (empty scope) runs in either
// production scope, any other cron in its own environment. cronParam is the
// placeholder of the cron's id.
func cronScopeDeploymentSQL(cronParam string) string {
	return `exists (select 1 from crons cron_scope where cron_scope.id = ` + cronParam + `::uuid and
	     (case when cron_scope.deployment_scope = '' then coalesce(nullif(d.scope, ''), 'default') in ('default', 'production')
	           else d.scope = cron_scope.deployment_scope end))`
}

// CronScopeSuspensionStore suspends the crons of one environment of an app
// that lost its live deployment, leaving the app's other environments firing.
type CronScopeSuspensionStore interface {
	SuspendCronsForAppScope(ctx context.Context, appID, scope, reason string) (int, error)
}

// SuspendCronEnvironment suspends the crons of c's environment for want of a
// live deployment there. A store without environment suspension suspends
// every cron of the app, as before.
func SuspendCronEnvironment(ctx context.Context, store CronSuspensionStore, c Cron) (int, error) {
	if scoped, ok := store.(CronScopeSuspensionStore); ok {
		return scoped.SuspendCronsForAppScope(ctx, c.AppID, c.DeploymentScope, CronSuspendedNoLiveDeployment)
	}
	return store.SuspendCronsForApp(ctx, c.AppID, CronSuspendedNoLiveDeployment)
}
