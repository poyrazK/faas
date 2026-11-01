package oidc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// DeployTargetLookup is the optional AccountLookup extension that resolves
// what a GitHub Actions identity may deploy (state.OIDCDeployTargetResolver).
type DeployTargetLookup interface {
	OIDCDeployTargetForSubject(ctx context.Context, accountID, issuerURL, subject, appSlug string) (state.OIDCDeployTarget, error)
}

// releaseTagPrefix is the tag family the workflow `gregale github setup`
// writes deploys from (`push: tags: ["v*"]`).
const releaseTagPrefix = "refs/tags/v"

// deployTarget binds a deploy bearer minted under a trust policy Gregale
// created (ADR-20261101100000). Such a policy follows the account's
// repository binding to every workflow of the repository (ADR-360), so it
// must not hand out more than the GitHub App's push-to-deploy does: a pull
// request workflow, or a workflow on any other branch, ran code nobody merged.
// The bearer is issued only to a run on a branch Gregale deploys from (or a
// v* release tag), outside pull_request events, for one app bound to the
// token's repository, and it is bound to that app.
func (h Handler) deployTarget(ctx context.Context, accountID, issuerURL, subject, app string, claims *Claims) (string, *api.Problem) {
	refuse := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Deploy not allowed from this workflow", detail)
	}
	switch claims.EventName {
	case "pull_request", "pull_request_target":
		return "", refuse("a pull request workflow cannot deploy: merge the change, or let its PR preview build it")
	}
	app = strings.TrimSpace(app)
	if app == "" {
		return "", refuse("name the app to deploy (`app`): a token issued through the repository's GitHub connection deploys one app bound to that repository")
	}
	lookup, ok := h.deps.Lookups.(DeployTargetLookup)
	if !ok {
		return "", api.ErrCapacity("deploy targets cannot be resolved")
	}
	target, err := lookup.OIDCDeployTargetForSubject(ctx, accountID, issuerURL, subject, app)
	if errors.Is(err, state.ErrNotFound) {
		return "", refuse("the app " + app + " is not bound to this repository")
	}
	if err != nil {
		return "", api.ErrCapacity("deploy target lookup failed")
	}
	if !deployRefAllowed(claims.Ref, target.Branches) {
		return "", refuse("this run is on " + claims.Ref + "; deploys come from the repository's deploy branches (" +
			strings.Join(target.Branches, ", ") + ") or v* release tags. Write the account's trust policy to allow another ref")
	}
	return target.AppID, nil
}

func deployRefAllowed(ref string, branches []string) bool {
	if strings.HasPrefix(ref, releaseTagPrefix) {
		return true
	}
	for _, branch := range branches {
		if ref == "refs/heads/"+branch {
			return true
		}
	}
	return false
}

func grantsDeploy(scopes []string) bool {
	for _, scope := range scopes {
		if scope == api.ScopeDeployWrite {
			return true
		}
	}
	return false
}
