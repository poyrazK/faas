package main

// adr: 281

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeRealtimeNode struct {
	connections []realtime.ConnectionInfo
	endpoints   []string
	sends       int
	closes      int
	subs        int
	pubs        int
	registered  int
	removed     int
	connReads   int
	connErr     error
	publishErr  error
}

func (f *fakeRealtimeNode) Send(context.Context, string, string, realtime.Message) error {
	f.sends++
	return nil
}
func (f *fakeRealtimeNode) CloseConnection(context.Context, string, string, string) error {
	f.closes++
	return nil
}
func (f *fakeRealtimeNode) Subscribe(context.Context, string, string, string) error {
	f.subs++
	return nil
}
func (f *fakeRealtimeNode) Unsubscribe(context.Context, string, string, string) error {
	f.subs++
	return nil
}
func (f *fakeRealtimeNode) Publish(context.Context, string, string, realtime.Message) (int, error) {
	f.pubs++
	if f.publishErr != nil {
		return 0, f.publishErr
	}
	return 1, nil
}
func (f *fakeRealtimeNode) Connections(context.Context) ([]realtime.ConnectionInfo, error) {
	f.connReads++
	if f.connErr != nil {
		return nil, f.connErr
	}
	return append([]realtime.ConnectionInfo(nil), f.connections...), nil
}
func (f *fakeRealtimeNode) Endpoints(context.Context) ([]string, error) {
	if f.connErr != nil {
		return nil, f.connErr
	}
	return append([]string(nil), f.endpoints...), nil
}
func (f *fakeRealtimeNode) RegisterEndpoint(context.Context, realtime.Endpoint) error {
	f.registered++
	return nil
}
func (f *fakeRealtimeNode) RemoveEndpoint(context.Context, string) error { f.removed++; return nil }

type publishingRealtimeNode struct {
	*fakeRealtimeNode
	publish func(context.Context) (int, error)
}

func (f *publishingRealtimeNode) Publish(ctx context.Context, _, _ string, _ realtime.Message) (int, error) {
	return f.publish(ctx)
}

func TestLeasedRealtimeOwnerDiscoversAndReusesConnectionLease(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	nodeA, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-a", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	fakeA := &fakeRealtimeNode{}
	fakeB := &fakeRealtimeNode{connections: []realtime.ConnectionInfo{{ID: "conn-1", EndpointID: "endpoint-1"}}}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.leaseTTL = time.Minute
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		switch node.ID {
		case nodeA.ID:
			return fakeA, nil
		case nodeB.ID:
			return fakeB, nil
		default:
			return nil, errors.New("unknown node")
		}
	}
	if err := owner.Send(ctx, "endpoint-1", "conn-1", realtime.Message{Data: []byte("hello")}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if fakeB.sends != 1 || fakeA.sends != 0 {
		t.Fatalf("send counts: node-a=%d node-b=%d", fakeA.sends, fakeB.sends)
	}
	reads := fakeB.connReads
	if err := owner.Send(ctx, "endpoint-1", "conn-1", realtime.Message{Data: []byte("again")}); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if fakeB.sends != 2 || fakeB.connReads != reads {
		t.Fatalf("lease was not reused: sends=%d reads=%d want reads=%d", fakeB.sends, fakeB.connReads, reads)
	}
	if err := owner.CloseConnection(ctx, "endpoint-1", "conn-1", "done"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if fakeB.closes != 1 {
		t.Fatalf("close count=%d, want 1", fakeB.closes)
	}
	if _, err := store.GetManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("owner after close=%v, want not found", err)
	}
}

func TestLeasedRealtimeOwnerBroadcastsPublishAndEndpoint(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	nodes := make([]state.ComputeNode, 0, 2)
	fakes := make([]*fakeRealtimeNode, 0, 2)
	for _, name := range []string{"node-a", "node-b"} {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: name, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
		fakes = append(fakes, &fakeRealtimeNode{})
	}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		for i := range nodes {
			if nodes[i].ID == node.ID {
				return fakes[i], nil
			}
		}
		return nil, errors.New("unknown node")
	}
	if err := owner.RegisterEndpoint(ctx, realtime.Endpoint{ID: "endpoint-1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	queued, err := owner.Publish(ctx, "endpoint-1", "updates", realtime.Message{Data: []byte("hello")})
	if err != nil || queued != 2 {
		t.Fatalf("publish queued=%d err=%v, want 2/nil", queued, err)
	}
	for i, fake := range fakes {
		if fake.registered != 1 || fake.pubs != 1 {
			t.Errorf("node %d registered=%d pubs=%d", i, fake.registered, fake.pubs)
		}
	}
}

func TestLeasedRealtimeOwnerPublishHasBoundedConcurrency(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	const nodeCount = maxConcurrentRealtimePublishes*2 + 1
	started := make(chan struct{}, nodeCount)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var active, peak atomic.Int32
	operators := make(map[string]realtimeNodeOperator, nodeCount)
	for i := range nodeCount {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("node-%d", i), Active: true})
		if err != nil {
			t.Fatal(err)
		}
		operators[node.ID] = &publishingRealtimeNode{
			fakeRealtimeNode: &fakeRealtimeNode{},
			publish: func(ctx context.Context) (int, error) {
				current := active.Add(1)
				defer active.Add(-1)
				for {
					previous := peak.Load()
					if current <= previous || peak.CompareAndSwap(previous, current) {
						break
					}
				}
				started <- struct{}{}
				select {
				case <-release:
					return 1, nil
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			},
		}
	}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		if operator := operators[node.ID]; operator != nil {
			return operator, nil
		}
		return nil, errors.New("unknown node")
	}
	type publishOutcome struct {
		queued int
		err    error
	}
	done := make(chan publishOutcome, 1)
	go func() {
		queued, err := owner.Publish(ctx, "endpoint-1", "updates", realtime.Message{})
		done <- publishOutcome{queued, err}
	}()
	for range maxConcurrentRealtimePublishes {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("publish did not start concurrent node requests")
		}
	}
	close(release)
	select {
	case result := <-done:
		if result.err != nil || result.queued != nodeCount {
			t.Fatalf("publish queued=%d err=%v, want %d/nil", result.queued, result.err, nodeCount)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publish did not finish")
	}
	if got := peak.Load(); got != maxConcurrentRealtimePublishes {
		t.Fatalf("peak concurrent publishes=%d, want %d", got, maxConcurrentRealtimePublishes)
	}
}

func TestLeasedRealtimeOwnerPublishKeepsPartialSuccess(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	operators := make(map[string]realtimeNodeOperator)
	var successNodeID string
	for i, publish := range []func(context.Context) (int, error){
		func(context.Context) (int, error) {
			return 0, &realtime.ManagementError{StatusCode: http.StatusNotFound}
		},
		func(context.Context) (int, error) { return 0, errors.New("node unavailable") },
		func(context.Context) (int, error) { return 2, nil },
	} {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("node-%d", i), Active: true})
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			successNodeID = node.ID
		}
		operators[node.ID] = &publishingRealtimeNode{fakeRealtimeNode: &fakeRealtimeNode{}, publish: publish}
	}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	failSuccessNode := false
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		if failSuccessNode && node.ID == successNodeID {
			return nil, errors.New("node unavailable")
		}
		if operator := operators[node.ID]; operator != nil {
			return operator, nil
		}
		return nil, errors.New("unknown node")
	}
	queued, err := owner.Publish(ctx, "endpoint-1", "updates", realtime.Message{})
	if err != nil || queued != 2 {
		t.Fatalf("partial publish queued=%d err=%v, want 2/nil", queued, err)
	}

	failSuccessNode = true
	queued, err = owner.Publish(ctx, "endpoint-1", "updates", realtime.Message{})
	if queued != 0 || !errors.Is(err, errManagedRealtimeOwnerUnavailable) {
		t.Fatalf("failed publish queued=%d err=%v, want 0/unavailable", queued, err)
	}
}

func TestLeasedRealtimeOwnerPublishCanceledBeforeFanout(t *testing.T) {
	store := state.NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(state.ComputeNode) (realtimeNodeOperator, error) {
		return &fakeRealtimeNode{}, nil
	}
	queued, err := owner.Publish(ctx, "endpoint-1", "updates", realtime.Message{})
	if queued != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publish queued=%d err=%v, want 0/context canceled", queued, err)
	}
}

func TestLeasedRealtimeOwnerListsPartialConnectionInventory(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	nodeA, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-a", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	fakeA := &fakeRealtimeNode{connections: []realtime.ConnectionInfo{{ID: "conn-a", EndpointID: "endpoint-1"}}}
	fakeB := &fakeRealtimeNode{connErr: errors.New("node unavailable")}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		switch node.ID {
		case nodeA.ID:
			return fakeA, nil
		case nodeB.ID:
			return fakeB, nil
		default:
			return nil, errors.New("unknown node")
		}
	}
	inventory, err := owner.ListConnectionInventory(ctx)
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}
	activeNodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("list active nodes: %v", err)
	}
	wantUnavailable := len(activeNodes) - 1 // fakeA is the only healthy responder.
	if inventory.NodesQueried != 1 || inventory.NodesUnavailable != wantUnavailable || len(inventory.Connections) != 1 || inventory.Connections[0].ID != "conn-a" {
		t.Fatalf("inventory = %+v", inventory)
	}
}

func TestLeasedRealtimeOwnerDoesNotReportAbsentFromPartialFleet(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	nodeA, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-a", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		if node.ID == nodeA.ID {
			return &fakeRealtimeNode{}, nil
		}
		if node.ID == nodeB.ID {
			return &fakeRealtimeNode{connErr: errors.New("node unreachable")}, nil
		}
		return nil, errors.New("unknown node")
	}
	err = owner.Send(ctx, "endpoint", "connection", realtime.Message{Data: []byte("hello")})
	if !errors.Is(err, errManagedRealtimeOwnerUnavailable) {
		t.Fatalf("partial discovery = %v, want retryable unavailable", err)
	}
}

func TestLeasedRealtimeOwnerPublishReportsPartialFleet(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-a", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(node state.ComputeNode) (realtimeNodeOperator, error) {
		if node.ID == nodeB.ID {
			return &fakeRealtimeNode{publishErr: errors.New("node unreachable")}, nil
		}
		return &fakeRealtimeNode{}, nil
	}
	activeNodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := owner.PublishWithStatus(ctx, "endpoint", "updates", realtime.Message{Data: []byte("hello")})
	if err != nil || result.Queued != len(activeNodes)-1 || !result.Partial ||
		result.NodesQueried != len(activeNodes)-1 || result.NodesUnavailable != 1 {
		t.Fatalf("partial publish = (%+v, %v)", result, err)
	}
}

func TestLeasedRealtimeOwnerPublishWithNoKnownSubscribersReturnsEmptyStatus(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	activeNodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeNodes) == 0 {
		t.Fatal("NewMemStore should provide at least one active local node")
	}
	local := &fakeRealtimeNode{}
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.channelRoutingEnabled = true
	owner.channelRoutes = store
	for _, node := range activeNodes {
		owner.setChannelRoutesReady(node.ID, true)
	}
	owner.clientFor = func(state.ComputeNode) (realtimeNodeOperator, error) { return local, nil }

	result, err := owner.PublishWithStatus(ctx, "endpoint", "updates", realtime.Message{})
	if err != nil {
		t.Fatalf("PublishWithStatus: %v", err)
	}
	if result.Queued != 0 || result.NodesQueried != 0 || result.NodesUnavailable != 0 || result.Partial {
		t.Fatalf("empty publish status = %+v, want all zero values", result)
	}
	if local.pubs != 0 {
		t.Fatalf("published to %d nodes, want no publish for an empty subscriber set", local.pubs)
	}
}
