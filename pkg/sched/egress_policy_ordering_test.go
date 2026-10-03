// adr: 435 — missing runtime capability cannot satisfy durable convergence.
package sched

import (
	"context"
	"crypto/tls"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Embedding the legacy interface deliberately hides additive capabilities.
type legacyEgressRouter struct{ RoutedVMM }

func TestEgressPolicyUnsupportedRouterRemainsPending(t *testing.T) {
	durable := &durableEgressTestStore{MemStore: state.NewMemStore(), entries: []state.AppEgressPolicyApplyTarget{{AppID: "app", NodeID: "node", Revision: 4}}}
	recorder := &recordingRouterVMM{}
	router := &legacyEgressRouter{RoutedVMM: recorder}
	engine := newEngine(t, durable, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	sub.reconcilePending(t.Context())
	if len(durable.entries) != 1 || len(durable.states) != 1 || durable.states[0].AppliedRevision != 0 || durable.states[0].LastError == "" {
		t.Fatalf("unsupported update acknowledged: pending=%+v states=%+v", durable.entries, durable.states)
	}
	if recorder.snapshotLen() != 0 {
		t.Fatal("subscriber fell back to legacy RPC")
	}
}

type policyRouterClient struct {
	*fakeRouterVMM
	app      string
	revision int64
	cidrs    []netip.Prefix
	ports    []int
}

func (c *policyRouterClient) UpdateAppEgressPolicy(_ context.Context, app string, revision int64, cidrs []netip.Prefix, ports []int) error {
	c.app, c.revision, c.cidrs, c.ports = app, revision, cidrs, ports
	return nil
}

func TestEgressPolicyRouterUsesRevisionedCapability(t *testing.T) {
	client := &policyRouterClient{fakeRouterVMM: &fakeRouterVMM{}}
	router := NewVMMRouter([]ComputeNodeInfo{{ID: "node", TargetURL: "unix://node"}}, func(context.Context, string, *tls.Config) (VMM, error) { return client, nil }, nil)
	if err := router.UpdateAppEgressPolicy(t.Context(), "node", "app", 7, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, []int{5432}); err != nil {
		t.Fatal(err)
	}
	if client.app != "app" || client.revision != 7 || len(client.cidrs) != 1 || len(client.ports) != 1 || client.ports[0] != 5432 {
		t.Fatalf("incomplete routed tuple: %+v", client)
	}
	if err := router.UpdateAppEgressPolicy(t.Context(), "missing", "app", 7, nil, nil); err == nil {
		t.Fatal("unknown node update succeeded")
	}
}

func TestEgressPolicyRouterOldClientFailsClosed(t *testing.T) {
	client := &fakeRouterVMM{}
	router := NewVMMRouter([]ComputeNodeInfo{{ID: "node", TargetURL: "unix://node"}}, func(context.Context, string, *tls.Config) (VMM, error) { return client, nil }, nil)
	if err := router.UpdateAppEgressPolicy(t.Context(), "node", "app", 7, nil, nil); err == nil {
		t.Fatal("old client accepted revisioned policy")
	}
}
