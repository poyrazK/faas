// adr: 530
package main

import (
	"context"
	"errors"
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
	address := func(index int) netip.Addr {
		addr, ok := api.ServiceAddressForIndex(index)
		if !ok {
			t.Fatalf("index %d has no address", index)
		}
		return addr
	}

	target, ok, err := resolve(ctx, caller.ID, address(db.ServiceAddressIndex))
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
	if foreign.ServiceAddressIndex != caller.ServiceAddressIndex {
		t.Fatalf("test setup: foreign index %d, caller index %d", foreign.ServiceAddressIndex, caller.ServiceAddressIndex)
	}
	if got, ok, err := resolve(ctx, caller.ID, address(foreign.ServiceAddressIndex)); err != nil || !ok || got.AppID == foreign.ID {
		t.Fatalf("an address resolved across accounts: %+v, %v, %v", got, ok, err)
	}

	if _, _, err := resolve(ctx, caller.ID, address(paused.ServiceAddressIndex)); !errors.Is(err, gateway.ErrServiceTCPTargetUnavailable) {
		t.Fatalf("maintenance target: err = %v, want ErrServiceTCPTargetUnavailable", err)
	}
	for name, tc := range map[string]struct {
		caller string
		addr   netip.Addr
	}{
		"unallocated index": {caller.ID, address(99)},
		"outside the block": {caller.ID, netip.MustParseAddr("10.100.0.1")},
		"unknown caller":    {"00000000-0000-4000-8000-000000000001", address(db.ServiceAddressIndex)},
		"malformed caller":  {"not-an-app", address(db.ServiceAddressIndex)},
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
