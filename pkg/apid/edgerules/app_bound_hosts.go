package edgerules

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// AppBoundHostProblem refuses a match_host outside app when the request's
// credential is bound to one app: a per-app deploy token (ADR-091 amendment
// 2026-10-31, security review). The gateway applies an account's rules to
// every host they cover, whichever of the account's apps owns the rule, so a
// rule written through one app with match_host "*", a glob or another app's
// host would redirect, answer or refuse the account's other apps. Such a
// credential may write rules for "@app", the app's own host under each apps
// domain, and the app's custom domains; anything wider needs an account
// credential. Other principals are not limited here.
func (s *Service) AppBoundHostProblem(ctx context.Context, app state.App, matchHost string) *api.Problem {
	_, key, ok := authmw.AccountFromCtx(ctx)
	if !ok || key == nil || key.AppID == "" {
		return nil
	}
	if key.AppID != app.ID {
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
	if _, key, ok := authmw.AccountFromCtx(ctx); !ok || key == nil || key.AppID == "" {
		return nil
	}
	app, err := s.Host.Store().AppByID(ctx, appID)
	if err != nil {
		return api.ErrCapacity("could not read the rule's app")
	}
	return s.AppBoundHostProblem(ctx, app, matchHost)
}

func errAppBoundHost(slug, matchHost string) *api.Problem {
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Host outside the token's app",
		fmt.Sprintf("this credential is bound to app %s, so its edge rules may match only @app, the app's own host or "+
			"its custom domains, not %q; use an account API key for a rule that covers other hosts", slug, matchHost))
}
