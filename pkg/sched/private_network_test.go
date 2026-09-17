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
