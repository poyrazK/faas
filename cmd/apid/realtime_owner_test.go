package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeRealtimeNode struct {
	connections []realtime.ConnectionInfo
	sends       int
	closes      int
	subs        int
	pubs        int
	registered  int
	removed     int
	connReads   int
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
	return 1, nil
}
func (f *fakeRealtimeNode) Connections(context.Context) ([]realtime.ConnectionInfo, error) {
	f.connReads++
	return append([]realtime.ConnectionInfo(nil), f.connections...), nil
}
func (f *fakeRealtimeNode) RegisterEndpoint(context.Context, realtime.Endpoint) error {
	f.registered++
	return nil
}
func (f *fakeRealtimeNode) RemoveEndpoint(context.Context, string) error { f.removed++; return nil }

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
