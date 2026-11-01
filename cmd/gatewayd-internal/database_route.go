package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbroute"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

// validateDatabaseRoute checks the app route to managed databases
// (ADR-1027): the forwarder binds the service proxy bridge address on the
// reserved port, where the host DNATs database-route traffic, and relays to
// at least one pgproxyd. A DNS suffix needs the forwarder behind it.
func validateDatabaseRoute(cfg *Config) error {
	listen := strings.TrimSpace(cfg.DatabaseRouteListen)
	if listen == "" {
		if strings.TrimSpace(cfg.DatabaseRouteHostSuffix) != "" || len(cfg.DatabaseRouteUpstreams) > 0 {
			return errors.New("gatewayd: database_route_host_suffix and database_route_upstreams require database_route_listen")
		}
		return nil
	}
	host, portText, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("gatewayd: database_route_listen must be host:port: %w", err)
	}
	if port, err := strconv.Atoi(portText); err != nil || port != api.DatabaseRouteProxyPort {
		return fmt.Errorf("gatewayd: database_route_listen must use reserved port %d", api.DatabaseRouteProxyPort)
	}
	if strings.TrimSpace(cfg.ServiceProxyListen) == "" {
		return errors.New("gatewayd: database_route_listen requires service_proxy_listen (it names the tenant-bridge address)")
	}
	bridge, err := serviceProxyBridgeIP(cfg.ServiceProxyListen)
	if err != nil {
		return fmt.Errorf("gatewayd: service_proxy_listen: %w", err)
	}
	if ip, err := netip.ParseAddr(host); err != nil || ip != bridge {
		return fmt.Errorf("gatewayd: database_route_listen must bind the service proxy bridge address %s", bridge)
	}
	if len(cfg.DatabaseRouteUpstreams) == 0 {
		return errors.New("gatewayd: database_route_listen requires database_route_upstreams (pgproxyd host:port over the overlay)")
	}
	for _, upstream := range cfg.DatabaseRouteUpstreams {
		if _, _, err := net.SplitHostPort(strings.TrimSpace(upstream)); err != nil {
			return fmt.Errorf("gatewayd: database_route_upstreams entry %q: %w", upstream, err)
		}
	}
	return nil
}

// startDatabaseRoute serves the forwarder until ctx ends.
func startDatabaseRoute(ctx context.Context, deps runDeps, cfg *Config, errc chan<- error, log *slog.Logger) error {
	var metrics *tcpmetrics.Metrics
	if deps.metrics != nil {
		metrics = tcpmetrics.New(deps.metrics.Registry(), "gatewayd_internal_database_route")
	}
	upstreams := make([]string, 0, len(cfg.DatabaseRouteUpstreams))
	for _, upstream := range cfg.DatabaseRouteUpstreams {
		upstreams = append(upstreams, strings.TrimSpace(upstream))
	}
	forwarder, err := dbroute.New(dbroute.Config{
		Upstreams:            upstreams,
		Destination:          netip.AddrPortFrom(api.DatabaseRouteAddress(), api.DatabaseRoutePort),
		OriginalDestination:  gateway.TCPOriginalDestination,
		DialTimeout:          api.DatabaseRouteDialTimeout,
		MaxSessions:          api.DatabaseRouteSessionsPerNodeMax,
		MaxSessionsPerSource: api.DatabaseRouteSessionsPerSourceMax,
		Metrics:              metrics,
		Log:                  log,
	})
	if err != nil {
		return fmt.Errorf("gatewayd: database route: %w", err)
	}
	addr := strings.TrimSpace(cfg.DatabaseRouteListen)
	listener, err := deps.listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gatewayd: database route listen %s: %w", addr, err)
	}
	go func() {
		log.Info("gatewayd database route listening", "addr", addr, "route", api.DatabaseRouteAddress().String(), "upstreams", len(upstreams))
		if err := forwarder.Serve(ctx, listener); err != nil && ctx.Err() == nil {
			errc <- err
		}
	}()
	return nil
}

// withDatabaseRouteDNS makes the guest resolver answer <id>.<suffix> with
// the route address.
func withDatabaseRouteDNS(handler *gateway.ServiceDiscoveryDNSHandler, cfg *Config, log *slog.Logger) error {
	suffix := strings.TrimSpace(cfg.DatabaseRouteHostSuffix)
	if suffix == "" {
		return nil
	}
	if _, err := handler.WithDatabaseRoute(suffix, api.DatabaseRouteAddress(), api.DatabaseRouteDNSTTLSeconds*time.Second); err != nil {
		return fmt.Errorf("gatewayd: database_route_host_suffix: %w", err)
	}
	log.Info("gatewayd: guest DNS answers managed databases with the route", "suffix", suffix, "route", api.DatabaseRouteAddress().String())
	return nil
}
