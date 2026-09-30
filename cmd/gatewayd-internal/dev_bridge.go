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

func developmentBridgeAuthorization(store state.Store) func(*http.Request) *api.Problem {
	return func(r *http.Request) *api.Problem {
		id := r.Header.Get(devbridge.SessionHeader)
		token := r.Header.Get(devbridge.TokenHeader)
		if id == "" && token == "" {
			devbridge.ClearCredentials(r.Header)
			return nil
		}
		env, app, ok := gateway.EnvironmentIDsFromHost(wire.DeployWildcardSuffix, strings.Split(r.Host, ":")[0])
		if !ok || len(id) != 43 || len(token) > 128 {
			return api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", "use the selected development environment")
		}
		bridges, ok := store.(state.DevBridgeStore)
		if !ok {
			return api.ErrCapacity("bridge session storage unavailable")
		}
		session, err := bridges.DevBridgeByID(r.Context(), r.Header.Get(devbridge.AccountHeader), id)
		if err != nil || !bridgeIdentityEqual(env, session.Scope.EnvironmentID) {
			return api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", "invalid session scope")
		}
		environments, ok := store.(interface {
			ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
		})
		if !ok {
			return api.ErrCapacity("bridge environment lookup unavailable")
		}
		current, err := environments.ProjectEnvironmentByID(r.Context(), session.Scope.EnvironmentID)
		if err != nil || current.AccountID != session.Scope.AccountID || current.Protected || current.Slug == "production" || current.Slug == "default" {
			return api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", "environment no longer permits local development")
		}
		authorized := false
		if bridgeIdentityEqual(app, session.Scope.TargetAppID) {
			authorized = session.AuthorizeRequest(time.Now(), token, session.Scope.AccountID, session.Scope.EnvironmentID, session.Scope.TargetAppID) == nil
		} else {
			for _, dependency := range session.Scope.DependencyAppIDs {
				if bridgeIdentityEqual(app, dependency) {
					authorized = session.AuthorizeDependency(time.Now(), token, session.Scope.AccountID, session.Scope.EnvironmentID, dependency) == nil
					break
				}
			}
		}
		if !authorized {
			return api.NewProblem(403, "dev_bridge_unauthorized", "Bridge denied", "invalid or expired routing credential")
		}
		*r = *r.WithContext(gateway.WithDevBridgeScope(r.Context(), session.Scope.AccountID, env, app))
		return nil
	}
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
		director := proxy.Director
		proxy.Director = func(out *http.Request) {
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
		proxy.ServeHTTP(w, r)
		return true
	}
}
