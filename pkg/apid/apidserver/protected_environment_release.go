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
	if app.ProjectID == "" || scope == "" || state.IsProductionScope(scope) {
		return nil
	}
	lookup, ok := store.(projectEnvironmentLookup)
	if !ok {
		return nil
	}
	env, err := lookup.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return api.ErrCapacity("could not read the project environment")
	}
	if !env.Protected {
		return nil
	}
	return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalRequired, "Protected environment",
		"The "+scope+" environment is protected: releases reach it through an approved project plan or environment promotion.").
		WithHint("Run `gregale project apply` or `gregale project promote` and approve the plan, or deploy with no traffic to stage the release.")
}

// stagedWithoutTraffic reports whether a deploy request stages its release
// with no traffic.
func stagedWithoutTraffic(req *api.CreateDeploymentRequest) bool {
	return req.TrafficPercent != nil && *req.TrafficPercent == 0
}
