// adr: 576
package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceTCPTargetResolver(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	create := func(accountID, slug string, mutate func(*state.App)) state.App {
		t.Helper()
		app := state.App{AccountID: accountID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1}
		if mutate != nil {
			mutate(&app)
		}
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("CreateApp(%s): %v", slug, err)
		}
		return created
	}
	acct, err := store.CreateAccount(ctx, "svc-tcp@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	other, err := store.CreateAccount(ctx, "svc-tcp-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	caller := create(acct.ID, "api", nil)
	db := create(acct.ID, "db", func(app *state.App) {
		app.Manifest.Ports = []api.WorkloadPort{
			{Name: "postgres", Port: 5432, Protocol: api.WorkloadPortTCP},
			{Name: "stats", Port: 8125, Protocol: api.WorkloadPortUDP},
		}
	})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: db.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:db", Status: state.DeployPending, OverridePort: 6379})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	paused := create(acct.ID, "paused", func(app *state.App) { app.MaintenanceMode = true })
	foreign := create(other.ID, "foreign", nil)
	resolve := newServiceTCPTargetResolver(store)
	indexOf := func(app state.App) int {
		t.Helper()
		index, err := store.AppServiceAddressIndex(ctx, app.ID)
		if err != nil {
			t.Fatalf("AppServiceAddressIndex(%s): %v", app.Slug, err)
		}
		return index
	}
	address := func(index int) netip.Addr {
		addr, ok := api.ServiceAddressForIndex(index)
		if !ok {
			t.Fatalf("index %d has no address", index)
		}
		return addr
	}

	target, ok, err := resolve(ctx, caller.ID, address(indexOf(db)))
	if err != nil || !ok {
		t.Fatalf("resolve db = %+v, %v, %v", target, ok, err)
	}
	if target.AppID != db.ID || target.AccountID != acct.ID || target.Plan != api.PlanPro {
		t.Fatalf("target = %+v, want app %s in account %s on Pro", target, db.ID, acct.ID)
	}
	if !slices.Equal(target.TCPPorts, []int{5432, 6379}) {
		t.Fatalf("TCPPorts = %v, want the declared TCP listener and the live serving port, not the UDP one", target.TCPPorts)
	}
	if want := time.Duration(api.MustLimitsFor(api.PlanPro).IdleTimeoutS) * time.Second; target.IdleTimeout != want {
		t.Fatalf("IdleTimeout = %v, want the plan idle timeout %v", target.IdleTimeout, want)
	}

	// Both accounts hold index 1; the caller's account decides which app it is.
	if indexOf(foreign) != indexOf(caller) {
		t.Fatalf("test setup: foreign index %d, caller index %d", indexOf(foreign), indexOf(caller))
	}
	if got, ok, err := resolve(ctx, caller.ID, address(indexOf(foreign))); err != nil || !ok || got.AppID == foreign.ID {
		t.Fatalf("an address resolved across accounts: %+v, %v, %v", got, ok, err)
	}

	if _, _, err := resolve(ctx, caller.ID, address(indexOf(paused))); !errors.Is(err, gateway.ErrServiceTCPTargetUnavailable) {
		t.Fatalf("maintenance target: err = %v, want ErrServiceTCPTargetUnavailable", err)
	}
	for name, tc := range map[string]struct {
		caller string
		addr   netip.Addr
	}{
		"unallocated index": {caller.ID, address(99)},
		"outside the block": {caller.ID, netip.MustParseAddr("10.100.0.1")},
		"unknown caller":    {"00000000-0000-4000-8000-000000000001", address(indexOf(db))},
		"malformed caller":  {"not-an-app", address(indexOf(db))},
	} {
		if got, ok, err := resolve(ctx, tc.caller, tc.addr); err != nil || ok {
			t.Fatalf("%s: resolve = %+v, %v, %v; want not found", name, got, ok, err)
		}
	}
}

func TestValidateServiceTCPListen(t *testing.T) {
	const proxy = "10.100.0.1:10081"
	tests := []struct {
		name    string
		addr    string
		proxy   string
		wantErr bool
	}{
		{name: "bridge address and reserved port", addr: "10.100.0.1:10082", proxy: proxy},
		{name: "wrong port", addr: "10.100.0.1:10083", proxy: proxy, wantErr: true},
		{name: "different address than the HTTP proxy", addr: "10.100.0.2:10082", proxy: proxy, wantErr: true},
		{name: "wildcard", addr: "0.0.0.0:10082", proxy: proxy, wantErr: true},
		{name: "no HTTP service proxy", addr: "10.100.0.1:10082", proxy: "", wantErr: true},
		{name: "not host:port", addr: "10.100.0.1", proxy: proxy, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateServiceTCPListen(tt.addr, tt.proxy); (err != nil) != tt.wantErr {
				t.Fatalf("validateServiceTCPListen(%q, %q) err = %v, wantErr %v", tt.addr, tt.proxy, err, tt.wantErr)
			}
		})
	}
}

func TestServiceTCPIdleTimeout(t *testing.T) {
	if got := serviceTCPIdleTimeout(gateway.App{Plan: api.PlanScale, IdleTimeoutS: 45}); got != 45*time.Second {
		t.Fatalf("app override = %v, want 45s", got)
	}
	if got, want := serviceTCPIdleTimeout(gateway.App{Plan: api.PlanFree}), time.Duration(api.MustLimitsFor(api.PlanFree).IdleTimeoutS)*time.Second; got != want {
		t.Fatalf("plan default = %v, want %v", got, want)
	}
	if got := serviceTCPIdleTimeout(gateway.App{Plan: "unknown"}); got != api.StreamingIdleTimeoutDefault {
		t.Fatalf("unknown plan = %v, want the streaming default", got)
	}
}

type serviceTCPTestProvider struct{}

func (serviceTCPTestProvider) ServiceEndpoints(_ context.Context, appID string) (gateway.ServiceEndpointsSnapshot, error) {
	return gateway.ServiceEndpointsSnapshot{AppID: appID}, nil
}

func TestStartServiceTCPProxy(t *testing.T) {
	services := gateway.NewServiceProxy(gateway.ServiceProxyConfig{
		Provider: serviceTCPTestProvider{},
		Authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
			return gateway.ServiceCaller{}, nil
		},
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			return "", "", nil
		},
	})
	store := state.NewMemStore()
	log := testLogger()
	newDeps := func(listen func(string, string) (net.Listener, error)) runDeps {
		return runDeps{listen: listen, metrics: gateway.NewMetrics(), nodeCache: makeNodeCacheForTest(&fakeSubscribe{}, nil, nil)}
	}

	if err := startServiceTCPProxy(context.Background(), runDeps{}, "10.100.0.1:10082", services, store, nil, log); err == nil {
		t.Fatal("started without the vmmd node cache and metrics")
	}
	noIdentity := gateway.NewServiceProxy(gateway.ServiceProxyConfig{
		Provider: serviceTCPTestProvider{},
		Authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
			return gateway.ServiceCaller{}, nil
		},
	})
	if err := startServiceTCPProxy(context.Background(), newDeps(net.Listen), "10.100.0.1:10082", noIdentity, store, nil, log); err == nil {
		t.Fatal("started without source-address caller identity")
	}
	refuse := func(string, string) (net.Listener, error) { return nil, errors.New("address in use") }
	if err := startServiceTCPProxy(context.Background(), newDeps(refuse), "10.100.0.1:10082", services, store, nil, log); err == nil {
		t.Fatal("a listen failure was not reported")
	}

	// The bridge address does not exist on a test host: bind loopback and
	// check that an accepted connection is served and refused.
	var bound net.Listener
	deps := newDeps(func(network, _ string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		bound = ln
		return ln, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	if err := startServiceTCPProxy(ctx, deps, "10.100.0.1:10082", services, store, errc, log); err != nil {
		t.Fatalf("startServiceTCPProxy: %v", err)
	}
	conn, err := net.Dial("tcp", bound.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("a connection without an original destination was not closed: %v", err)
	}
	// Without conntrack the destination lookup fails; with it (a Linux host
	// with nf_conntrack loaded) a direct dial reports its own loopback
	// address. Either way the session is refused before any resolution.
	if got := serviceTCPRejected(t, deps.metrics, "original_destination") + serviceTCPRejected(t, deps.metrics, "not_service_address"); got != 1 {
		t.Fatalf("rejected{original_destination|not_service_address} = %v, want 1", got)
	}
	cancel()
	select {
	case err := <-errc:
		t.Fatalf("cancellation surfaced as a serve error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func serviceTCPRejected(t *testing.T, metrics *gateway.Metrics, reason string) float64 {
	t.Helper()
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "gatewayd_internal_service_tcp_sessions_rejected_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "reason" && label.GetValue() == reason {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}
