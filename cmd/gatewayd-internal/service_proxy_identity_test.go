// adr: 169
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyCallerResolverMapsLocalLiveInstances(t *testing.T) {
	instances := []state.Instance{
		{ID: "local", AppID: "app-local", NodeID: "node-a", HostIP: "10.100.0.5"},
		{ID: "foreign", AppID: "app-foreign", NodeID: "node-b", HostIP: "10.100.0.6"},
		{ID: "bad", AppID: "app-bad", NodeID: "node-a", HostIP: "not-an-ip"},
	}
	resolver := newServiceProxyCallerResolver(func(context.Context) ([]state.Instance, error) {
		return instances, nil
	}, "node-a")

	for _, tc := range []struct {
		name string
		addr string
		want string
	}{
		{name: "mapped guest", addr: "10.100.0.5:41234", want: "app-local"},
		{name: "foreign node", addr: "10.100.0.6:41234"},
		{name: "unknown guest", addr: "10.100.0.99:41234"},
		{name: "malformed address", addr: "not-an-address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolver(context.Background(), tc.addr)
			if err != nil {
				t.Fatalf("resolve(%q): %v", tc.addr, err)
			}
			if got != tc.want {
				t.Fatalf("resolve(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

func TestServiceProxyCallerResolverRefreshesAndFailsClosed(t *testing.T) {
	now := time.Unix(100, 0)
	var calls int
	resolver := &serviceProxyCallerResolver{
		list: func(context.Context) ([]state.Instance, error) {
			calls++
			return []state.Instance{{AppID: "app-local", NodeID: "node-a", HostIP: "10.100.0.5"}}, nil
		},
		nodeID: "node-a",
		now:    func() time.Time { return now },
		ttl:    time.Second,
		byIP:   make(map[string]string),
	}
	if got, err := resolver.Resolve(context.Background(), "10.100.0.5:1"); err != nil || got != "app-local" {
		t.Fatalf("first resolve = %q, %v; want app-local", got, err)
	}
	if got, err := resolver.Resolve(context.Background(), "10.100.0.5:2"); err != nil || got != "app-local" {
		t.Fatalf("cached resolve = %q, %v; want app-local", got, err)
	}
	if calls != 1 {
		t.Fatalf("list calls = %d, want one cached read", calls)
	}
	now = now.Add(2 * time.Second)
	if got, err := resolver.Resolve(context.Background(), "10.100.0.5:3"); err != nil || got != "app-local" {
		t.Fatalf("refreshed resolve = %q, %v; want app-local", got, err)
	}
	if calls != 2 {
		t.Fatalf("list calls after expiry = %d, want two", calls)
	}

	resolver.list = func(context.Context) ([]state.Instance, error) { return nil, errors.New("db down") }
	now = now.Add(2 * time.Second)
	if _, err := resolver.Resolve(context.Background(), "10.100.0.5:4"); !errors.Is(err, gateway.ErrServiceProxyUnavailable) {
		t.Fatalf("list failure = %v, want ErrServiceProxyUnavailable", err)
	}
}

func TestServiceProxyCallerResolverRejectsAmbiguousHostIP(t *testing.T) {
	resolver := newServiceProxyCallerResolver(func(context.Context) ([]state.Instance, error) {
		return []state.Instance{
			{AppID: "app-a", NodeID: "node-a", HostIP: "10.100.0.5"},
			{AppID: "app-b", NodeID: "node-a", HostIP: "10.100.0.5"},
		}, nil
	}, "node-a")
	got, err := resolver(context.Background(), "10.100.0.5:1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Fatalf("ambiguous host IP resolved to %q, want empty", got)
	}
}

func TestValidateServiceProxyListen(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{addr: "10.100.0.1:10080", want: true},
		{addr: "172.16.4.1:10080", want: true},
		{addr: "0.0.0.0:10080"},
		{addr: "127.0.0.1:10080"},
		{addr: "10.100.0.1:8080"},
		{addr: "10.100.0.1:10080/path"},
		{addr: "[fd00::1]:10080"},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			err := validateServiceProxyListen(tc.addr)
			if (err == nil) != tc.want {
				t.Fatalf("validateServiceProxyListen(%q) error = %v, want valid=%v", tc.addr, err, tc.want)
			}
		})
	}
}
