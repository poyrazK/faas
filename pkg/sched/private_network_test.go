// adr: 164

package sched

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type privateNetworkRouteCall struct {
	nodeID string
	appID  string
	cidrs  []netip.Prefix
}

type privateNetworkRouterFake struct {
	mu        sync.Mutex
	calls     []privateNetworkRouteCall
	errByNode map[string]error
}

type privateNetworkPeeringSweeperFake struct {
	accountID string
	region    string
	err       error
}

func (f *privateNetworkPeeringSweeperFake) SweepAccountRegion(_ context.Context, accountID, region string) (privatenetwork.PeeringReconcileSummary, error) {
	f.accountID = accountID
	f.region = region
	return privatenetwork.PeeringReconcileSummary{}, f.err
}

type privateNetworkFabricCall struct {
	nodeID    string
	accountID string
	networkID string
	region    string
	cidr      netip.Prefix
}

type privateNetworkFabricRouterFake struct {
	mu    sync.Mutex
	calls []privateNetworkFabricCall
}

type privateNetworkFabricTransportCall struct {
	nodeID    string
	accountID string
	networkID string
	region    string
	cidr      netip.Prefix
	peers     []netip.Addr
}

type privateNetworkFabricTransportRouterFake struct {
	privateNetworkFabricRouterFake
	muTransport sync.Mutex
	transport   []privateNetworkFabricTransportCall
}

func (f *privateNetworkFabricRouterFake) ReconcilePrivateNetworkFabric(_ context.Context, nodeID, accountID, networkID, region string, cidr netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, privateNetworkFabricCall{
		nodeID: nodeID, accountID: accountID, networkID: networkID,
		region: region, cidr: cidr,
	})
	return nil
}

func (f *privateNetworkFabricRouterFake) callsSnapshot() []privateNetworkFabricCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]privateNetworkFabricCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *privateNetworkFabricTransportRouterFake) ReconcilePrivateNetworkFabricWithPeers(_ context.Context, nodeID, accountID, networkID, region string, cidr netip.Prefix, peers []netip.Addr) error {
	f.muTransport.Lock()
	defer f.muTransport.Unlock()
	f.transport = append(f.transport, privateNetworkFabricTransportCall{
		nodeID: nodeID, accountID: accountID, networkID: networkID,
		region: region, cidr: cidr, peers: append([]netip.Addr(nil), peers...),
	})
	return nil
}

func (f *privateNetworkFabricTransportRouterFake) transportCallsSnapshot() []privateNetworkFabricTransportCall {
	f.muTransport.Lock()
	defer f.muTransport.Unlock()
	out := make([]privateNetworkFabricTransportCall, len(f.transport))
	copy(out, f.transport)
	return out
}

func (f *privateNetworkRouterFake) UpdatePrivateNetwork(_ context.Context, nodeID, appID string, cidrs []netip.Prefix) error {
	f.mu.Lock()
	f.calls = append(f.calls, privateNetworkRouteCall{
		nodeID: nodeID,
		appID:  appID,
		cidrs:  append([]netip.Prefix(nil), cidrs...),
	})
	err := f.errByNode[nodeID]
	f.mu.Unlock()
	return err
}

func (f *privateNetworkRouterFake) callsSnapshot() []privateNetworkRouteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]privateNetworkRouteCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func newPrivateNetworkInstanceStore(t *testing.T, appID string, rows ...struct{ state, node string }) *state.MemStore {
	t.Helper()
	store := state.NewMemStore()
	for _, row := range rows {
		if _, err := store.CreateInstance(context.Background(), appID, "dep-1", row.state, 256, row.node, ""); err != nil {
			t.Fatalf("CreateInstance(%q, %q): %v", row.state, row.node, err)
		}
	}
	return store
}

func TestPrivateNetworkRouteApplierFansOutLiveNodesOnce(t *testing.T) {
	const appID = "app-private"
	store := newPrivateNetworkInstanceStore(t, appID,
		struct{ state, node string }{string(state.StateRunning), "node-a"},
		struct{ state, node string }{string(state.StateWaking), "node-a"},
		struct{ state, node string }{string(state.StateColdBooting), "node-b"},
		struct{ state, node string }{string(state.StateSnapshotting), "node-c"},
		struct{ state, node string }{string(state.StateParked), "node-parked"},
		struct{ state, node string }{string(state.StateStopped), "node-stopped"},
		struct{ state, node string }{string(state.StateRunning), ""},
	)
	router := &privateNetworkRouterFake{errByNode: map[string]error{}}
	applier := NewPrivateNetworkRouteApplier(store, router, nil)
	wantCIDR := netip.MustParsePrefix("10.42.0.0/16")

	if err := applier.Apply(context.Background(), appID, []netip.Prefix{wantCIDR}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	calls := router.callsSnapshot()
	gotNodes := make([]string, 0, len(calls))
	for _, call := range calls {
		gotNodes = append(gotNodes, call.nodeID)
		if call.appID != appID {
			t.Errorf("call appID = %q, want %q", call.appID, appID)
		}
		if len(call.cidrs) != 1 || call.cidrs[0] != wantCIDR {
			t.Errorf("call CIDRs = %v, want [%s]", call.cidrs, wantCIDR)
		}
	}
	sort.Strings(gotNodes)
	if want := []string{"node-a", "node-b", "node-c"}; !equalStrings(gotNodes, want) {
		t.Fatalf("updated nodes = %v, want %v", gotNodes, want)
	}
}

func TestPrivateNetworkRouteApplierAttemptsEveryLiveNodeAfterFailure(t *testing.T) {
	const appID = "app-private-failure"
	store := newPrivateNetworkInstanceStore(t, appID,
		struct{ state, node string }{string(state.StateRunning), "node-a"},
		struct{ state, node string }{string(state.StateRunning), "node-b"},
		struct{ state, node string }{string(state.StateRunning), "node-b"},
	)
	wantErr := errors.New("node-a route update failed")
	router := &privateNetworkRouterFake{errByNode: map[string]error{"node-a": wantErr}}
	applier := NewPrivateNetworkRouteApplier(store, router, nil)

	err := applier.Apply(context.Background(), appID, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Apply error = %v, want %v", err, wantErr)
	}
	calls := router.callsSnapshot()
	gotNodes := make([]string, 0, len(calls))
	for _, call := range calls {
		gotNodes = append(gotNodes, call.nodeID)
	}
	sort.Strings(gotNodes)
	if want := []string{"node-a", "node-b"}; !equalStrings(gotNodes, want) {
		t.Fatalf("updated nodes after partial failure = %v, want %v", gotNodes, want)
	}
}

func TestPrivateNetworkRouteApplierReportsPerNodeHealth(t *testing.T) {
	const appID = "app-private-report"
	store := newPrivateNetworkInstanceStore(t, appID,
		struct{ state, node string }{string(state.StateRunning), "node-b"},
		struct{ state, node string }{string(state.StateRunning), "node-a"},
		struct{ state, node string }{string(state.StateRunning), "node-a"},
	)
	wantErr := errors.New("vmmd unavailable")
	router := &privateNetworkRouterFake{errByNode: map[string]error{"node-b": wantErr}}
	applier := NewPrivateNetworkRouteApplier(store, router, nil)

	report, err := applier.ApplyWithReport(context.Background(), appID, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ApplyWithReport error = %v, want %v", err, wantErr)
	}
	if len(report.Nodes) != 2 {
		t.Fatalf("report nodes = %+v, want one entry per live node", report.Nodes)
	}
	if report.Nodes[0].NodeID != "node-a" || report.Nodes[0].Status != api.PrivateNetworkAttachmentStatusReady {
		t.Fatalf("node-a report = %+v, want ready", report.Nodes[0])
	}
	if report.Nodes[1].NodeID != "node-b" || report.Nodes[1].Status != api.PrivateNetworkAttachmentStatusError {
		t.Fatalf("node-b report = %+v, want error", report.Nodes[1])
	}
	if report.Nodes[1].Detail != wantErr.Error() {
		t.Fatalf("node-b detail = %q, want %q", report.Nodes[1].Detail, wantErr)
	}
}

func TestPrivateNetworkPeeringRouteApplierReplacesDerivedRoutes(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "")
	ctx := context.Background()
	const (
		accountID = "acct-private"
		region    = "fra1"
		appID     = "app-private-peering"
	)
	baseCIDR := netip.MustParsePrefix("10.42.0.0/16")
	peerCIDR := netip.MustParsePrefix("10.84.0.0/16")
	store := newPrivateNetworkInstanceStore(t, appID, struct{ state, node string }{string(state.StateRunning), "node-a"})
	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: accountID, AppID: appID, NetworkID: "net-left", Region: region,
		CIDRs: []netip.Prefix{baseCIDR}, Status: api.PrivateNetworkAttachmentStatusReady,
	}); err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	router := &privateNetworkRouterFake{errByNode: map[string]error{}}
	applier := NewPrivateNetworkPeeringRouteApplier(store, router, nil)
	routes := []privatenetwork.PeeringRoute{{
		FromNetworkID: "net-left", DestinationNetwork: "net-right", DestinationCIDR: peerCIDR,
	}}

	if err := applier.Apply(ctx, accountID, region, routes); err != nil {
		t.Fatalf("Apply peering routes: %v", err)
	}
	if err := applier.Apply(ctx, accountID, region, nil); err != nil {
		t.Fatalf("Apply empty peering route set: %v", err)
	}

	calls := router.callsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("route calls = %d, want 2: %+v", len(calls), calls)
	}
	if got, want := calls[0].cidrs, []netip.Prefix{baseCIDR, peerCIDR}; !equalPrefixes(got, want) {
		t.Fatalf("effective CIDRs = %v, want %v", got, want)
	}
	if got, want := calls[1].cidrs, []netip.Prefix{baseCIDR}; !equalPrefixes(got, want) {
		t.Fatalf("stale-cleanup CIDRs = %v, want %v", got, want)
	}
}

func TestPrivateNetworkRouteApplierRetainsReadyPeeringRoutesOnAttachmentReplay(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "")
	ctx := context.Background()
	const accountID = "acct-private"
	const region = "fra1"
	baseCIDR := netip.MustParsePrefix("10.42.0.0/16")
	peerCIDR := netip.MustParsePrefix("10.84.0.0/16")
	store := newPrivateNetworkInstanceStore(t, "app-private-peering-replay", struct{ state, node string }{string(state.StateRunning), "node-a"})
	for _, network := range []state.PrivateNetwork{
		{ID: "net-left", AccountID: accountID, Name: "left", Region: region, CIDR: baseCIDR},
		{ID: "net-right", AccountID: accountID, Name: "right", Region: region, CIDR: peerCIDR},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	peering, err := store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{
		ID: "peer-ready", AccountID: accountID, LeftNetworkID: "net-left", RightNetworkID: "net-right", Region: region,
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering: %v", err)
	}
	if _, err := store.UpdatePrivateNetworkPeeringStatus(ctx, accountID, peering.ID, api.PrivateNetworkPeeringStatusReady, "routes active"); err != nil {
		t.Fatalf("UpdatePrivateNetworkPeeringStatus: %v", err)
	}
	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: accountID, AppID: "app-private-peering-replay", NetworkID: "net-left", Region: region,
		CIDRs: []netip.Prefix{baseCIDR}, Status: api.PrivateNetworkAttachmentStatusReady,
	}); err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	router := &privateNetworkRouterFake{errByNode: map[string]error{}}
	applier := NewPrivateNetworkRouteApplier(store, router, nil).WithPeeringStore(store)
	if _, err := applier.ApplyAttachmentWithReport(ctx, state.AppPrivateNetworkAttachment{
		AccountID: accountID, AppID: "app-private-peering-replay", NetworkID: "net-left", Region: region,
		CIDRs: []netip.Prefix{baseCIDR}, Status: api.PrivateNetworkAttachmentStatusReady,
	}); err != nil {
		t.Fatalf("ApplyAttachmentWithReport: %v", err)
	}
	calls := router.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("route calls = %d, want 1: %+v", len(calls), calls)
	}
	if got, want := calls[0].cidrs, []netip.Prefix{baseCIDR, peerCIDR}; !equalPrefixes(got, want) {
		t.Fatalf("replayed CIDRs = %v, want %v", got, want)
	}
}

func TestPrivateNetworkFabricApplierPreparesActiveRegionalNodesWithoutLiveInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	region := "fra1"
	otherRegion := "hel1"
	for _, node := range []state.ComputeNode{
		{ID: "node-fra", Name: "node-fra", TargetURL: "tcp://10.42.0.2:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region},
		{ID: "node-hel", Name: "node-hel", TargetURL: "tcp://10.42.0.3:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &otherRegion},
		{ID: "node-legacy", Name: "node-legacy", TargetURL: "tcp://10.42.0.4:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true},
	} {
		if _, err := store.CreateComputeNode(ctx, node); err != nil {
			t.Fatalf("CreateComputeNode(%s): %v", node.Name, err)
		}
	}

	router := &privateNetworkFabricRouterFake{}
	applier := NewPrivateNetworkFabricApplier(store, router, nil)
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	_, err := applier.ApplyWithReport(ctx, state.AppPrivateNetworkAttachment{
		AccountID: "acct-private", AppID: "app-private", NetworkID: "net-private",
		Region: region, CIDRs: []netip.Prefix{cidr}, Status: api.PrivateNetworkAttachmentStatusReady,
	})
	if err != nil {
		t.Fatalf("ApplyWithReport: %v", err)
	}

	calls := router.callsSnapshot()
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		got = append(got, call.nodeID)
		if call.accountID != "acct-private" || call.networkID != "net-private" || call.region != region || call.cidr != cidr {
			t.Errorf("fabric call = %+v, want attachment identity and CIDR", call)
		}
	}
	sort.Strings(got)
	// node-legacy has no region metadata and remains eligible during fleet
	// rollout; node-hel and the single-box local node are excluded by region.
	if want := []string{"node-fra", "node-legacy"}; !equalStrings(got, want) {
		t.Fatalf("fabric nodes = %v, want %v", got, want)
	}
}

func TestRegionalTransportPeersRequiresCompleteRegionalRoster(t *testing.T) {
	region := "fra1"
	other := "hel1"
	addrA := netip.MustParseAddr("100.64.0.10")
	addrB := netip.MustParseAddr("100.64.0.11")
	nodes := []state.ComputeNode{
		{ID: "node-a", Active: true, Region: &region, OverlayIP: &addrA},
		{ID: "node-b", Active: true, Region: &region, OverlayIP: &addrB},
		{ID: "node-hel", Active: true, Region: &other},
	}
	peers, ready := regionalTransportPeers(nodes, nodes[0], region)
	if !ready || len(peers) != 1 || peers[0] != addrB {
		t.Fatalf("regional peers = %v, ready=%v; want [%s], true", peers, ready, addrB)
	}
	nodes[1].OverlayIP = nil
	if _, ready := regionalTransportPeers(nodes, nodes[0], region); ready {
		t.Fatal("incomplete regional roster reported ready")
	}
}

func TestPrivateNetworkFabricApplierUsesAuthoritativeTransportRoster(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	region := "fra1"
	addrA := netip.MustParseAddr("100.64.0.10")
	addrB := netip.MustParseAddr("100.64.0.11")
	for _, node := range []state.ComputeNode{
		{ID: "node-a", Name: "node-a", TargetURL: "tcp://100.64.0.10:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region, OverlayIP: &addrA},
		{ID: "node-b", Name: "node-b", TargetURL: "tcp://100.64.0.11:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region, OverlayIP: &addrB},
	} {
		if _, err := store.CreateComputeNode(ctx, node); err != nil {
			t.Fatalf("CreateComputeNode(%s): %v", node.Name, err)
		}
	}
	router := &privateNetworkFabricTransportRouterFake{}
	applier := NewPrivateNetworkFabricApplier(store, router, nil)
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	if _, err := applier.ApplyWithReport(ctx, state.AppPrivateNetworkAttachment{
		AccountID: "acct-private", AppID: "app-private", NetworkID: "net-private",
		Region: region, CIDRs: []netip.Prefix{cidr}, Status: api.PrivateNetworkAttachmentStatusReady,
	}); err != nil {
		t.Fatalf("ApplyWithReport: %v", err)
	}
	calls := router.transportCallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("transport calls = %d, want 2: %+v", len(calls), calls)
	}
	for _, call := range calls {
		if len(call.peers) != 1 {
			t.Fatalf("transport call = %+v, want one peer", call)
		}
		want := addrB
		if call.nodeID == "node-b" {
			want = addrA
		}
		if call.peers[0] != want {
			t.Errorf("node %s peers = %v, want [%s]", call.nodeID, call.peers, want)
		}
	}
}

func TestPrivateNetworkAttachmentSubscriberClearsDetachedRoutes(t *testing.T) {
	const appID = "app-private-detach"
	store := newPrivateNetworkInstanceStore(t, appID,
		struct{ state, node string }{string(state.StateRunning), "node-a"},
		struct{ state, node string }{string(state.StateRunning), "node-b"},
	)
	router := &privateNetworkRouterFake{errByNode: map[string]error{}}
	subscriber := NewPrivateNetworkAttachmentSubscriber(
		NewPrivateNetworkRouteApplier(store, router, nil), nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := make(chan db.Notification, 1)
	done := make(chan error, 1)
	go func() { done <- subscriber.Run(ctx, feed) }()

	feed <- db.Notification{
		Channel: db.NotifyPrivateNetworkAttachmentChanged,
		Payload: `{"kind":"private_network_attachment","app_id":"` + appID + `","status":"detached"}`,
	}
	deadline := time.After(time.Second)
	for len(router.callsSnapshot()) < 2 {
		select {
		case <-deadline:
			t.Fatalf("detach cleanup calls = %d, want 2", len(router.callsSnapshot()))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("subscriber exit = %v, want context.Canceled", err)
	}
	for _, call := range router.callsSnapshot() {
		if len(call.cidrs) != 0 {
			t.Errorf("detach call for %s carried CIDRs %v, want empty cleanup set", call.nodeID, call.cidrs)
		}
	}
}

func TestPrivateNetworkPeeringSubscriberSweepsAffectedRegion(t *testing.T) {
	sweeper := &privateNetworkPeeringSweeperFake{}
	subscriber := NewPrivateNetworkPeeringSubscriber(sweeper, nil)
	err := subscriber.Handle(context.Background(), db.Notification{
		Channel: db.NotifyPrivateNetworkChanged,
		Payload: `{"kind":"private_network_peering","account_id":"acct-1","region":"fra1","status":"deleted"}`,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if sweeper.accountID != "acct-1" || sweeper.region != "fra1" {
		t.Fatalf("sweep target = %q/%q, want acct-1/fra1", sweeper.accountID, sweeper.region)
	}
}

func TestPrivateNetworkPeeringSubscriberRejectsIncompletePayload(t *testing.T) {
	subscriber := NewPrivateNetworkPeeringSubscriber(&privateNetworkPeeringSweeperFake{}, nil)
	if err := subscriber.Handle(context.Background(), db.Notification{
		Channel: db.NotifyPrivateNetworkChanged,
		Payload: `{"kind":"private_network_peering","account_id":"acct-1","status":"deleted"}`,
	}); err == nil {
		t.Fatal("Handle succeeded for payload without region")
	}
}

func TestPrivateNetworkAttachmentSubscriberReturnsCleanupFailureForReplay(t *testing.T) {
	const appID = "app-private-replay"
	store := newPrivateNetworkInstanceStore(t, appID,
		struct{ state, node string }{string(state.StateRunning), "node-a"},
	)
	wantErr := errors.New("vmmd unavailable")
	router := &privateNetworkRouterFake{errByNode: map[string]error{"node-a": wantErr}}
	subscriber := NewPrivateNetworkAttachmentSubscriber(
		NewPrivateNetworkRouteApplier(store, router, nil), nil,
	)

	err := subscriber.Handle(context.Background(), db.Notification{
		Channel: db.NotifyPrivateNetworkAttachmentChanged,
		Payload: `{"kind":"private_network_attachment","app_id":"` + appID + `","status":"detached"}`,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle error = %v, want %v so outbox can retry", err, wantErr)
	}
}

func TestPrivateNetworkCIDRsForFailsClosedUntilReady(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	engine := &Engine{store: store}
	app := state.App{ID: "app-private-projection", AccountID: "acct-private"}
	want := []string{"10.42.0.0/16"}

	for _, status := range []string{
		api.PrivateNetworkAttachmentStatusPending,
		api.PrivateNetworkAttachmentStatusError,
	} {
		if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
			AccountID: app.AccountID, AppID: app.ID, NetworkID: "vpc-1", Region: "fra1",
			CIDRs: []netip.Prefix{netip.MustParsePrefix(want[0])}, Status: status,
		}); err != nil {
			t.Fatalf("upsert %s: %v", status, err)
		}
		if got := engine.privateNetworkCIDRsFor(ctx, app); got != nil {
			t.Fatalf("status %s projected CIDRs = %v, want nil", status, got)
		}
	}

	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: app.AccountID, AppID: app.ID, NetworkID: "vpc-1", Region: "fra1",
		CIDRs: []netip.Prefix{netip.MustParsePrefix(want[0])}, Status: api.PrivateNetworkAttachmentStatusReady,
	}); err != nil {
		t.Fatal(err)
	}
	if got := engine.privateNetworkCIDRsFor(ctx, app); !equalStrings(got, want) {
		t.Fatalf("ready projected CIDRs = %v, want %v", got, want)
	}

	if err := store.DeleteAppPrivateNetworkAttachment(ctx, app.AccountID, app.ID); err != nil {
		t.Fatal(err)
	}
	if got := engine.privateNetworkCIDRsFor(ctx, app); got != nil {
		t.Fatalf("missing attachment projected CIDRs = %v, want nil", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalPrefixes(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
