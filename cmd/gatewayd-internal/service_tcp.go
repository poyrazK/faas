package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

// newServiceTCPTargetResolver maps a service address to an app in the
// caller's account (ADR-576). The account comes from the caller row, never
// from the connection, so an address from another account resolves to
// nothing. Availability and ports come from the same projection the HTTP
// gateway routes with.
func newServiceTCPTargetResolver(store state.Store) gateway.ServiceTCPTargetResolver {
	return func(ctx context.Context, callerAppID string, address netip.Addr) (gateway.ServiceTCPTarget, bool, error) {
		index, ok := api.ServiceAddressIndexOf(address)
		if !ok || !isAppID(callerAppID) {
			return gateway.ServiceTCPTarget{}, false, nil
		}
		caller, err := store.AppByID(ctx, callerAppID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceTCPTarget{}, false, nil
		}
		if err != nil {
			return gateway.ServiceTCPTarget{}, false, fmt.Errorf("service TCP: load caller: %w", err)
		}
		if caller.Status == state.AppDeleted || caller.AccountID == "" {
			return gateway.ServiceTCPTarget{}, false, nil
		}
		app, err := store.AppByServiceAddressIndex(ctx, caller.AccountID, index)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceTCPTarget{}, false, nil
		}
		if err != nil {
			return gateway.ServiceTCPTarget{}, false, fmt.Errorf("service TCP: resolve address: %w", err)
		}
		resolved, routable, err := (pgRouter{store: store}).toApp(ctx, app)
		if err != nil {
			return gateway.ServiceTCPTarget{}, false, fmt.Errorf("service TCP: project target: %w", err)
		}
		if !routable {
			return gateway.ServiceTCPTarget{}, false, nil
		}
		// Mirror the public edge's lifecycle gate: past_due still serves.
		if resolved.MaintenanceMode || resolved.SecurityQuarantined || resolved.AccountAbuseHeld ||
			resolved.AccountStatus == string(state.AccountSuspended) ||
			resolved.AccountStatus == string(state.AccountDeletedPending) {
			return gateway.ServiceTCPTarget{}, false, gateway.ErrServiceTCPTargetUnavailable
		}
		live, err := store.LiveDeployments(ctx, app.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceTCPTarget{}, false, fmt.Errorf("service TCP: live deployments: %w", err)
		}
		result := gateway.ServiceTCPTarget{
			AppID:       app.ID,
			AccountID:   app.AccountID,
			Plan:        resolved.Plan,
			TCPPorts:    serviceTCPPorts(app, live),
			IdleTimeout: serviceTCPIdleTimeout(resolved),
		}
		if caller.PreviewOfSlug != "" && caller.PreviewPrNumber == 0 {
			member, memberErr := store.ScenarioTestMemberByApp(ctx, caller.ID)
			if memberErr != nil && !errors.Is(memberErr, state.ErrNotFound) {
				return gateway.ServiceTCPTarget{}, false, memberErr
			}
			if memberErr == nil {
				targetMember, targetErr := store.ScenarioTestMemberByApp(ctx, app.ID)
				if targetErr != nil || targetMember.RunID != member.RunID || targetMember.AccountID != member.AccountID {
					return gateway.ServiceTCPTarget{}, false, gateway.ErrServiceProxyDenied
				}
				result.ScenarioTestRunID = member.RunID
				result.ScenarioWorkload = targetMember.Workload
			}
		}
		return result, true, nil
	}
}

// serviceTCPPorts is what a service address exposes: every TCP listener the
// app declares, internal ones included, plus each traffic-bearing
// deployment's serving port. Image EXPOSE lists are not persisted beyond the
// serving port they seed, so a multi-listener image declares its extra ports
// through the app's ports (or compose expose:).
func serviceTCPPorts(app state.App, live []state.Deployment) []int {
	var out []int
	add := func(port int) {
		if port >= 1 && port <= 65535 && !slices.Contains(out, port) {
			out = append(out, port)
		}
	}
	for _, port := range app.Manifest.Ports {
		if port.EffectiveProtocol() == api.WorkloadPortTCP {
			add(port.Port)
		}
	}
	for _, dep := range live {
		if dep.TrafficPercent <= 0 {
			continue
		}
		port := sched.DeploymentRuntimePort(dep)
		if port == 0 {
			port = app.Manifest.Port
		}
		if port == 0 {
			port = api.DefaultAppPort
		}
		add(port)
	}
	slices.Sort(out)
	return out
}

// serviceTCPIdleTimeout is the target's idle timeout: an idle session should
// keep a target awake no longer than an idle HTTP app stays up.
func serviceTCPIdleTimeout(app gateway.App) time.Duration {
	seconds := app.IdleTimeoutS
	if seconds <= 0 {
		if limits, ok := api.LimitsFor(app.Plan); ok {
			seconds = limits.DefaultIdleTimeoutS(string(app.Type))
		}
	}
	if seconds <= 0 {
		return api.StreamingIdleTimeoutDefault
	}
	return time.Duration(seconds) * time.Second
}

// validateServiceTCPListen pins the service TCP proxy to the reserved port
// on the same tenant-bridge address as the HTTP service proxy, which is
// where the host DNAT sends service-address traffic.
func validateServiceTCPListen(addr, serviceProxyAddr string) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("gatewayd: service_tcp_listen must be host:port: %w", err)
	}
	if port, err := strconv.Atoi(portText); err != nil || port != api.ServiceTCPProxyPort {
		return fmt.Errorf("gatewayd: service_tcp_listen must use reserved port %d", api.ServiceTCPProxyPort)
	}
	if strings.TrimSpace(serviceProxyAddr) == "" {
		return errors.New("gatewayd: service_tcp_listen requires service_proxy_listen (identity and authorization are shared)")
	}
	bridge, err := serviceProxyBridgeIP(serviceProxyAddr)
	if err != nil {
		return fmt.Errorf("gatewayd: service_proxy_listen: %w", err)
	}
	if ip, err := netip.ParseAddr(host); err != nil || ip != bridge {
		return fmt.Errorf("gatewayd: service_tcp_listen must bind the service proxy bridge address %s", bridge)
	}
	return nil
}

// startServiceTCPProxy serves private TCP service addresses until ctx ends.
func startServiceTCPProxy(ctx context.Context, deps runDeps, addr string, services *gateway.ServiceProxy, store state.Store, errc chan<- error, log *slog.Logger) error {
	if deps.nodeCache == nil || deps.metrics == nil {
		return errors.New("gatewayd: service_tcp_listen needs the vmmd node cache and metrics")
	}
	metrics := tcpmetrics.New(deps.metrics.Registry(), "gatewayd_internal_service")
	forwarder := gateway.TCPForwarder{Nodes: deps.nodeCache.cache, Metrics: metrics}
	proxy, err := gateway.NewServiceTCPProxy(gateway.ServiceTCPProxyConfig{
		Services:     services,
		ResolveChaos: newServiceTCPChaosResolver(store),
		Resolve:      newServiceTCPTargetResolver(store),
		Forward: func(ctx context.Context, conn net.Conn, target gateway.Target, idle time.Duration) error {
			session := forwarder
			session.IdleTimeout = idle
			return session.ServeConnAwaitingDial(ctx, conn, target)
		},
		OriginalDestination: gateway.TCPOriginalDestination,
		AddressCIDR:         api.ServiceAddressCIDR(),
		ReservedPorts:       api.ServiceTCPReservedPorts(),
		SessionsPerAccount:  func(plan api.Plan) int { return plan.ServiceTCPSessionsPerAccount() },
		MaxSessions:         api.ServiceTCPSessionsPerNodeMax,
		WakeTimeout:         api.ServiceTCPWakeTimeout,
		Metrics:             metrics,
		Log:                 log,
	})
	if err != nil {
		return fmt.Errorf("gatewayd: service TCP proxy: %w", err)
	}
	listener, err := deps.listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gatewayd: service TCP listen %s: %w", addr, err)
	}
	go func() {
		log.Info("gatewayd private service TCP listening", "addr", addr, "service_address_cidr", api.ServiceAddressCIDR().String())
		if err := proxy.Serve(ctx, listener); err != nil && ctx.Err() == nil {
			errc <- err
		}
	}()
	return nil
}
