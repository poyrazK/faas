package main

import (
	"context"
	"errors"
	"net"
	"strconv"

	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/state"
)

func startRehearsalPostgresProxy(ctx context.Context, deps runDeps, serviceAddress, node string, errc chan<- error) error {
	if osGetenv("FAAS_REHEARSAL_POSTGRES_FIXTURES") != "1" {
		return nil
	}
	bridge, e := serviceProxyBridgeIP(serviceAddress)
	if e != nil || deps.pgStore == nil {
		return errors.New("rehearsal PostgreSQL requires the private service bridge and durable state")
	}
	target := osGetenv("FAAS_REHEARSAL_POSTGRES_PROXY_TARGET")
	if !rehearsalpg.ValidProxyTarget(target) {
		return errors.New("rehearsal PostgreSQL requires an explicit local fixture-cluster target")
	}
	identities := newServiceProxyCallerIdentityResolver(deps.pgStore.ListAllInstances, node)
	identities.lookup = deps.pgStore.LiveServiceCallersByHostIP
	proxy := rehearsalpg.Proxy{Target: target, Resolve: func(ctx context.Context, remote string) (rehearsalpg.Resource, error) {
		app, _, e := identities.ResolveIdentity(ctx, remote)
		if e != nil || app == "" {
			return rehearsalpg.Resource{}, state.ErrNotFound
		}
		return deps.pgStore.ScenarioPostgresForApp(ctx, app)
	}}
	l, e := deps.listen("tcp", net.JoinHostPort(bridge.String(), strconv.Itoa(rehearsalpg.ProxyPort)))
	if e != nil {
		return e
	}
	go func() {
		if e := proxy.Serve(ctx, l); e != nil {
			select {
			case errc <- errors.New("rehearsal PostgreSQL private proxy stopped"):
			case <-ctx.Done():
			}
		}
	}()
	return nil
}
