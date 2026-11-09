package edgerules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// restrictedKey returns the request's credential when it reaches less than
// the whole account: a per-app deploy token (AppID set) or an agent or
// mcp-login key limited to projects (ADR-20261104085712). Edge rules are
// scoped by owner, not by app or project, so such a credential is held to
// its own app's hosts and route targets (ADR-091 amendments 2026-10-31,
// 2026-11-02 and 2026-11-07). Account keys, organization keys and dashboard
// sessions return nil.
func restrictedKey(ctx context.Context) *state.APIKey {
	_, key, ok := authmw.AccountFromCtx(ctx)
	if !ok || key == nil || (key.AppID == "" && !key.ProjectLimited) {
		return nil
	}
	return key
}

// restrictedKeyReaches reports whether a restricted credential may touch app:
// the token's own app, or an app inside the key's projects.
func restrictedKeyReaches(key *state.APIKey, app state.App) bool {
	if key.AppID != "" && key.AppID != app.ID {
		return false
	}
	return !authmw.AppOutsideKeyProjects(key, app)
}

// AppBoundHostProblem refuses a match_host outside app when the request's
// credential reaches less than the account: a per-app deploy token (ADR-091
// amendment 2026-10-31, security review) or a project-limited agent or
// mcp-login key (amendment 2026-11-07). The gateway applies an owner's rules
// to every host they cover, whichever of the owner's apps holds the rule, so
// a rule written through one app with match_host "*", a glob or another
// app's host would redirect, answer, route or refuse the owner's other apps,
// including apps outside the key's projects. Such a credential may write
// rules for "@app", the app's own host under each apps domain, and the app's
// custom domains; anything wider needs an account credential. Other
// principals are not limited here.
func (s *Service) AppBoundHostProblem(ctx context.Context, app state.App, matchHost string) *api.Problem {
	key := restrictedKey(ctx)
	if key == nil {
		return nil
	}
	if !restrictedKeyReaches(key, app) {
		return errAppBoundHost(app.Slug, matchHost)
	}
	host := strings.ToLower(strings.TrimSpace(matchHost))
	if api.IsEdgeRuleAppHosts(host) {
		return nil
	}
	domains := hostidentity.Configured()
	if canonical, ok := domains.CanonicalAppHost(host); ok && canonical == strings.ToLower(app.Slug)+"."+domains.Apps {
		return nil
	}
	owned, err := s.Host.Store().ListDomainsForApp(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app's custom domains")
	}
	for _, d := range owned {
		if strings.EqualFold(strings.TrimSpace(d.Domain), host) {
			return nil
		}
	}
	return errAppBoundHost(app.Slug, matchHost)
}

// appBoundUpdateHostProblem is AppBoundHostProblem for an update, which
// knows the rule's app only by ID.
func (s *Service) appBoundUpdateHostProblem(ctx context.Context, appID, matchHost string) *api.Problem {
	if restrictedKey(ctx) == nil {
		return nil
	}
	app, err := s.Host.Store().AppByID(ctx, appID)
	if err != nil {
		return api.ErrCapacity("could not read the rule's app")
	}
	return s.AppBoundHostProblem(ctx, app, matchHost)
}

// StoredRuleReachProblem refuses a restricted credential any change to a
// stored rule (update, mode switch, WAF exclusion, delete) whose match_host
// reaches past the credential's app (ADR-091 amendment 2026-11-07). An
// account credential may have written "*" or another app's host on an app
// the key can reach; disabling or editing that rule would change the gates
// of apps the key cannot reach.
func (s *Service) StoredRuleReachProblem(ctx context.Context, row state.EdgeRule) *api.Problem {
	if restrictedKey(ctx) == nil {
		return nil
	}
	return s.appBoundUpdateHostProblem(ctx, row.AppID, row.MatchHost)
}

// appBoundRouteTargetProblem refuses a kind=route target the request's
// credential cannot reach (ADR-091 amendment 2026-11-02, review round 8; and
// 2026-11-07 for project-limited keys). The match_host check above keeps
// such a rule on the app's own hosts, but its target could still be any
// public app of the owner: a leaked token could serve another app on this
// app's hosts, past that app's host-specific rules. A deploy token may route
// only to its own app; a project-limited key only to apps in its projects.
// The refusal does not depend on whether the slug exists, so it reveals
// nothing about other slugs.
func (s *Service) appBoundRouteTargetProblem(ctx context.Context, sourceAppID, targetSlug string) *api.Problem {
	key := restrictedKey(ctx)
	if key == nil {
		return nil
	}
	if key.AppID == "" || key.AppID == sourceAppID {
		target, err := s.Host.Store().AppBySlug(ctx, targetSlug)
		switch {
		case err == nil && restrictedKeyReaches(key, target) && (key.AppID == "" || target.ID == key.AppID):
			return nil
		case err != nil && !errors.Is(err, state.ErrNotFound):
			return api.ErrCapacity("could not check the route target")
		}
	}
	reach := "only that app"
	if key.AppID == "" && key.ProjectLimited {
		reach = "only apps in its projects"
	}
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Route target outside the credential's reach",
		fmt.Sprintf("this credential is limited, so its route rules may serve %s, not %q; "+
			"use an account API key to route to another app", reach, targetSlug))
}

func errAppBoundHost(slug, matchHost string) *api.Problem {
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Host outside the credential's app",
		fmt.Sprintf("this credential is limited to app %s or its projects, so its edge rules may match only @app, the app's own host or "+
			"its custom domains, not %q; use an account API key for a rule that covers other hosts", slug, matchHost))
}
