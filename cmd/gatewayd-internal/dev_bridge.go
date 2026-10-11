package main

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func bridgeIdentityEqual(a, b string) bool {
	left, e1 := uuid.Parse(a)
	right, e2 := uuid.Parse(b)
	return e1 == nil && e2 == nil && left == right
}

type bridgeAuthority struct {
	id, token, account, context string
	propagated                  devbridge.RequestContext
}

func developmentBridgeAuthorization(store state.Store) func(*http.Request) *api.Problem {
	return func(r *http.Request) *api.Problem {
		authority, problem := parseBridgeAuthority(r)
		if problem != nil {
			return problem
		}
		if authority.id == "" && authority.token == "" && authority.context == "" {
			devbridge.ClearCredentials(r.Header)
			return nil
		}
		env, app, ok := gateway.EnvironmentIDsFromHost(wire.DeployWildcardSuffix, strings.Split(r.Host, ":")[0])
		if !ok || len(authority.id) != 43 || len(authority.token) > 128 {
			return bridgeDenied("use the selected development environment")
		}
		session, problem := bridgeRoutingSession(r.Context(), store, authority, env, app)
		if problem != nil {
			return problem
		}
		if !bridgeAuthorityPermitted(session, authority, app) {
			return bridgeDenied("invalid or expired routing credential")
		}
		if r.Header.Get(api.ReleaseHeader) != "" || r.Header.Get(api.TargetDeploymentHeader) != "" || r.Header.Get(api.RevisionHeader) != "" {
			return api.NewProblem(409, "dev_bridge_routing_conflict", "Bridge routing conflict", "session routing cannot be combined with release or revision overrides")
		}
		stampBridgeAuthority(r, session, authority, env, app)
		return nil
	}
}

func parseBridgeAuthority(r *http.Request) (bridgeAuthority, *api.Problem) {
	a := bridgeAuthority{id: r.Header.Get(devbridge.SessionHeader), token: r.Header.Get(devbridge.TokenHeader), account: r.Header.Get(devbridge.AccountHeader), context: r.Header.Get(devbridge.ContextHeader)}
	for _, key := range []string{devbridge.SessionHeader, devbridge.TokenHeader, devbridge.AccountHeader, devbridge.ContextHeader} {
		if len(r.Header.Values(key)) > 1 {
			return a, bridgeDenied("ambiguous routing credential")
		}
	}
	if a.context == "" {
		return a, nil
	}
	var err error
	a.propagated, err = devbridge.ParseRequestContext(a.context)
	if err != nil || (a.id != "" && a.id != a.propagated.SessionID) || (a.account != "" && a.account != a.propagated.AccountID) {
		return a, bridgeDenied("invalid session context")
	}
	a.id, a.account = a.propagated.SessionID, a.propagated.AccountID
	if a.token == "" {
		a.token = a.propagated.Token
	}
	return a, nil
}

func bridgeRoutingSession(ctx context.Context, store state.Store, a bridgeAuthority, env, app string) (devbridge.Session, *api.Problem) {
	bridges, ok := store.(state.DevBridgeStore)
	if !ok {
		return devbridge.Session{}, api.ErrCapacity("bridge session storage unavailable")
	}
	session, err := bridges.DevBridgeByID(ctx, a.account, a.id)
	if err != nil || !bridgeIdentityEqual(env, session.Scope.EnvironmentID) {
		return session, bridgeDenied("invalid session scope")
	}
	current, err := bridgeEnvironment(ctx, store, session.Scope.EnvironmentID)
	owner, ownerErr := store.AccountByID(ctx, a.account)
	if err != nil || ownerErr != nil || owner.Status != state.AccountActive || owner.AbuseHoldAt != nil || current.AccountID != session.Scope.AccountID || current.ProjectID != session.Scope.ProjectID || current.Protected || current.Slug == "production" || current.Slug == "default" {
		return session, bridgeDenied("environment no longer permits local development")
	}
	allowedApp := bridgeScopedApp(session, app)
	resource, resourceErr := store.AppByID(ctx, allowedApp)
	if allowedApp == "" || resourceErr != nil || resource.Status != state.AppActive || resource.AccountID != a.account || resource.ProjectID != current.ProjectID {
		return session, bridgeDenied("service is outside the selected project")
	}
	return session, nil
}

func bridgeAuthorityPermitted(session devbridge.Session, a bridgeAuthority, app string) bool {
	allowedApp := bridgeScopedApp(session, app)
	if a.context != "" && session.AuthorizeContextRoute(time.Now(), a.propagated, session.Scope.EnvironmentID, allowedApp) != nil {
		return false
	}
	if bridgeIdentityEqual(app, session.Scope.TargetAppID) {
		return session.AuthorizeRequest(time.Now(), a.token, session.Scope.AccountID, session.Scope.EnvironmentID, allowedApp) == nil
	}
	if session.AuthorizeDependency(time.Now(), a.token, session.Scope.AccountID, session.Scope.EnvironmentID, allowedApp) == nil {
		return true
	}
	return a.context != "" && a.token == a.propagated.Token
}

func stampBridgeAuthority(r *http.Request, session devbridge.Session, a bridgeAuthority, env, app string) {
	if bridgeIdentityEqual(app, session.Scope.TargetAppID) {
		a.propagated = devbridge.RequestContext{AccountID: a.account, SessionID: a.id, Token: a.token}
		a.context = a.propagated.Encode()
	}
	if a.context != "" {
		r.Header.Set(devbridge.ContextHeader, a.context)
		// Only request authority may reach a remote workload.
		a.token = a.propagated.Token
	}
	r.Header.Set(devbridge.SessionHeader, a.id)
	r.Header.Set(devbridge.AccountHeader, a.account)
	r.Header.Set(devbridge.TokenHeader, a.token)
	*r = *r.WithContext(gateway.WithDevBridgeScope(r.Context(), session.Scope.AccountID, env, app))
}

func bridgeDenied(detail string) *api.Problem {
	return api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", detail)
}

func bridgeEnvironment(ctx context.Context, store state.Store, id string) (state.ProjectEnvironment, error) {
	reader, ok := store.(interface {
		ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
	})
	if !ok {
		return state.ProjectEnvironment{}, state.ErrNotFound
	}
	return reader.ProjectEnvironmentByID(ctx, id)
}

func bridgeScopedApp(session devbridge.Session, app string) string {
	if bridgeIdentityEqual(app, session.Scope.TargetAppID) {
		return session.Scope.TargetAppID
	}
	for _, dependency := range session.Scope.DependencyAppIDs {
		if bridgeIdentityEqual(app, dependency) {
			return dependency
		}
	}
	return ""
}

// This seam is installed only on the node-local listener with authoritative
// source-instance identity. Normal service binding authorization runs first.
func developmentBridgeServiceForwarder(store state.Store, route http.Handler) gateway.ServiceProxyDevBridge {
	return func(w http.ResponseWriter, r *http.Request, caller gateway.ServiceCaller, target gateway.ServiceTarget, path string) {
		env, problem := bridgeServiceEnvironment(r, store, caller, target)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path, clone.URL.RawPath = path, ""
		if strings.HasSuffix(r.URL.Path, path) && r.URL.RawPath != "" {
			prefix := strings.TrimSuffix(r.URL.Path, path)
			clone.URL.RawPath = strings.TrimPrefix(r.URL.EscapedPath(), prefix)
		}
		clone.Host = gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, target.AppID)
		clone.URL.Host = clone.Host
		devbridge.ClearCredentials(clone.Header)
		clone.Header.Del(gateway.ServiceProxyCallerAppHeader)
		clone.Header.Del(gateway.ServiceCallerAssertionHeader)
		route.ServeHTTP(w, clone)
	}
}

func bridgeServiceEnvironment(r *http.Request, store state.Store, caller gateway.ServiceCaller, target gateway.ServiceTarget) (state.ProjectEnvironment, *api.Problem) {
	var env state.ProjectEnvironment
	c, err := devbridge.ParseRequestContext(r.Header.Get(devbridge.ContextHeader))
	bridges, ok := store.(state.DevBridgeStore)
	if err != nil || !ok || len(r.Header.Values(devbridge.ContextHeader)) != 1 || caller.DeploymentID == "" || caller.AccountID != c.AccountID {
		return env, bridgeDenied("verified development caller is required")
	}
	session, err := bridges.DevBridgeByID(r.Context(), c.AccountID, c.SessionID)
	if err != nil || session.AuthorizeContextRoute(time.Now(), c, session.Scope.EnvironmentID, target.AppID) != nil || bridgeScopedApp(session, caller.AppID) == "" {
		return env, bridgeDenied("service call is outside the session graph")
	}
	env, err = bridgeEnvironment(r.Context(), store, session.Scope.EnvironmentID)
	deployment, depErr := store.DeploymentByID(r.Context(), caller.DeploymentID)
	resource, resourceErr := store.AppByID(r.Context(), caller.AppID)
	if err != nil || depErr != nil || resourceErr != nil || resource.AccountID != c.AccountID || resource.ProjectID != session.Scope.ProjectID || env.ProjectID != session.Scope.ProjectID || env.AccountID != c.AccountID || deployment.AppID != caller.AppID || deployment.Scope != env.Slug {
		return env, bridgeDenied("caller deployment is outside the selected environment")
	}
	if r.Header.Get(api.ReleaseHeader) != "" || r.Header.Get(api.TargetDeploymentHeader) != "" || r.Header.Get(api.RevisionHeader) != "" {
		return env, api.NewProblem(409, "dev_bridge_routing_conflict", "Bridge routing conflict", "session routing cannot be combined with release or revision overrides")
	}
	return env, nil
}

func developmentBridgeForwarder(store state.DevBridgeStore, target *url.URL) func(http.ResponseWriter, *http.Request, gateway.App) bool {
	return func(w http.ResponseWriter, r *http.Request, app gateway.App) bool {
		id := r.Header.Get(devbridge.SessionHeader)
		if id == "" {
			return false
		}
		session, err := store.DevBridgeByID(r.Context(), app.AccountID, id)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", "invalid session"))
			return true
		}
		if !bridgeIdentityEqual(app.ID, session.Scope.TargetAppID) {
			devbridge.ClearCredentials(r.Header)
			return false
		}
		if session.AuthorizeRequest(time.Now(), r.Header.Get(devbridge.TokenHeader), app.AccountID, session.Scope.EnvironmentID, session.Scope.TargetAppID) != nil {
			api.WriteProblem(w, api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", "invalid session"))
			return true
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director                 //nolint:staticcheck // SA1019: retain the qualified bridge forwarding contract during the compiler patch.
		proxy.Director = func(out *http.Request) { //nolint:staticcheck // SA1019: supported Go 1.26 API; Rewrite migration needs bridge-contract qualification.
			director(out)
			out.URL.Path = "/v1/dev/bridges/" + id + "/traffic" + r.URL.Path
			if r.URL.RawPath != "" {
				out.URL.RawPath = "/v1/dev/bridges/" + id + "/traffic" + r.URL.EscapedPath()
			}
			out.Header.Set(devbridge.AccountHeader, session.Scope.AccountID)
		}
		proxy.FlushInterval = -1
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			api.WriteProblem(w, api.NewProblem(503, "dev_bridge_disconnected", "Bridge disconnected", "the local process is unavailable"))
		}
		_ = http.NewResponseController(w).EnableFullDuplex()
		proxy.ServeHTTP(w, r)
		return true
	}
}
