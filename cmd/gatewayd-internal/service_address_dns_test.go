// adr: 576
package main

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type serviceAddressDNSFixture struct {
	t     *testing.T
	ctx   context.Context
	store *state.MemStore
	node  state.ComputeNode
}

func newServiceAddressDNSFixture(t *testing.T) *serviceAddressDNSFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "svc-dns.faas", TargetURL: "unix:///tmp/vmmd.sock", VPCPUs: 2, MemMB: 4096,
		MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 2, Lifecycle: state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	return &serviceAddressDNSFixture{t: t, ctx: ctx, store: store, node: node}
}

func (f *serviceAddressDNSFixture) app(email, slug string) state.App {
	f.t.Helper()
	acct, err := f.store.AccountByEmail(f.ctx, email)
	if err != nil {
		acct, err = f.store.CreateAccount(f.ctx, email, api.PlanPro)
		if err != nil {
			f.t.Fatalf("CreateAccount: %v", err)
		}
	}
	app, err := f.store.CreateApp(f.ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		f.t.Fatalf("CreateApp(%s): %v", slug, err)
	}
	return app
}

// start runs an instance of app behind hostIP on the fixture node.
func (f *serviceAddressDNSFixture) start(app state.App, hostIP string) state.Instance {
	f.t.Helper()
	instance, err := f.store.CreateInstance(f.ctx, app.ID, "", string(state.StateColdBooting), 128, f.node.ID, "")
	if err != nil {
		f.t.Fatalf("CreateInstance: %v", err)
	}
	published, err := f.store.PublishInstanceRuntime(f.ctx, instance.ID, string(state.StateColdBooting), "fc-"+hostIP, hostIP, 20001)
	if err != nil {
		f.t.Fatalf("PublishInstanceRuntime: %v", err)
	}
	return published
}

func (f *serviceAddressDNSFixture) index(app state.App) int {
	f.t.Helper()
	index, err := f.store.AppServiceAddressIndex(f.ctx, app.ID)
	if err != nil {
		f.t.Fatalf("AppServiceAddressIndex(%s): %v", app.Slug, err)
	}
	return index
}

func (f *serviceAddressDNSFixture) ready() {
	f.t.Helper()
	if _, err := f.store.SetComputeNodeServiceAddressReady(f.ctx, f.node.ID, true); err != nil {
		f.t.Fatalf("SetComputeNodeServiceAddressReady: %v", err)
	}
}

func TestServiceAddressLookup(t *testing.T) {
	f := newServiceAddressDNSFixture(t)
	api1 := f.app("a@example.com", "api-a")
	db := f.app("a@example.com", "db-a")
	f.app("b@example.com", "billing-b")
	lookup := newServiceAddressLookup(f.store, f.node.Name, slog.Default())
	want, _ := api.ServiceAddressForIndex(f.index(db))

	// An instance whose namespace predates readiness keeps the bridge.
	f.start(api1, "10.100.0.21")
	f.ready()
	if addr, ok := lookup(f.ctx, "10.100.0.21:5353", "db-a"); ok {
		t.Fatalf("pre-readiness instance was handed %s", addr)
	}

	f.start(api1, "10.100.0.22")
	if addr, ok := lookup(f.ctx, "10.100.0.22:5353", "db-a"); !ok || addr != want {
		t.Fatalf("lookup(db-a) = %s, %v; want %s", addr, ok, want)
	}
	for name, tc := range map[string]struct{ remote, service string }{
		"another account's service": {"10.100.0.22:5353", "billing-b"},
		"unknown service":           {"10.100.0.22:5353", "nope"},
		"unknown source":            {"10.100.0.99:5353", "db-a"},
		"not an address":            {"bridge", "db-a"},
	} {
		if addr, ok := lookup(f.ctx, tc.remote, tc.service); ok {
			t.Fatalf("%s: handed %s", name, addr)
		}
	}
}

// Host addresses are recycled when instances die. The answer must follow
// the instance now behind the address, never a cached answer for the last.
func TestServiceAddressLookupFollowsRecycledHostIP(t *testing.T) {
	f := newServiceAddressDNSFixture(t)
	f.ready()
	callerA := f.app("a@example.com", "api-a")
	dbA := f.app("a@example.com", "db-a")
	callerB := f.app("b@example.com", "api-b")
	lookup := newServiceAddressLookup(f.store, f.node.Name, slog.Default())

	first := f.start(callerA, "10.100.0.30")
	if _, ok := lookup(f.ctx, "10.100.0.30:5353", "db-a"); !ok {
		t.Fatal("account A caller was not answered")
	}
	if err := f.store.UpdateInstanceState(f.ctx, first.ID, string(state.StateStopped)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	f.start(callerB, "10.100.0.30")
	if addr, ok := lookup(f.ctx, "10.100.0.30:5353", "db-a"); ok {
		want, _ := api.ServiceAddressForIndex(f.index(dbA))
		t.Fatalf("account B caller on a recycled address was handed %s (account A's db is %s)", addr, want)
	}
}

func TestServiceAddressCacheExpires(t *testing.T) {
	addr := netip.MustParseAddr("198.19.0.1")
	at := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	cache := &serviceAddressCache{now: func() time.Time { return at }, entries: map[serviceAddressCacheKey]serviceAddressCacheEntry{}}
	key := serviceAddressCacheKey{callerAppID: "app", service: "db"}
	cache.put(key, addr, true)
	if got, ok, hit := cache.get(key); !hit || !ok || got != addr {
		t.Fatalf("fresh entry = %s, %v, %v", got, ok, hit)
	}
	at = at.Add(serviceAddressLookupTTL)
	if _, _, hit := cache.get(key); hit {
		t.Fatal("entry outlived the DNS TTL")
	}
}
