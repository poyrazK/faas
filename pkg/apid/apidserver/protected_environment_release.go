// adr: 20261019071733 — amendment 9: protected environments take releases
// only through an approved plan or promotion.
package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectEnvironmentLookup interface {
	ProjectEnvironmentBySlug(ctx context.Context, accountID, projectID, slug string) (state.ProjectEnvironment, error)
}

// protectedEnvironmentReleaseProblem refuses a release that would take
// traffic in a protected project environment other than production, outside
// an approved project plan or environment promotion. Production keeps its
// direct releases, which its own guards (freezes, approvals, the default
// rollout) hold (ADR-233, ADR-20261019071733 amendment 9). A release staged
// without traffic may still be deployed there: it reaches traffic only
// through those approved paths. Lookup failures other than not-found refuse.
func protectedEnvironmentReleaseProblem(ctx context.Context, store any, app state.App, scope string) *api.Problem {
	protected, problem := protectedNonProductionEnvironment(ctx, store, app.AccountID, app.ProjectID, scope)
	if problem != nil || !protected {
		return problem
	}
	return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalRequired, "Protected environment",
		"The "+scope+" environment is protected: releases reach it through an approved project plan or environment promotion.").
		WithHint("Run `gregale project apply` or `gregale project promote` and approve the plan, or deploy with no traffic to stage the release.")
}

// protectedNonProductionEnvironment reports whether scope names a protected
// project environment other than production. Lookup failures other than
// not-found refuse.
func protectedNonProductionEnvironment(ctx context.Context, store any, accountID, projectID, scope string) (bool, *api.Problem) {
	if projectID == "" || scope == "" || state.IsProductionScope(scope) {
		return false, nil
	}
	lookup, ok := store.(projectEnvironmentLookup)
	if !ok {
		return false, nil
	}
	env, err := lookup.ProjectEnvironmentBySlug(ctx, accountID, projectID, scope)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, api.ErrCapacity("could not read the project environment")
	}
	return env.Protected, nil
}

// protectedEnvironmentConfigProblem refuses setting configuration values
// (env vars, secret values, project environment configuration) in a
// protected environment other than production, outside an approved project
// plan or environment promotion: the runtime reads them on the next wake, so
// they take effect like a release (ADR-20261019071733 amendment 11).
// Production keeps direct config writes, which the config guard (ADR-1451)
// undoes, and revoking a secret (delete, rotate) stays open everywhere for
// incident response.
func protectedEnvironmentConfigProblem(ctx context.Context, store any, accountID, projectID, scope string) *api.Problem {
	protected, problem := protectedNonProductionEnvironment(ctx, store, accountID, projectID, scope)
	if problem != nil || !protected {
		return problem
	}
	return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalRequired, "Protected environment",
		"The "+scope+" environment is protected: its configuration changes through an approved project plan or environment promotion.").
		WithHint("Change it in the source environment and promote, or run `gregale project apply` and approve the plan. Deleting or rotating a secret stays open for incident response.")
}

// stagedWithoutTraffic reports whether a deploy request stages its release
// with no traffic.
func stagedWithoutTraffic(req *api.CreateDeploymentRequest) bool {
	return req.TrafficPercent != nil && *req.TrafficPercent == 0
}
