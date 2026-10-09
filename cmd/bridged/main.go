// Command bridged owns unprivileged development laptop connections. Public
// ingress stays with gatewayd-public and apid; bridged binds only loopback.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() { wire.Daemon("bridged", run) }

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("bridged", role.FromConfig("", "FAAS_BRIDGED_ROLE"), role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	if os.Getenv("FAAS_DEV_BRIDGE_ENABLED") != "1" {
		return errors.New("bridged: preview is not enabled")
	}
	address := os.Getenv("FAAS_DEV_BRIDGE_ADDR")
	if address == "" {
		address = "127.0.0.1:9098"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("bridged: listener must be loopback")
	}
	pool, err := db.OpenReadOnlyWithAppName(ctx, "", "bridged")
	if err != nil {
		return fmt.Errorf("bridged database: %w", err)
	}
	defer pool.Close()
	store := state.NewPgStore(pool)
	lookup := func(ctx context.Context, account, id string) (devbridge.Session, error) {
		owner, err := store.AccountByID(ctx, account)
		if err != nil || owner.Status != state.AccountActive || owner.AbuseHoldAt != nil {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		session, err := store.DevBridgeByID(ctx, account, id)
		if err != nil {
			return session, err
		}
		env, err := store.ProjectEnvironmentByID(ctx, session.Scope.EnvironmentID)
		if err != nil || env.AccountID != account || env.ProjectID != session.Scope.ProjectID || env.Protected || env.Slug == "production" || env.Slug == "default" {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		app, err := store.AppByID(ctx, session.Scope.TargetAppID)
		if err != nil || app.AccountID != account || app.ProjectID != env.ProjectID || app.Status != state.AppActive {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		return session, nil
	}
	relay := devbridge.NewRelay(api.DevBridgeMaxConcurrentRequests)
	dependencyGateway := os.Getenv("FAAS_DEV_BRIDGE_GATEWAY_URL")
	if dependencyGateway == "" {
		dependencyGateway = "http://127.0.0.1:8080"
	}
	target, err := url.Parse(dependencyGateway)
	if err != nil {
		return errors.New("bridged: invalid dependency gateway URL")
	}
	ip := net.ParseIP(target.Hostname())
	if ip == nil || !ip.IsLoopback() || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return errors.New("bridged: invalid dependency gateway URL")
	}
	dependencies := httputil.NewSingleHostReverseProxy(target) //nolint:gosec // Operator configuration is restricted above to a literal loopback origin.
	director := dependencies.Director //nolint:staticcheck // SA1019: retain the qualified forwarding contract during the compiler patch.
	dependencies.Director = func(r *http.Request) { //nolint:staticcheck // SA1019: supported Go 1.26 API; Rewrite migration needs forwarding-contract qualification.
		director(r)
		r.Host = gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, r.Header.Get("X-Gregale-Dev-Environment"), r.Header.Get("X-Gregale-Dev-Dependency"))
		r.Header.Del("X-Gregale-Dev-Environment")
		r.Header.Del("X-Gregale-Dev-Dependency")
		r.Header.Del("X-Gregale-Dev-Account")
	}
	dependencies.FlushInterval = -1
	server := &http.Server{Addr: address, Handler: devbridge.NewServer(relay, lookup, dependencies), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: api.DevBridgeMaxHeaderBytes, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Info("development bridge relay listening", "address", address)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
