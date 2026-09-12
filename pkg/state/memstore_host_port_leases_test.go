package state

// adr: 177

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/hostport"
)

func TestMemStoreHostPortLeasesAreDurableUntilRelease(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	got, err := store.AcquireHostPortLeases(ctx, "node-a", "instance-a", []hostport.Request{
		{Name: "http", Protocol: hostport.TCP, GuestPort: 8080},
		{Name: "metrics", Protocol: hostport.TCP, GuestPort: 9090},
	})
	if err != nil {
		t.Fatalf("AcquireHostPortLeases: %v", err)
	}
	if len(got) != 2 || got[0].HostPort == 0 || got[1].HostPort == 0 {
		t.Fatalf("unexpected leases: %+v", got)
	}
	reloaded, err := store.ListHostPortLeases(ctx, "node-a", "instance-a")
	if err != nil {
		t.Fatalf("ListHostPortLeases: %v", err)
	}
	if len(reloaded) != 2 || reloaded[0].HostPort != got[0].HostPort {
		t.Fatalf("list did not preserve mappings: got=%+v want=%+v", reloaded, got)
	}
	if err := store.ReleaseHostPortLeases(ctx, "node-a", "instance-a"); err != nil {
		t.Fatalf("ReleaseHostPortLeases: %v", err)
	}
	remaining, err := store.ListHostPortLeases(ctx, "node-a", "instance-a")
	if err != nil {
		t.Fatalf("ListHostPortLeases after release: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("release left leases behind: %+v", remaining)
	}
}

func TestMemStoreHostPortLeasesIsolateNodes(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	left, err := store.AcquireHostPortLeases(ctx, "node-a", "instance-a", []hostport.Request{{Protocol: hostport.TCP, GuestPort: 80}})
	if err != nil {
		t.Fatalf("node-a AcquireHostPortLeases: %v", err)
	}
	right, err := store.AcquireHostPortLeases(ctx, "node-b", "instance-b", []hostport.Request{{Protocol: hostport.TCP, GuestPort: 80}})
	if err != nil {
		t.Fatalf("node-b AcquireHostPortLeases: %v", err)
	}
	if left[0].HostPort != right[0].HostPort {
		t.Fatalf("node-local lease ranges should be independently reusable: left=%+v right=%+v", left, right)
	}
}
