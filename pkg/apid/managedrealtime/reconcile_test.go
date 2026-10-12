package managedrealtime

// adr: 281

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodeChannelRouteSnapshotUsesCompactEndpoint(t *testing.T) {
	op := &fakeRealtimeNode{
		connections:   []realtime.ConnectionInfo{{EndpointID: "endpoint", Channels: []string{"connection-snapshot"}}},
		channelRoutes: []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "compact-snapshot"}},
	}

	routes, err := NodeChannelRouteSnapshot(context.Background(), op)
	if err != nil {
		t.Fatalf("nodeChannelRouteSnapshot: %v", err)
	}
	want := []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "compact-snapshot"}}
	if len(routes) != len(want) || routes[0] != want[0] {
		t.Fatalf("routes = %+v, want %+v", routes, want)
	}
	if op.routeReads != 1 || op.connReads != 0 {
		t.Fatalf("snapshot reads = (routes %d, connections %d), want (1, 0)", op.routeReads, op.connReads)
	}
}

func TestNodeChannelRouteSnapshotFallsBackForOlderNode(t *testing.T) {
	op := &fakeRealtimeNode{
		connections:      []realtime.ConnectionInfo{{EndpointID: "endpoint", Channels: []string{"updates"}}},
		channelRoutesErr: &realtime.ManagementError{StatusCode: http.StatusNotFound},
	}

	routes, err := NodeChannelRouteSnapshot(context.Background(), op)
	if err != nil {
		t.Fatalf("nodeChannelRouteSnapshot: %v", err)
	}
	want := []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "updates"}}
	if len(routes) != len(want) || routes[0] != want[0] {
		t.Fatalf("routes = %+v, want fallback %+v", routes, want)
	}
	if op.routeReads != 1 || op.connReads != 1 {
		t.Fatalf("snapshot reads = (routes %d, connections %d), want (1, 1)", op.routeReads, op.connReads)
	}
}

type revisionedRealtimeNode struct {
	*fakeRealtimeNode
	revision realtime.ChannelRouteRevision
}

func (n *revisionedRealtimeNode) ChannelRouteRevision(context.Context) (realtime.ChannelRouteRevision, error) {
	return n.revision, nil
}

func TestReconcileManagedRealtimeChannelRouteRevisionRepairsMissedReport(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("find local compute node: %v", err)
	}
	generation, err := store.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		t.Fatalf("read initial route generation: %v", err)
	}
	if err := store.ReplaceManagedRealtimeChannelRoutesWithRevision(ctx, node.ID, generation, nil,
		&state.ManagedRealtimeChannelRouteSnapshotRevision{InstanceID: "realtimed-process", Revision: 0}); err != nil {
		t.Fatalf("store initial empty snapshot: %v", err)
	}
	local := &revisionedRealtimeNode{
		fakeRealtimeNode: &fakeRealtimeNode{channelRoutes: []realtime.ChannelRoute{{EndpointID: "endpoint", Channel: "updates"}}},
		revision:         realtime.ChannelRouteRevision{InstanceID: "realtimed-process", Revision: 1},
	}
	owner := NewLeasedRealtimeOwner(store, store, node.ID, local, slog.New(slog.DiscardHandler))
	owner.channelRoutes = store
	owner.channelRouteMetrics = NewManagedRealtimeChannelRouteMetrics(prometheus.NewRegistry(), "apid")
	owner.clientFor = func(state.ComputeNode) (RealtimeNodeOperator, error) { return local, nil }

	if err := ReconcileManagedRealtimeChannelRouteRevisions(ctx, owner, []state.ComputeNode{node}); err != nil {
		t.Fatalf("reconcile changed route revision: %v", err)
	}
	view, err := store.ListManagedRealtimeChannelRouteView(ctx, "endpoint", "updates")
	if err != nil {
		t.Fatalf("list repaired route view: %v", err)
	}
	if len(view.NodeIDs) != 1 || view.NodeIDs[0] != node.ID {
		t.Fatalf("repaired route nodes = %v, want [%s]", view.NodeIDs, node.ID)
	}
	revisions, err := store.ListManagedRealtimeChannelRouteSnapshotRevisions(ctx, []string{node.ID})
	if err != nil {
		t.Fatalf("list repaired snapshot revision: %v", err)
	}
	if got := revisions[node.ID]; got.InstanceID != "realtimed-process" || got.Revision != 1 {
		t.Fatalf("repaired snapshot revision = %+v, want process revision 1", got)
	}
}
